//go:build load

package load

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// High-load flags. Defaults fit a 4-CPU Docker VM: the top rate saturates it, the closed loop finds
// the throughput ceiling. The load generator and the stub bidder run on the host, outside the VM.
var (
	hlRates       = flag.String("hl-rates", "200,400,800,1600", "open-loop arrival rates (auctions/s), one step each")
	hlClosed      = flag.Int("hl-closed", 128, "closed-loop step: requests kept in flight; 0 skips the step")
	hlDuration    = flag.Duration("hl-duration", 15*time.Second, "length of one run")
	hlRepeats     = flag.Int("hl-repeats", 3, "runs per step and configuration; medians are reported")
	hlConcurrency = flag.Int("hl-concurrency", 512, "max in-flight requests of the open-loop steps")
	hlReport      = flag.String("hl-report", "", "write the markdown report to this file (empty: test log only)")
	hlProfile     = flag.Int("hl-profile", 10, "seconds of CPU profile captured during the first closed-loop run of each configuration; 0 disables")
)

// sustainedShare is how much of the target rate an open-loop run must reach to count as sustained.
const sustainedShare = 0.95

type hlConfig struct {
	name   string
	env    []string
	bodies [][]byte
	hooks  bool // the module's hooks run
	traced bool // every auction is traced
}

type hlStep struct {
	name   string
	rate   float64 // 0 = closed loop
	closed bool
}

// hlRun is one measured run.
type hlRun struct {
	report    Report
	cpu       time.Duration // PBS CPU time during the run
	hookCalls float64
	hookMean  time.Duration
	hookBad   float64
	gcCycles  float64
	mem       int64
	traces    int // packets on stdout during the run
	dropped   int // packets the module dropped, from its own counter
	sustained bool
}

func (r hlRun) cpuPerAuction() time.Duration {
	if r.report.Requests == 0 {
		return 0
	}
	return r.cpu / time.Duration(r.report.Requests)
}

func (r hlRun) cpuCores() float64 {
	if r.report.Elapsed == 0 {
		return 0
	}
	return r.cpu.Seconds() / r.report.Elapsed.Seconds()
}

// hlCell is the aggregate of the repeats of one configuration at one step.
type hlCell struct {
	config  string
	step    string
	runs    []hlRun
	median  hlRun // per-field medians of the runs
	profile string
	module  string // pprof share of samples on paths through the module
}

// L-10, L-11, L-12 (workspace/test-specs/load.md). TestHighload measures Prebid Server with the
// module against stub bidders on a ladder of arrival rates up to saturation and in a closed loop at
// fixed concurrency, for hooks off, hooks on with nothing traced, and active tracing on every
// auction. Each cell is repeated and reported as medians with the spread; PBS's CPU time is read
// from the container's cgroup so the cost per auction is measured, not inferred from latency.
//
//	make highload    # builds pbs-tracer:loadbench, then: go test -tags load -run TestHighload ./test/load
func TestHighload(t *testing.T) {
	raw, err := os.ReadFile("../../workspace/assessment/01-bid-request-example.json")
	require.NoError(t, err)
	sample := withoutDebug(t, raw)
	startStubBidder(t, *stubAddr, *stubLatency)
	cpus := dockerCPUs(t)
	profileDir := t.TempDir()

	configs := []hlConfig{
		{name: "hooks off", env: []string{"PBS_HOOKS_ENABLED=false"}, bodies: [][]byte{sample}},
		{name: "hooks on, untraced", bodies: [][]byte{sample}, hooks: true},
		{name: "active tracing", bodies: [][]byte{withPartner(t, sample, benchPartner+"1")}, hooks: true, traced: true},
	}
	var steps []hlStep
	for _, r := range strings.Split(*hlRates, ",") {
		rate, err := strconv.ParseFloat(strings.TrimSpace(r), 64)
		require.NoError(t, err, "-hl-rates")
		steps = append(steps, hlStep{name: fmt.Sprintf("%.0f rps", rate), rate: rate})
	}
	if *hlClosed > 0 {
		steps = append(steps, hlStep{name: fmt.Sprintf("closed loop, %d in flight", *hlClosed), closed: true})
	}

	var cells []hlCell
	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			pbs := startPBS(t, *benchImage, cfg.env, false)
			client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *hlConcurrency, MaxConnsPerHost: *hlConcurrency}}
			for range 50 { // warm-up
				auction(t, client, pbs.URL, cfg.bodies[0])
			}
			for _, step := range steps {
				cell := hlCell{config: cfg.name, step: step.name}
				for i := range *hlRepeats {
					profile := step.closed && i == 0 && *hlProfile > 0
					run, prof := hlMeasure(t, pbs, client, cfg, step, profile, profileDir)
					cell.runs = append(cell.runs, run)
					if prof != "" {
						cell.profile, cell.module = pprofTop(t, prof, 12)
					}
					time.Sleep(2 * time.Second) // let connections and GC settle between runs
				}
				cell.median = hlMedian(cell.runs)
				hlAssert(t, cfg, step, cell)
				t.Logf("%s @ %s: %s", cfg.name, step.name, hlLine(cell.median))
				cells = append(cells, cell)
			}
		})
	}

	report := hlReportMarkdown(cells, configs, steps, cpus)
	t.Log("\n" + report)
	if *hlReport != "" {
		require.NoError(t, os.WriteFile(*hlReport, []byte(report), 0o600))
		t.Logf("report written to %s", *hlReport)
	}
}

