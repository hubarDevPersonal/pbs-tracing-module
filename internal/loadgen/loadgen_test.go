package loadgen

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestRun_ResponseAboveLimitIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 2048))
	}))
	defer srv.Close()

	rep, err := Run(
		context.Background(),
		Config{URL: srv.URL, Body: []byte(`{}`), RPS: 20, Duration: 200 * time.Millisecond, Concurrency: 2, Timeout: time.Second, MaxResponseBytes: 1024},
		srv.Client(),
	)
	require.NoError(t, err)
	assert.Positive(t, rep.Requests)
	assert.Equal(t, rep.Requests, rep.Errors, "every oversized response must be counted as an error")
}

func TestRun_CancellationStopsSchedulingAndReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	rep, err := Run(ctx, Config{URL: srv.URL, Body: []byte(`{}`), RPS: 50, Duration: 10 * time.Second, Concurrency: 4, Timeout: time.Second}, srv.Client())
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 2*time.Second, "run must end with the context, not with Duration")
	assert.Positive(t, rep.Requests)
}

func TestReadBodyFile(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.json")
	require.NoError(t, os.WriteFile(small, []byte(`{"id":"x"}`), 0o600))
	big := filepath.Join(dir, "big.json")
	require.NoError(t, os.WriteFile(big, make([]byte, 300), 0o600))
	empty := filepath.Join(dir, "empty.json")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))

	body, err := ReadBodyFile(small, 256)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"x"}`, string(body))

	_, err = ReadBodyFile(big, 256)
	assert.ErrorIs(t, err, ErrResponseTooLarge)
	_, err = ReadBodyFile(empty, 256)
	assert.ErrorContains(t, err, "empty body")
	_, err = ReadBodyFile(filepath.Join(dir, "missing.json"), 256)
	assert.Error(t, err)
}

func TestRun_RejectsInvalidConfig(t *testing.T) {
	_, err := Run(context.Background(), Config{}, nil)
	assert.Error(t, err)
	for _, rps := range []float64{math.Inf(1), math.NaN(), 1e10} {
		_, err := Run(context.Background(), Config{URL: "http://127.0.0.1:9", Body: []byte(`{}`), RPS: rps, Duration: time.Millisecond}, nil)
		assert.Error(t, err, "rps %v must be rejected before the ticker is created", rps)
	}
}
