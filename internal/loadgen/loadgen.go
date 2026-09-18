// Package loadgen is a small fixed-rate HTTP load generator with latency percentiles, used by
// scripts/perf-docker.sh to put a bounded, reproducible load on Prebid Server.
package loadgen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config describes one load run.
type Config struct {
	URL         string
	Body        []byte
	Headers     map[string]string
	RPS         float64       // target request rate (open loop, bounded by Concurrency)
	Duration    time.Duration // how long to schedule requests
	Concurrency int           // max in-flight requests (goroutines parked on I/O; unrelated to GOMAXPROCS)
	Timeout     time.Duration // per-request timeout
	// MaxResponseBytes bounds the bytes buffered per response; a larger response counts as an error.
	// 0 selects DefaultMaxResponseBytes.
	MaxResponseBytes int64
}

// DefaultMaxResponseBytes is the response size cap used when Config.MaxResponseBytes is 0.
const DefaultMaxResponseBytes = 4 << 20

// MaxRPS bounds the target rate: above it the ticker interval would be shorter than 1 ms, which is
// meaningless for an HTTP auction and, past 1e9, would collapse to a non-positive ticker interval.
const MaxRPS = 1000.0

// ErrResponseTooLarge is returned (as a per-request error) when a response exceeds MaxResponseBytes.
var ErrResponseTooLarge = errors.New("response exceeds the configured size limit")

// Report is the outcome of a run.
type Report struct {
	Requests    int            `json:"requests"`
	OK          int            `json:"ok"`        // HTTP 2xx
	Non2xx      int            `json:"non_2xx"`   // any other status
	Errors      int            `json:"errors"`    // transport errors / timeouts
	WithBids    int            `json:"with_bids"` // 2xx responses whose body has a non-empty seatbid
	Statuses    map[int]int    `json:"statuses"`
	Elapsed     time.Duration  `json:"elapsed_ns"`
	AchievedRPS float64        `json:"achieved_rps"`
	TargetRPS   float64        `json:"target_rps"`
	Latency     LatencySummary `json:"latency"`
	BytesIn     int64          `json:"bytes_in"`
}

// LatencySummary holds latency percentiles of successful and non-2xx responses (errors excluded).
type LatencySummary struct {
	Min  time.Duration `json:"min_ns"`
	Mean time.Duration `json:"mean_ns"`
	P50  time.Duration `json:"p50_ns"`
	P90  time.Duration `json:"p90_ns"`
	P95  time.Duration `json:"p95_ns"`
	P99  time.Duration `json:"p99_ns"`
	Max  time.Duration `json:"max_ns"`
}

// String renders a human-readable summary.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "requests=%d ok=%d non2xx=%d errors=%d with_bids=%d\n", r.Requests, r.OK, r.Non2xx, r.Errors, r.WithBids)
	fmt.Fprintf(&b, "elapsed=%s target_rps=%.1f achieved_rps=%.1f bytes_in=%d\n", r.Elapsed.Round(time.Millisecond), r.TargetRPS, r.AchievedRPS, r.BytesIn)
	fmt.Fprintf(&b, "latency min=%s mean=%s p50=%s p90=%s p95=%s p99=%s max=%s\n",
		ms(r.Latency.Min), ms(r.Latency.Mean), ms(r.Latency.P50), ms(r.Latency.P90), ms(r.Latency.P95), ms(r.Latency.P99), ms(r.Latency.Max))
	if len(r.Statuses) > 0 {
		keys := make([]int, 0, len(r.Statuses))
		for k := range r.Statuses {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		fmt.Fprintf(&b, "statuses:")
		for _, k := range keys {
			fmt.Fprintf(&b, " %d=%d", k, r.Statuses[k])
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond)) }

type sample struct {
	latency time.Duration
	status  int
	bytes   int64
	bids    bool
	err     error
}