// hlMeasure performs one run and reads every counter around it.
func hlMeasure(t *testing.T, pbs *pbsContainer, client *http.Client, cfg hlConfig, step hlStep, profile bool, profileDir string) (hlRun, string) {
	t.Helper()
	before := pbs.metrics(t, metricHookCount, metricHookSum, metricTimeouts, metricFailed, metricErrors)
	memBefore := pbs.memStats(t)
	cpuBefore := cpuUsage(t)
	stdoutBefore, stderrBefore := settledLogs(t) // the previous run's tail must have landed, or it counts here

	var profPath string
	profDone := make(chan struct{})
	if profile {
		go func() {
			defer close(profDone)
			profPath = pbs.cpuProfile(t, profileDir, min(*hlProfile, int(hlDuration.Seconds())))
		}()
	} else {
		close(profDone)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *hlDuration+30*time.Second)
	defer cancel()
	report, err := Run(ctx, Config{
		URL:              pbs.URL + "/openrtb2/auction",
		Bodies:           cfg.bodies,
		RPS:              step.rate,
		ClosedLoop:       step.closed,
		Duration:         *hlDuration,
		Concurrency:      hlWorkers(step),
		Timeout:          10 * time.Second,
		MaxResponseBytes: 4 << 20,
	}, client)
	require.NoError(t, err)
	<-profDone

	cpu := cpuUsage(t) - cpuBefore
	stdout, stderr := settledLogs(t)
	after := pbs.metrics(t, metricHookCount, metricHookSum, metricTimeouts, metricFailed, metricErrors)
	memAfter := pbs.memStats(t)

	run := hlRun{
		report:    report,
		cpu:       cpu,
		hookCalls: after[metricHookCount] - before[metricHookCount],
		hookBad: after[metricTimeouts] - before[metricTimeouts] + after[metricFailed] - before[metricFailed] +
			after[metricErrors] - before[metricErrors],
		gcCycles: memAfter.NumGC - memBefore.NumGC,
		mem:      rss(t),
		traces:   strings.Count(string(stdout[len(stdoutBefore):]), "\n"),
		dropped:  droppedPackets(stderr) - droppedPackets(stderrBefore),
	}
	if run.hookCalls > 0 {
		run.hookMean = time.Duration((after[metricHookSum] - before[metricHookSum]) / run.hookCalls * float64(time.Second))
	}
	if step.closed {
		run.sustained = report.Errors == 0
	} else {
		run.sustained = report.Errors == 0 && report.Dropped == 0 && report.AchievedRPS >= sustainedShare*step.rate
	}
	return run, profPath
}

// settledLogs reads the container logs once their stdout line count has stopped growing: the module's
// queue and Docker's log driver both trail the last response. Three unchanged polls in a row, because
// a log driver on a loaded host stalls for longer than one poll interval and one quiet poll is not
// the end of the run.
func settledLogs(t *testing.T) (stdout, stderr []byte) {
	t.Helper()
	const stablePolls = 3
	last, stable := -1, 0
	for range 40 {
		time.Sleep(500 * time.Millisecond)
		stdout, stderr = containerLogs(t)
		if n := strings.Count(string(stdout), "\n"); n == last {
			if stable++; stable == stablePolls {
				return stdout, stderr
			}
		} else {
			last, stable = n, 0
		}
	}
	return stdout, stderr
}

