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
	"net/http"
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
	Concurrency int           // max in-flight requests
	Timeout     time.Duration // per-request timeout
}

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
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 16
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if client == nil {
		client = http.DefaultClient
	}

	ticks := make(chan struct{})
	samples := make(chan sample, 1024)
	var wg sync.WaitGroup
	for range cfg.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range ticks {
				samples <- do(ctx, cfg, client)
			}
		}()
	}

	start := time.Now()
	go func() {
		interval := time.Duration(float64(time.Second) / cfg.RPS)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		deadline := time.After(cfg.Duration)
		for {
			select {
			case <-ctx.Done():
				close(ticks)
				return
			case <-deadline:
				close(ticks)
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

func do(ctx context.Context, cfg Config, client *http.Client) sample {
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
	body, err := io.ReadAll(resp.Body)
	latency := time.Since(t0)
	if err != nil {
		return sample{err: err}
	}
	return sample{latency: latency, status: resp.StatusCode, bytes: int64(len(body)), bids: hasBids(body)}
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
