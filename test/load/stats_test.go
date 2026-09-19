package load

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPercentile(t *testing.T) {
	sorted := []time.Duration{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	assert.Equal(t, time.Duration(1), percentile(sorted, 1))
	assert.Equal(t, time.Duration(5), percentile(sorted, 50))
	assert.Equal(t, time.Duration(9), percentile(sorted, 90))
	assert.Equal(t, time.Duration(10), percentile(sorted, 99))
	assert.Equal(t, time.Duration(10), percentile(sorted, 100))
	assert.Equal(t, time.Duration(0), percentile(nil, 50))
}

func TestSummarize(t *testing.T) {
	assert.Equal(t, Latency{}, summarize(nil))

	s := summarize([]time.Duration{30, 10, 20})
	assert.Equal(t, time.Duration(10), s.Min)
	assert.Equal(t, time.Duration(20), s.Mean)
	assert.Equal(t, time.Duration(20), s.P50)
	assert.Equal(t, time.Duration(30), s.Max)
}
