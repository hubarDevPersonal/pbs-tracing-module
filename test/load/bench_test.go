//go:build load

package load

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bench flags. The image must be built with the loadbench rules: `make load-bench` does both.
var (
	benchImage    = flag.String("bench-image", "pbs-tracer:loadbench", "image built with --build-arg GO_TAGS=loadbench")
	benchRPS      = flag.Float64("bench-rps", 100, "auctions per second per scenario")
	benchDuration = flag.Duration("bench-duration", 20*time.Second, "length of each scenario")
	benchWorkers  = flag.Int("bench-concurrency", 64, "max in-flight auctions")
	stubAddr      = flag.String("stub-addr", ":18081", "listen address of the stub bidder (deploy/pbs.load.yaml points at port 18081)")
	stubLatency   = flag.Duration("stub-latency", 10*time.Millisecond, "delay of every stub bidder answer, standing in for the network")
)

const (
	benchPartner = "load-partner-" // loadbench rules: three partners with limits that outlast the run
	paddingBytes = 60 << 10        // large payload: below max_request_size (256 KiB)

	metricHookCount = "prebid_server_modules_test_provider_test_tracer_duration_count"
	metricHookSum   = "prebid_server_modules_test_provider_test_tracer_duration_sum"
	metricTimeouts  = "prebid_server_modules_test_provider_test_tracer_timeouts"
	metricFailed    = "prebid_server_modules_test_provider_test_tracer_failed"
	metricErrors    = "prebid_server_modules_test_provider_test_tracer_execution_errors"
)

type traces int

const (
	tracesNone    traces = iota // no packet on stdout
	tracesEvery                 // one packet per auction
	tracesUnknown               // stdout is not observable (stalled)
)

type benchScenario struct {
	name    string
	env     []string // PBS_* overrides
	bodies  [][]byte
	stalled bool // stdout nobody reads
	hooks   bool // the module's hooks run
	traces  traces
}

type benchResult struct {
	scenario   string
	report     Report
	hookCalls  float64
	hookMean   time.Duration
	hookBad    float64 // timeouts + failures + execution errors, must be 0
	gcCycles   float64 // during the run
	rssBytes   int64   // after the run, docker stats (includes page cache)
	traceLines int
	dropped    int
}

// L-05, L-06, L-07, L-08, L-09 (docs/test-specs/load.md). TestLoadBench measures Prebid Server with the module against
// stub bidders on the host, one fresh container per scenario, and compares hooks off, hooks on with
// nothing traced, active tracing for one and for three partners, a large payload, and a stdout that
// nobody reads. It fails on any auction error, on a trace count that does not match the scenario, and
// on a stalled stdout that is not survived; latency and memory are reported side by side.
//
//	make load-bench    # builds pbs-tracer:loadbench, then: go test -tags load -run TestLoadBench ./test/load
func TestLoadBench(t *testing.T) {
	raw, err := os.ReadFile("../../docs/assessment/01-bid-request-example.json")
	require.NoError(t, err)
	// The sample asks for ext.prebid.debug and trace: "verbose". With hooks on, PBS then appends a
	// per-invocation hook trace to every response, which would make "hooks off" and "hooks on"
	// compare different responses. Production traffic carries neither, so the bench strips them.
	sample := withoutDebug(t, raw)
	startStubBidder(t, *stubAddr, *stubLatency)

	scenarios := []benchScenario{
		{name: "hooks off", env: []string{"PBS_HOOKS_ENABLED=false"}, bodies: [][]byte{sample}, traces: tracesNone},
		{name: "hooks on, untraced account", bodies: [][]byte{sample}, hooks: true, traces: tracesNone},
		{name: "active, one partner", bodies: [][]byte{withPartner(t, sample, benchPartner+"1")}, hooks: true, traces: tracesEvery},
		{name: "active, three partners", bodies: [][]byte{
			withPartner(t, sample, benchPartner+"1"), withPartner(t, sample, benchPartner+"2"), withPartner(t, sample, benchPartner+"3"),
		}, hooks: true, traces: tracesEvery},
		{name: "active, large payload", bodies: [][]byte{withPadding(t, withPartner(t, sample, benchPartner+"1"), paddingBytes)}, hooks: true, traces: tracesEvery},
		{name: "active, stalled stdout", bodies: [][]byte{withPartner(t, sample, benchPartner+"1")}, stalled: true, hooks: true, traces: tracesUnknown},
	}

	results := make([]benchResult, 0, len(scenarios))
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			results = append(results, runScenario(t, sc))
		})
	}
	t.Log("\n" + comparison(results))
}

