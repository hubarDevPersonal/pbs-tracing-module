package load

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validConfig(url string) Config {
	return Config{
		URL:              url,
		Bodies:           [][]byte{[]byte(`{}`)},
		RPS:              50,
		Duration:         600 * time.Millisecond,
		Concurrency:      8,
		Timeout:          time.Second,
		MaxResponseBytes: 1 << 20,
	}
}

func TestConfigValidate(t *testing.T) {
	require.NoError(t, validConfig("http://x").Validate())

	cases := map[string]func(*Config){
		"empty url":          func(c *Config) { c.URL = "" },
		"no bodies":          func(c *Config) { c.Bodies = nil },
		"zero rps":           func(c *Config) { c.RPS = 0 },
		"nan rps":            func(c *Config) { c.RPS = math.NaN() },
		"infinite rps":       func(c *Config) { c.RPS = math.Inf(1) },
		"rps above max":      func(c *Config) { c.RPS = MaxRPS + 1 },
		"zero duration":      func(c *Config) { c.Duration = 0 },
		"zero concurrency":   func(c *Config) { c.Concurrency = 0 },
		"zero timeout":       func(c *Config) { c.Timeout = 0 },
		"zero response size": func(c *Config) { c.MaxResponseBytes = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig("http://x")
			mutate(&cfg)
			assert.Error(t, cfg.Validate())
		})
	}
}

func TestRun_RejectsInvalidConfig(t *testing.T) {
	_, err := Run(context.Background(), Config{}, http.DefaultClient)
	assert.Error(t, err)
}

func TestRun_CountsStatusesLatencyAndBids(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := n.Add(1)
		time.Sleep(5 * time.Millisecond)
		switch {
		case i%5 == 0:
			w.WriteHeader(http.StatusInternalServerError)
		case i%3 == 0:
			w.WriteHeader(http.StatusNoContent)
		case i%2 == 0:
			_, _ = w.Write([]byte(`{"id":"x","seatbid":[{"bid":[{"id":"b"}]}]}`))
		default:
			_, _ = w.Write([]byte(`{"id":"x"}`))
		}
	}))
	defer srv.Close()

	rep, err := Run(context.Background(), validConfig(srv.URL), srv.Client())
	require.NoError(t, err)

	assert.GreaterOrEqual(t, rep.Requests, 15, "~30 requests expected at 50 rps over 0.6 s")
	assert.Equal(t, rep.Requests, rep.OK+rep.Non2xx+rep.Errors)
	assert.Zero(t, rep.Errors)
	assert.Zero(t, rep.Dropped, "8 workers at 5 ms per request never run out")
	assert.Positive(t, rep.Statuses[http.StatusNoContent])
	assert.Positive(t, rep.Non2xx)
	assert.Positive(t, rep.WithBids)
	assert.Less(t, rep.WithBids, rep.OK)
	assert.GreaterOrEqual(t, rep.Latency.P50, 5*time.Millisecond)
	assert.LessOrEqual(t, rep.Latency.Min, rep.Latency.P50)
	assert.LessOrEqual(t, rep.Latency.P50, rep.Latency.Max)
	assert.Positive(t, rep.AchievedRPS)
	assert.Contains(t, rep.String(), "statuses:")
}

func TestRun_BusyWorkersDropTicks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := validConfig(srv.URL)
	cfg.RPS, cfg.Concurrency, cfg.Duration = 100, 1, 300*time.Millisecond
	rep, err := Run(context.Background(), cfg, srv.Client())
	require.NoError(t, err)
	assert.Positive(t, rep.Dropped, "one worker cannot absorb 100 rps at 200 ms per request")
	assert.Less(t, rep.AchievedRPS, cfg.RPS)
}

func TestRun_TransportErrorsAreCounted(t *testing.T) {
	cfg := validConfig("http://127.0.0.1:1")
	cfg.RPS, cfg.Duration, cfg.Concurrency, cfg.Timeout = 20, 200*time.Millisecond, 2, 200*time.Millisecond
	rep, err := Run(context.Background(), cfg, http.DefaultClient)
	require.NoError(t, err)
	assert.Positive(t, rep.Requests)
	assert.Equal(t, rep.Requests, rep.Errors)
}

func TestRun_ResponseAboveLimitIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 2048))
	}))
	defer srv.Close()

	cfg := validConfig(srv.URL)
	cfg.RPS, cfg.Duration, cfg.MaxResponseBytes = 20, 200*time.Millisecond, 1024
	rep, err := Run(context.Background(), cfg, srv.Client())
	require.NoError(t, err)
	assert.Positive(t, rep.Requests)
	assert.Equal(t, rep.Requests, rep.Errors, "every oversized response is an error")
}

func TestRun_CancellationEndsTheRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	cfg := validConfig(srv.URL)
	cfg.Duration = 10 * time.Second
	start := time.Now()
	rep, err := Run(ctx, cfg, srv.Client())
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 2*time.Second, "the run ends with ctx, not with Duration")
	assert.Positive(t, rep.Requests)
}
