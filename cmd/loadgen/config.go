package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/hubarDevPersonal/pbs-tracing-module/internal/loadgen"
)

// Defaults are deliberately conservative: the harness drives a Prebid Server that calls live third-party
// bidders, so the rate is chosen to be polite to them, not to find the capacity limit of PBS.
const (
	defaultURL  = "http://localhost:8080/openrtb2/auction"
	defaultBody = "01-bid-request-example.json"

	// defaultRPS × bidders per request (4 in the sample) ≈ 20 outbound requests/s to third parties.
	// Enough to observe connection reuse and DNS-cache behavior; raise it only against stored
	// responses or a mock bidder (docs/06-performance.md §6).
	defaultRPS = 5.0

	// defaultDuration yields 150 samples at defaultRPS: p50/p90 are stable, p99 is indicative only
	// (it is the second-worst sample). The pprof capture in scripts/perf-docker.sh uses the same length.
	defaultDuration = 30 * time.Second

	// defaultConcurrency ≥ RPS × worst-case latency (5 × ~1 s observed p99 with live bidders), so the
	// target rate is sustained without dropping ticks. Workers park on network I/O; they are goroutines,
	// not threads, and are unrelated to GOMAXPROCS.
	defaultConcurrency = 16
	maxConcurrency     = 4096

	// defaultTimeout must exceed the PBS auction timeout the server enforces (auction_timeouts_ms.max,
	// 3 s in deploy/pbs.perf.yaml) plus response encoding; otherwise the client, not PBS, decides latency.
	defaultTimeout = 10 * time.Second

	// maxBodyBytes matches PBS max_request_size (256 KiB default); a larger body is rejected by PBS anyway.
	maxBodyBytes = 256 << 10
	// maxResponseBytes bounds what a worker buffers per response (debug responses are ~15 KB; 4 MiB is generous).
	maxResponseBytes = 4 << 20

	// shutdownGrace is added to Duration + Timeout for the hard deadline of the whole run.
	shutdownGrace = 5 * time.Second

	envPrefix = "LOADGEN_"
)

// config is the resolved run configuration. Precedence: flags > environment (LOADGEN_*) > defaults.
type config struct {
	URL         string
	BodyPath    string
	RPS         float64
	Duration    time.Duration
	Concurrency int
	Timeout     time.Duration
	Out         string // JSON report path, empty = none
}

// parseConfig builds the config from environment defaults and command-line flags.
// lookupEnv is injectable for tests (os.LookupEnv in production).
func parseConfig(args []string, lookupEnv func(string) (string, bool), usageOut io.Writer) (config, error) {
	cfg := config{
		URL:         envString(lookupEnv, "URL", defaultURL),
		BodyPath:    envString(lookupEnv, "BODY", defaultBody),
		RPS:         defaultRPS,
		Duration:    defaultDuration,
		Concurrency: defaultConcurrency,
		Timeout:     defaultTimeout,
		Out:         envString(lookupEnv, "OUT", ""),
	}
	var err error
	if cfg.RPS, err = envFloat(lookupEnv, "RPS", cfg.RPS); err != nil {
		return config{}, err
	}
	if cfg.Duration, err = envDuration(lookupEnv, "DURATION", cfg.Duration); err != nil {
		return config{}, err
	}
	if cfg.Concurrency, err = envInt(lookupEnv, "CONCURRENCY", cfg.Concurrency); err != nil {
		return config{}, err
	}
	if cfg.Timeout, err = envDuration(lookupEnv, "TIMEOUT", cfg.Timeout); err != nil {
		return config{}, err
	}

	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	fs.SetOutput(usageOut)
	fs.StringVar(&cfg.URL, "url", cfg.URL, "auction endpoint")
	fs.StringVar(&cfg.BodyPath, "body", cfg.BodyPath, "request body file (≤ 256 KiB, PBS max_request_size)")
	fs.Float64Var(&cfg.RPS, "rps", cfg.RPS, "target requests per second (keep modest against live bidders)")
	fs.DurationVar(&cfg.Duration, "duration", cfg.Duration, "run duration")
	fs.IntVar(&cfg.Concurrency, "concurrency", cfg.Concurrency, "max in-flight requests")
	fs.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "per-request timeout")
	fs.StringVar(&cfg.Out, "out", cfg.Out, "write the JSON report to this file")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	return cfg, cfg.validate()
}

func (c config) validate() error {
	switch {
	case c.URL == "":
		return errors.New("url must not be empty")
	case c.BodyPath == "":
		return errors.New("body must not be empty")
	case c.RPS <= 0 || math.IsInf(c.RPS, 0) || math.IsNaN(c.RPS) || c.RPS > loadgen.MaxRPS:
		return fmt.Errorf("rps must be in (0, %v], got %v", loadgen.MaxRPS, c.RPS)
	case c.Duration <= 0:
		return fmt.Errorf("duration must be positive, got %s", c.Duration)
	case c.Concurrency <= 0 || c.Concurrency > maxConcurrency:
		return fmt.Errorf("concurrency must be in 1..%d, got %d", maxConcurrency, c.Concurrency)
	case c.Timeout <= 0:
		return fmt.Errorf("timeout must be positive, got %s", c.Timeout)
	}
	return nil
}

// hardDeadline bounds the whole run: scheduling window + the slowest in-flight request + grace.
func (c config) hardDeadline() time.Duration { return c.Duration + c.Timeout + shutdownGrace }

func envString(lookup func(string) (string, bool), key, def string) string {
	if v, ok := lookup(envPrefix + key); ok && v != "" {
		return v
	}
	return def
}

func envFloat(lookup func(string) (string, bool), key string, def float64) (float64, error) {
	v, ok := lookup(envPrefix + key)
	if !ok || v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s%s: %w", envPrefix, key, err)
	}
	return f, nil
}

func envInt(lookup func(string) (string, bool), key string, def int) (int, error) {
	v, ok := lookup(envPrefix + key)
	if !ok || v == "" {
		return def, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s%s: %w", envPrefix, key, err)
	}
	return i, nil
}

func envDuration(lookup func(string) (string, bool), key string, def time.Duration) (time.Duration, error) {
	v, ok := lookup(envPrefix + key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s%s: %w", envPrefix, key, err)
	}
	return d, nil
}
