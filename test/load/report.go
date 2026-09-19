package load

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// Report is the outcome of one run.
type Report struct {
	Requests    int         // requests sent
	OK          int         // 2xx responses
	Non2xx      int         // other statuses
	Errors      int         // transport errors, timeouts, oversized responses
	Dropped     int         // schedule ticks with no free worker
	WithBids    int         // 200 responses with a non-empty seatbid
	Statuses    map[int]int // responses per HTTP status
	BytesIn     int64
	Elapsed     time.Duration
	TargetRPS   float64
	AchievedRPS float64
	Latency     Latency // over responses with a status; errors excluded
}

func aggregate(samples []sample, dropped int, elapsed time.Duration, targetRPS float64) Report {
	r := Report{Requests: len(samples), Dropped: dropped, Statuses: map[int]int{}, Elapsed: elapsed, TargetRPS: targetRPS}
	latencies := make([]time.Duration, 0, len(samples))
	for _, s := range samples {
		if s.err != nil {
			r.Errors++
			continue
		}
		latencies = append(latencies, s.latency)
		r.BytesIn += int64(s.bytes)
		r.Statuses[s.status]++
		if s.status >= 200 && s.status < 300 {
			r.OK++
		} else {
			r.Non2xx++
		}
		if s.bids {
			r.WithBids++
		}
	}
	if elapsed > 0 {
		r.AchievedRPS = float64(r.Requests) / elapsed.Seconds()
	}
	r.Latency = summarize(latencies)
	return r
}

// String renders the report for test logs.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "requests=%d ok=%d non2xx=%d errors=%d dropped=%d with_bids=%d\n",
		r.Requests, r.OK, r.Non2xx, r.Errors, r.Dropped, r.WithBids)
	fmt.Fprintf(&b, "elapsed=%s target_rps=%.1f achieved_rps=%.1f bytes_in=%d\n",
		r.Elapsed.Round(time.Millisecond), r.TargetRPS, r.AchievedRPS, r.BytesIn)
	fmt.Fprintf(&b, "latency %s\n", r.Latency)
	if len(r.Statuses) > 0 {
		b.WriteString("statuses:")
		for _, code := range slices.Sorted(maps.Keys(r.Statuses)) {
			fmt.Fprintf(&b, " %d=%d", code, r.Statuses[code])
		}
		b.WriteByte('\n')
	}
	return b.String()
}
