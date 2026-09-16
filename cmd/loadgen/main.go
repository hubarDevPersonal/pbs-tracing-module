// Command loadgen sends the assessment's sample BidRequest to a Prebid Server at a fixed rate and
// prints latency percentiles. Used by scripts/perf-docker.sh; keep the rate modest against live bidders.
//
//	loadgen -url http://localhost:8080/openrtb2/auction -body 01-bid-request-example.json -rps 5 -duration 30s
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/hubarDevPersonal/pbs-tracing-module/internal/loadgen"
)

func main() {
	os.Exit(run())
}

func run() int {
	url := flag.String("url", "http://localhost:8080/openrtb2/auction", "auction endpoint")
	bodyPath := flag.String("body", "01-bid-request-example.json", "request body file")
	rps := flag.Float64("rps", 5, "target requests per second")
	duration := flag.Duration("duration", 30*time.Second, "run duration")
	concurrency := flag.Int("concurrency", 16, "max in-flight requests")
	timeout := flag.Duration("timeout", 10*time.Second, "per-request timeout")
	out := flag.String("out", "", "write the JSON report to this file")
	flag.Parse()

	body, err := os.ReadFile(*bodyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *concurrency, MaxConnsPerHost: *concurrency}}
	report, err := loadgen.Run(ctx, loadgen.Config{
		URL: *url, Body: body, RPS: *rps, Duration: *duration, Concurrency: *concurrency, Timeout: *timeout,
	}, client)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		return 2
	}
	fmt.Print(report.String())
	if *out != "" {
		raw, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(*out, raw, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "loadgen:", err)
			return 2
		}
	}
	if report.Errors > 0 {
		return 1
	}
	return 0
}
