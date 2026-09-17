// Command loadgen sends the assessment's sample BidRequest to a Prebid Server at a fixed rate and
// prints latency percentiles. Used by scripts/perf-docker.sh; keep the rate modest against live bidders.
//
//	loadgen [-url …] [-body file] [-rps 5] [-duration 30s] [-concurrency 16] [-timeout 10s] [-out report.json]
//
// Every flag can also be set through the environment as LOADGEN_<FLAG> (flags win). See config.go for
// the defaults and the reasoning behind them.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/hubarDevPersonal/pbs-tracing-module/internal/loadgen"
)

func main() {
	os.Exit(run(os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr))
}

func run(args []string, lookupEnv func(string) (string, bool), stdout, stderr io.Writer) int {
	cfg, err := parseConfig(args, lookupEnv, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "loadgen:", err)
		return 2
	}

	body, err := loadgen.ReadBodyFile(cfg.BodyPath, maxBodyBytes)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "loadgen:", err)
		return 2
	}

	// Root context of the run: SIGINT (operator) and SIGTERM (docker stop, CI cancel) end the run
	// gracefully so the partial report is still printed; the hard deadline guards against a wedged
	// server holding connections past Duration + Timeout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, cfg.hardDeadline())
	defer cancel()

	client := &http.Client{Transport: &http.Transport{
		MaxIdleConnsPerHost: cfg.Concurrency, // one warm connection per worker
		MaxConnsPerHost:     cfg.Concurrency, // never exceed the worker count towards PBS
	}}
	report, err := loadgen.Run(ctx, loadgen.Config{
		URL:              cfg.URL,
		Body:             body,
		RPS:              cfg.RPS,
		Duration:         cfg.Duration,
		Concurrency:      cfg.Concurrency,
		Timeout:          cfg.Timeout,
		MaxResponseBytes: maxResponseBytes,
	}, client)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "loadgen:", err)
		return 2
	}

	_, _ = fmt.Fprint(stdout, report.String())
	if cfg.Out != "" {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "loadgen:", err)
			return 2
		}
		if err := os.WriteFile(cfg.Out, raw, 0o600); err != nil {
			_, _ = fmt.Fprintln(stderr, "loadgen:", err)
			return 2
		}
	}
	if report.Errors > 0 {
		return 1
	}
	return 0
}
