package load

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// ErrResponseTooLarge marks a response above Config.MaxResponseBytes.
var ErrResponseTooLarge = errors.New("response exceeds MaxResponseBytes")

type sample struct {
	latency time.Duration
	status  int
	bytes   int
	bids    bool
	err     error
}

// Run sends cfg.Body to cfg.URL at cfg.RPS for cfg.Duration and returns the aggregated report.
//
// Arrivals are open-loop: a request is due on every schedule tick regardless of how long earlier ones
// take, so a slow server shows up as latency and dropped ticks instead of silently lowering the rate
// (a closed loop that waits for each response would hide exactly the latency under test).
// A tick that finds all Concurrency workers busy is dropped and counted in Report.Dropped.
func Run(ctx context.Context, cfg Config, client *http.Client) (Report, error) {
	if err := cfg.Validate(); err != nil {
		return Report{}, err
	}

	ticks := make(chan struct{})
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		samples []sample
	)
	for range cfg.Concurrency {
		wg.Go(func() {
			var own []sample // no shared state on the request path; merged once when the worker ends
			for range ticks {
				own = append(own, send(ctx, cfg, client))
			}
			mu.Lock()
			samples = append(samples, own...)
			mu.Unlock()
		})
	}

	start := time.Now()
	dropped := schedule(ctx, cfg, ticks)
	wg.Wait()

	return aggregate(samples, dropped, time.Since(start), cfg.RPS), nil
}

// schedule offers one tick per interval until the scheduling window or ctx ends, then closes ticks.
// It returns the number of ticks no worker was free to take.
func schedule(ctx context.Context, cfg Config, ticks chan<- struct{}) int {
	defer close(ticks)
	window, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()
	ticker := time.NewTicker(cfg.interval())
	defer ticker.Stop()

	dropped := 0
	for {
		select {
		case <-window.Done():
			return dropped
		case <-ticker.C:
			select {
			case ticks <- struct{}{}:
			default:
				dropped++
			}
		}
	}
}

// send performs one request. It derives from ctx, not from the scheduling window, so a request in
// flight when the window closes still completes and is counted.
func send(ctx context.Context, cfg Config, client *http.Client) sample {
	reqCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.URL, bytes.NewReader(cfg.Body))
	if err != nil {
		return sample{err: err}
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return sample{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readLimited(resp.Body, cfg.MaxResponseBytes)
	if err != nil {
		return sample{err: err}
	}

	s := sample{latency: time.Since(start), status: resp.StatusCode, bytes: len(body)}
	// Only a 200 carries a BidResponse; 204 and error statuses have nothing to parse.
	if resp.StatusCode == http.StatusOK {
		s.bids = hasBids(body)
	}
	return s
}

// readLimited reads r to the end, failing when it holds more than limit bytes.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w (%d bytes)", ErrResponseTooLarge, limit)
	}
	return body, nil
}

func hasBids(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var resp struct {
		SeatBid []json.RawMessage `json:"seatbid"`
	}
	return json.Unmarshal(body, &resp) == nil && len(resp.SeatBid) > 0
}
