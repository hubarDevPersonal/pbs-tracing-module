package loadgen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPercentile(t *testing.T) {
	sorted := []time.Duration{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	assert.Equal(t, time.Duration(5), Percentile(sorted, 50))
	assert.Equal(t, time.Duration(9), Percentile(sorted, 90))
	assert.Equal(t, time.Duration(10), Percentile(sorted, 99))
	assert.Equal(t, time.Duration(1), Percentile(sorted, 0))
	assert.Equal(t, time.Duration(10), Percentile(sorted, 100))
	assert.Equal(t, time.Duration(0), Percentile(nil, 50))
}

func TestRun_CountsStatusesLatencyAndBids(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := n.Add(1)
		time.Sleep(5 * time.Millisecond)
		switch {
		case i%5 == 0:
			w.WriteHeader(http.StatusInternalServerError)
		case i%2 == 0:
			_, _ = w.Write([]byte(`{"id":"x","seatbid":[{"bid":[{"id":"b"}]}]}`))
		default:
			_, _ = w.Write([]byte(`{"id":"x"}`))
		}
	}))
	defer srv.Close()

	rep, err := Run(context.Background(), Config{URL: srv.URL, Body: []byte(`{}`), RPS: 50, Duration: 600 * time.Millisecond, Concurrency: 8, Timeout: time.Second}, srv.Client())
	require.NoError(t, err)

	assert.GreaterOrEqual(t, rep.Requests, 15, "at least ~30 requests expected at 50 rps over 0.6 s")
	assert.Equal(t, rep.Requests, rep.OK+rep.Non2xx+rep.Errors)
	assert.Zero(t, rep.Errors)
	assert.Positive(t, rep.Non2xx)
	assert.Positive(t, rep.WithBids)
	assert.Less(t, rep.WithBids, rep.OK)
	assert.GreaterOrEqual(t, rep.Latency.P50, 5*time.Millisecond)
	assert.LessOrEqual(t, rep.Latency.Min, rep.Latency.P50)
	assert.LessOrEqual(t, rep.Latency.P50, rep.Latency.Max)
	assert.Positive(t, rep.AchievedRPS)
	assert.Contains(t, rep.String(), "statuses:")
}

func TestRun_TransportErrorsAreCounted(t *testing.T) {
	rep, err := Run(context.Background(), Config{URL: "http://127.0.0.1:1", Body: []byte(`{}`), RPS: 20, Duration: 200 * time.Millisecond, Concurrency: 2, Timeout: 200 * time.Millisecond}, nil)
	require.NoError(t, err)
	assert.Positive(t, rep.Requests)
	assert.Equal(t, rep.Requests, rep.Errors)
}

func TestRun_RejectsInvalidConfig(t *testing.T) {
	_, err := Run(context.Background(), Config{}, nil)
	assert.Error(t, err)
}
