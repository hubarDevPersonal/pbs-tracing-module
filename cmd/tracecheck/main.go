// Command tracecheck verifies the NDJSON trace written by the test_provider.test_tracer module.
//
//	tracecheck [flags] trace.ndjson
//
// It is the assertion step of scripts/e2e-*.sh and exits non-zero on the first violated expectation.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hubarDevPersonal/pbs-tracing-module/internal/tracecheck"
)

const (
	sampleAuctionID = "5d394bed0104ca857c702982fe8d95e408820eb2-3"
	samplePartner   = "664-025-677-881"
	sampleBidders   = "aceex,appnexus,amx,adyoulike"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "tracecheck: FAIL:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("tracecheck", flag.ContinueOnError)
	expect := fs.Int("expect", -1, "exact number of trace packets expected (-1 disables)")
	partner := fs.String("partner", samplePartner, "expected partner_id (empty disables)")
	auctionID := fs.String("auction-id", sampleAuctionID, "expected BidRequest.id (empty disables)")
	bidders := fs.String("bidders", sampleBidders, "comma-separated exact set of bidders expected in bidder_requests (empty disables)")
	strictBids := fs.Bool("strict-bids", false, "fail when no bidder response was recorded (live bidders may answer 204)")
	requireDebug := fs.Bool("require-debug", true, "final_response.body.ext.debug must be present")
	responses := fs.String("responses", "", "glob of HTTP response JSON files whose ext.prebid.modules must show successful hooks")
	pbsLog := fs.String("pbs-log", "", "PBS stderr log; fails on 'Not found hook' warnings for the module")
	if err := fs.Parse(args); err != nil {
		return err
	}

	in := stdin
	if fs.NArg() > 0 && fs.Arg(0) != "-" {
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		in = f
	}

	opts := tracecheck.Options{
		ExpectPackets: *expect,
		PartnerID:     *partner,
		AuctionID:     *auctionID,
		StrictBids:    *strictBids,
		RequireDebug:  *requireDebug,
	}
	if *bidders != "" {
		opts.Bidders = strings.Split(*bidders, ",")
	}
	report, err := tracecheck.Verify(in, opts)
	if err != nil {
		return err
	}

	if *responses != "" {
		paths, err := filepath.Glob(*responses)
		if err != nil {
			return err
		}
		if err := tracecheck.CheckResponses(paths); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "hook outcomes OK in %d HTTP responses\n", len(paths))
	}
	if *pbsLog != "" {
		f, err := os.Open(*pbsLog)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		if err := tracecheck.CheckLog(f); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintln(stdout, report.String())
	return nil
}
