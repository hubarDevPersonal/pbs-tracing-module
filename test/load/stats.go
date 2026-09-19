package load

import (
	"fmt"
	"slices"
	"time"
)

// Latency summarizes a latency distribution.
type Latency struct {
	Min, Mean, P50, P90, P95, P99, Max time.Duration
}

func (l Latency) String() string {
	return fmt.Sprintf("min=%s mean=%s p50=%s p90=%s p95=%s p99=%s max=%s",
		ms(l.Min), ms(l.Mean), ms(l.P50), ms(l.P90), ms(l.P95), ms(l.P99), ms(l.Max))
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond)) }

// summarize sorts latencies in place and returns their summary.
func summarize(latencies []time.Duration) Latency {
	if len(latencies) == 0 {
		return Latency{}
	}
	slices.Sort(latencies)
	var sum time.Duration
	for _, l := range latencies {
		sum += l
	}
	return Latency{
		Min:  latencies[0],
		Mean: sum / time.Duration(len(latencies)),
		P50:  percentile(latencies, 50),
		P90:  percentile(latencies, 90),
		P95:  percentile(latencies, 95),
		P99:  percentile(latencies, 99),
		Max:  latencies[len(latencies)-1],
	}
}

// percentile returns the nearest-rank percentile p (0 < p ≤ 100) of an ascending slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p / 100 * float64(len(sorted)))
	if float64(rank) < p/100*float64(len(sorted)) {
		rank++ // ceil
	}
	return sorted[min(max(rank-1, 0), len(sorted)-1)]
}