func runScenario(t *testing.T, sc benchScenario) benchResult {
	t.Helper()
	pbs := startPBS(t, *benchImage, sc.env, sc.stalled)
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *benchWorkers, MaxConnsPerHost: *benchWorkers}}

	// warm-up: connection pools on both sides, JIT-free but page-cache and GC state settle
	for range 20 {
		auction(t, client, pbs.URL, sc.bodies[0])
	}
	before := pbs.metrics(t, metricHookCount, metricHookSum, metricTimeouts, metricFailed, metricErrors)
	memBefore := pbs.memStats(t)
	stdoutBefore, _ := containerLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), *benchDuration+30*time.Second)
	defer cancel()
	report, err := Run(ctx, Config{
		URL:              pbs.URL + "/openrtb2/auction",
		Bodies:           sc.bodies,
		RPS:              *benchRPS,
		Duration:         *benchDuration,
		Concurrency:      *benchWorkers,
		Timeout:          10 * time.Second,
		MaxResponseBytes: 4 << 20,
	}, client)
	require.NoError(t, err)

	time.Sleep(time.Second) // let the last packets reach stdout
	after := pbs.metrics(t, metricHookCount, metricHookSum, metricTimeouts, metricFailed, metricErrors)
	memAfter := pbs.memStats(t)
	stdout, stderr := containerLogs(t)
	res := benchResult{
		scenario:  sc.name,
		report:    report,
		hookCalls: after[metricHookCount] - before[metricHookCount],
		hookBad: after[metricTimeouts] - before[metricTimeouts] + after[metricFailed] - before[metricFailed] +
			after[metricErrors] - before[metricErrors],
		gcCycles:   memAfter.NumGC - memBefore.NumGC,
		rssBytes:   rss(t),
		traceLines: bytes.Count(stdout[len(stdoutBefore):], []byte("\n")),
		dropped:    droppedPackets(stderr),
	}
	if res.hookCalls > 0 {
		res.hookMean = time.Duration((after[metricHookSum] - before[metricHookSum]) / res.hookCalls * float64(time.Second))
	}
	t.Log("\n" + report.String())

	// every auction answered, at the target rate
	assert.Zero(t, report.Errors, "transport errors or timeouts")
	assert.Zero(t, report.Non2xx, "non-2xx statuses: %v", report.Statuses)
	assert.Zero(t, report.Dropped, "arrivals dropped by the generator: raise -bench-concurrency")
	assert.Equal(t, report.Requests, report.WithBids, "every auction carries the stub bids")

	// the module ran exactly when it should
	if sc.hooks {
		assert.Positive(t, res.hookCalls, "hook invocations")
		assert.Zero(t, res.hookBad, "hook timeouts, failures or execution errors")
	} else {
		assert.Zero(t, res.hookCalls, "hooks must not run with hooks disabled")
	}
	switch sc.traces {
	case tracesNone:
		assert.Zero(t, res.traceLines, "no packet expected on stdout")
		assert.Zero(t, res.dropped)
	case tracesEvery:
		assert.Equal(t, report.Requests, res.traceLines, "one packet per auction on stdout")
		assert.Zero(t, res.dropped, "packets dropped although stdout drains")
	case tracesUnknown:
		assert.Positive(t, res.dropped, "a stalled stdout must show up as dropped packets in the PBS log")
	}
	return res
}

func auction(t *testing.T, client *http.Client, base string, body []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/openrtb2/auction", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, err := readLimited(resp.Body, 4<<20)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "auction: %s", out)
}

// withPartner sets the account the sample resolves to (site.publisher.ext.prebid.parentAccount).
func withPartner(t *testing.T, body []byte, partner string) []byte {
	t.Helper()
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	site := req["site"].(map[string]any)
	pub := site["publisher"].(map[string]any)
	pub["ext"] = map[string]any{"prebid": map[string]any{"parentAccount": partner}}
	out, err := json.Marshal(req)
	require.NoError(t, err)
	return out
}

var droppedRe = regexp.MustCompile(`\((\d+) dropped so far\)`)

// droppedPackets reads the module's drop counter from the PBS log; the warning is rate-limited, so
// the last occurrence carries the total.
func droppedPackets(log []byte) int {
	n := 0
	for _, m := range droppedRe.FindAllSubmatch(log, -1) {
		if v, err := strconv.Atoi(string(m[1])); err == nil && v > n {
			n = v
		}
	}
	return n
}

// withoutDebug removes ext.prebid.debug and ext.prebid.trace from the request.
func withoutDebug(t *testing.T, body []byte) []byte {
	t.Helper()
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	if ext, ok := req["ext"].(map[string]any); ok {
		if prebid, ok := ext["prebid"].(map[string]any); ok {
			delete(prebid, "debug")
			delete(prebid, "trace")
		}
	}
	out, err := json.Marshal(req)
	require.NoError(t, err)
	return out
}

// withPadding grows the request by n bytes of opaque site.ext data, which PBS keeps and forwards.
func withPadding(t *testing.T, body []byte, n int) []byte {
	t.Helper()
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	site := req["site"].(map[string]any)
	site["ext"] = map[string]any{"padding": strings.Repeat("x", n)}
	out, err := json.Marshal(req)
	require.NoError(t, err)
	return out
}

func comparison(results []benchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "| scenario | requests | bytes/resp | p50 | p95 | p99 | max | hook calls | hook mean | traces | dropped | GC cycles | memory |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range results {
		l := r.report.Latency
		perResp := int64(0)
		if r.report.Requests > 0 {
			perResp = r.report.BytesIn / int64(r.report.Requests)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %s | %s | %s | %.0f | %s | %d | %d | %.0f | %s |\n",
			r.scenario, r.report.Requests, perResp, ms(l.P50), ms(l.P95), ms(l.P99), ms(l.Max),
			r.hookCalls, r.hookMean.Round(time.Microsecond), r.traceLines, r.dropped, r.gcCycles, mib(r.rssBytes))
	}
	return b.String()
}