// Run executes the load described by cfg using client and returns the aggregated report.
func Run(ctx context.Context, cfg Config, client *http.Client) (Report, error) {
	if cfg.RPS <= 0 || cfg.Duration <= 0 || cfg.URL == "" {
		return Report{}, errors.New("loadgen: URL, RPS and Duration are required")
	}
	if math.IsInf(cfg.RPS, 0) || math.IsNaN(cfg.RPS) || cfg.RPS > MaxRPS {
		return Report{}, fmt.Errorf("loadgen: RPS must be a finite number ≤ %v, got %v", MaxRPS, cfg.RPS)
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 16
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if client == nil {
		client = http.DefaultClient
	}

	// Both channels are allocated once per run (a chan of zero-size elements carries no payload), so
	// they add no GC pressure; per-request allocations are the HTTP request/response themselves.
	ticks := make(chan struct{})
	samples := make(chan sample, 1024)
	var wg sync.WaitGroup
	for range cfg.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range ticks {
				// Requests derive from ctx, not from the scheduling context: a request in flight when the
				// scheduling window closes is allowed to finish and be counted.
				samples <- sendRequest(ctx, cfg, client)
			}
		}()
	}

	// The scheduling window is a context so cancellation and the duration share one mechanism.
	schedCtx, cancelSched := context.WithTimeout(ctx, cfg.Duration)
	defer cancelSched()

	start := time.Now()
	go func() {
		defer close(ticks)
		interval := max(time.Duration(float64(time.Second)/cfg.RPS), time.Millisecond) // NewTicker panics on <= 0
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-schedCtx.Done():
				return
			case <-ticker.C:
				select {
				case ticks <- struct{}{}: // a worker is free
				default: // all workers busy: open-loop tick dropped, achieved_rps will show it
				}
			}
		}
	}()

	go func() {
		wg.Wait()
		close(samples)
	}()

	report := Report{Statuses: map[int]int{}, TargetRPS: cfg.RPS}
	var lat []time.Duration
	for s := range samples {
		report.Requests++
		if s.err != nil {
			report.Errors++
			continue
		}
		lat = append(lat, s.latency)
		report.BytesIn += s.bytes
		report.Statuses[s.status]++
		if s.status >= 200 && s.status < 300 {
			report.OK++
			if s.bids {
				report.WithBids++
			}
		} else {
			report.Non2xx++
		}
	}
	report.Elapsed = time.Since(start)
	if report.Elapsed > 0 {
		report.AchievedRPS = float64(report.Requests) / report.Elapsed.Seconds()
	}
	report.Latency = summarize(lat)
	return report, nil
}

// sendRequest performs one request with its own timeout and returns the measured sample.
func sendRequest(ctx context.Context, cfg Config, client *http.Client) sample {
	reqCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.URL, bytes.NewReader(cfg.Body))
	if err != nil {
		return sample{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	t0 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return sample{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readBounded(resp.Body, cfg.MaxResponseBytes)
	latency := time.Since(t0)
	if err != nil {
		return sample{err: err}
	}
	return sample{latency: latency, status: resp.StatusCode, bytes: int64(len(body)), bids: hasBids(body)}
}

// readBounded reads at most limit bytes; one byte more is read to detect (and reject) larger inputs.
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w (%d bytes)", ErrResponseTooLarge, limit)
	}
	return body, nil
}

// ReadBodyFile loads a request body from disk, refusing files larger than limit bytes
// (Prebid Server rejects bodies above max_request_size anyway).
func ReadBodyFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // path is an operator-supplied CLI argument
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	body, err := readBounded(f, limit)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("read %s: empty body", path)
	}
	return body, nil
}

func hasBids(body []byte) bool {
	var r struct {
		SeatBid []json.RawMessage `json:"seatbid"`
	}
	return json.Unmarshal(body, &r) == nil && len(r.SeatBid) > 0
}

func summarize(lat []time.Duration) LatencySummary {
	if len(lat) == 0 {
		return LatencySummary{}
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	var sum time.Duration
	for _, l := range lat {
		sum += l
	}
	return LatencySummary{
		Min:  lat[0],
		Mean: sum / time.Duration(len(lat)),
		P50:  Percentile(lat, 50),
		P90:  Percentile(lat, 90),
		P95:  Percentile(lat, 95),
		P99:  Percentile(lat, 99),
		Max:  lat[len(lat)-1],
	}
}

// Percentile returns the nearest-rank percentile of an ascending-sorted slice.
func Percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	rank := int(p/100*float64(len(sorted))+0.999999) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