func hlWorkers(step hlStep) int {
	if step.closed {
		return *hlClosed
	}
	return *hlConcurrency
}

// hlAssert fails on what must hold regardless of the numbers.
func hlAssert(t *testing.T, cfg hlConfig, step hlStep, cell hlCell) {
	t.Helper()
	m := cell.median
	assert.Zero(t, m.report.Non2xx, "%s @ %s: non-2xx statuses %v", cfg.name, step.name, m.report.Statuses)
	if cfg.hooks {
		assert.Positive(t, m.hookCalls, "%s @ %s: hooks did not run", cfg.name, step.name)
	} else {
		assert.Zero(t, m.hookCalls, "%s @ %s: hooks ran although disabled", cfg.name, step.name)
	}
	if !m.sustained {
		return // saturation: latency and errors are what the report is about
	}
	// Hook timeouts are a property of the group timeout (50 ms in the bench configuration) under CPU
	// pressure, not of the module's work (tens of µs): they are reported at every step and must be
	// absent only where the server is not CPU-bound.
	if m.cpuCores() < 0.5 {
		assert.Zero(t, m.hookBad, "%s @ %s: hook timeouts, failures or execution errors on an idle server", cfg.name, step.name)
	}
	if cfg.traced {
		// docker logs is read after the queue settled; at thousands of packets per second the log
		// driver can still lag by a few, hence the tolerance
		assert.InDelta(t, m.report.Requests, m.traces+m.dropped, 0.005*float64(m.report.Requests)+1,
			"%s @ %s: every auction is either written or counted as dropped", cfg.name, step.name)
	} else {
		assert.Zero(t, m.traces, "%s @ %s: packet on stdout without tracing", cfg.name, step.name)
	}
}

// hlMedian builds a run whose numeric fields are the per-field medians of runs; counts that must be
// zero (errors, drops, bad hooks) take their maximum so nothing is averaged away.
func hlMedian(runs []hlRun) hlRun {
	med := func(f func(hlRun) float64) float64 {
		v := make([]float64, 0, len(runs))
		for _, r := range runs {
			v = append(v, f(r))
		}
		slices.Sort(v)
		return v[len(v)/2]
	}
	maxOf := func(f func(hlRun) int) int {
		m := 0
		for _, r := range runs {
			m = max(m, f(r))
		}
		return m
	}
	dur := func(f func(hlRun) time.Duration) time.Duration {
		return time.Duration(med(func(r hlRun) float64 { return float64(f(r)) }))
	}
	m := hlRun{
		cpu:       dur(func(r hlRun) time.Duration { return r.cpu }),
		hookCalls: med(func(r hlRun) float64 { return r.hookCalls }),
		hookMean:  dur(func(r hlRun) time.Duration { return r.hookMean }),
		hookBad:   float64(maxOf(func(r hlRun) int { return int(r.hookBad) })),
		gcCycles:  med(func(r hlRun) float64 { return r.gcCycles }),
		mem:       int64(med(func(r hlRun) float64 { return float64(r.mem) })),
		traces:    int(med(func(r hlRun) float64 { return float64(r.traces) })),
		dropped:   maxOf(func(r hlRun) int { return r.dropped }),
		sustained: true,
	}
	m.report = Report{
		Requests:    int(med(func(r hlRun) float64 { return float64(r.report.Requests) })),
		Errors:      maxOf(func(r hlRun) int { return r.report.Errors }),
		Non2xx:      maxOf(func(r hlRun) int { return r.report.Non2xx }),
		Dropped:     maxOf(func(r hlRun) int { return r.report.Dropped }),
		Elapsed:     dur(func(r hlRun) time.Duration { return r.report.Elapsed }),
		AchievedRPS: med(func(r hlRun) float64 { return r.report.AchievedRPS }),
		Latency: Latency{
			P50: dur(func(r hlRun) time.Duration { return r.report.Latency.P50 }),
			P90: dur(func(r hlRun) time.Duration { return r.report.Latency.P90 }),
			P99: dur(func(r hlRun) time.Duration { return r.report.Latency.P99 }),
			Max: dur(func(r hlRun) time.Duration { return r.report.Latency.Max }),
		},
	}
	for _, r := range runs {
		m.sustained = m.sustained && r.sustained
	}
	return m
}

func hlLine(r hlRun) string {
	return fmt.Sprintf("achieved=%.0f rps p50=%s p99=%s cpu/auction=%s cores=%.2f errors=%d dropped=%d hookBad=%.0f traces=%d packetsDropped=%d sustained=%v",
		r.report.AchievedRPS, ms(r.report.Latency.P50), ms(r.report.Latency.P99), r.cpuPerAuction().Round(time.Microsecond), r.cpuCores(),
		r.report.Errors, r.report.Dropped, r.hookBad, r.traces, r.dropped, r.sustained)
}

// hlReportMarkdown renders every cell, the module's cost derived from hooks off, and the profiles.
func hlReportMarkdown(cells []hlCell, configs []hlConfig, steps []hlStep, cpus int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# High-load run %s\n\n", time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "Docker: %d CPUs for Prebid Server. Stub bidder delay %s. %d runs of %s per cell; medians, with min–max where it matters. "+
		"Open-loop steps hold an arrival rate; a step is *sustained* when the achieved rate is ≥ %.0f %% of the target with no errors and no dropped arrivals. "+
		"The closed loop keeps %d requests in flight and measures throughput. CPU is Prebid Server's cgroup time; the generator and the stub run outside the VM.\n\n",
		cpus, *stubLatency, *hlRepeats, *hlDuration, sustainedShare*100, *hlClosed)

	find := func(cfg, step string) *hlCell {
		for i := range cells {
			if cells[i].config == cfg && cells[i].step == step {
				return &cells[i]
			}
		}
		return nil
	}
	spread := func(runs []hlRun, f func(hlRun) time.Duration) string {
		lo, hi := f(runs[0]), f(runs[0])
		for _, r := range runs {
			lo, hi = min(lo, f(r)), max(hi, f(r))
		}
		return fmt.Sprintf("%s–%s", ms(lo), ms(hi))
	}
	for _, step := range steps {
		fmt.Fprintf(&b, "## %s\n\n", step.name)
		fmt.Fprintf(
			&b,
			"| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |\n",
		)
		fmt.Fprintf(&b, "|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, cfg := range configs {
			c := find(cfg.name, step.name)
			if c == nil {
				continue
			}
			m := c.median
			fmt.Fprintf(&b, "| %s | %.0f | %s | %s | %s | %s (%s) | %s | %s | %.2f | %s | %.0f | %d | %d | %d | %d | %.0f | %s |\n",
				cfg.name, m.report.AchievedRPS, yesNo(m.sustained), ms(m.report.Latency.P50), ms(m.report.Latency.P90),
				ms(m.report.Latency.P99), spread(c.runs, func(r hlRun) time.Duration { return r.report.Latency.P99 }),
				ms(m.report.Latency.Max), m.cpuPerAuction().Round(time.Microsecond), m.cpuCores(), m.hookMean.Round(time.Microsecond),
				m.hookBad, m.report.Errors, m.report.Dropped, m.traces, m.dropped, m.gcCycles, mib(m.mem))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Module cost against hooks off\n\n")
	fmt.Fprintf(&b, "| step | configuration | Δ CPU/auction | Δ p50 | Δ p99 | Δ throughput |\n|---|---|---:|---:|---:|---:|\n")
	for _, step := range steps {
		base := find(configs[0].name, step.name)
		if base == nil {
			continue
		}
		for _, cfg := range configs[1:] {
			c := find(cfg.name, step.name)
			if c == nil {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %+.0f rps |\n", step.name, cfg.name,
				signed(c.median.cpuPerAuction()-base.median.cpuPerAuction()),
				signed(c.median.report.Latency.P50-base.median.report.Latency.P50),
				signed(c.median.report.Latency.P99-base.median.report.Latency.P99),
				c.median.report.AchievedRPS-base.median.report.AchievedRPS)
		}
	}
	b.WriteString("\n")

	for _, c := range cells {
		if c.profile == "" {
			continue
		}
		fmt.Fprintf(&b, "## CPU profile: %s @ %s\n\n", c.config, c.step)
		if c.module != "" {
			fmt.Fprintf(&b, "Paths through the module: %s\n\n", c.module)
		}
		fmt.Fprintf(&b, "```\n%s```\n\n", c.profile)
	}
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func signed(d time.Duration) string {
	if d < 0 {
		return "-" + ms(-d)
	}
	return "+" + ms(d)
}
