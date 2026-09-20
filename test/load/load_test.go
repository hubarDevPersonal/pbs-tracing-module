//go:build load

package load

import (
	"context"
	"flag"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The defaults are polite to the live third-party bidders PBS calls: 5 auctions/s × 4 bidders ≈ 20
// outbound requests/s. Raise the rate only against a PBS whose bidders are stubbed.
var (
	pbsURL      = flag.String("pbs-url", "http://localhost:8080", "base URL of a running Prebid Server with the module")
	rps         = flag.Float64("rps", 5, "target auctions per second")
	duration    = flag.Duration("duration", 30*time.Second, "length of each scenario")
	concurrency = flag.Int("concurrency", 16, "max in-flight auctions; ≥ rps × worst-case latency so no tick is dropped")
)

const (
	// timeout exceeds the PBS auction timeout (auction_timeouts_ms.max, 3 s in deploy/pbs.perf.yaml) so PBS,
	// not the client, decides the latency.
	timeout = 10 * time.Second
	// debug responses of the sample auction are ~15 KB.
	maxResponseBytes = 4 << 20
)

// L-04 (docs/test-specs/load.md). TestLoad_Auction holds a constant auction rate against a running PBS and requires every auction to be
// answered: no transport errors, no non-2xx statuses, no dropped arrivals. Latency is reported, not
// asserted, because live bidders dominate it.
//
//	go test -tags load -count=1 -v ./test/load -pbs-url http://localhost:8080 -rps 5 -duration 30s
func TestLoad_Auction(t *testing.T) {
	sample, err := os.ReadFile("../../docs/assessment/01-bid-request-example.json")
	require.NoError(t, err)
	liveBid, err := os.ReadFile("../../testdata/bid-request-live-bid.json")
	require.NoError(t, err)
	scenarios := []struct {
		name string
		body []byte
	}{
		// the assessment request: traced partner, four bidders that answer 204
		{"sample request", sample},
		// adds onetag's test publisher: a real bid, so raw_bidder_response and bid processing run
		{"live bid request", liveBid},
	}

	client := &http.Client{Transport: &http.Transport{
		MaxIdleConnsPerHost: *concurrency, // one warm connection per worker
		MaxConnsPerHost:     *concurrency, // never more connections than workers
	}}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), *duration+timeout+5*time.Second)
			defer cancel()
			report, err := Run(ctx, Config{
				URL:              *pbsURL + "/openrtb2/auction",
				Bodies:           [][]byte{sc.body},
				RPS:              *rps,
				Duration:         *duration,
				Concurrency:      *concurrency,
				Timeout:          timeout,
				MaxResponseBytes: maxResponseBytes,
			}, client)
			require.NoError(t, err)
			t.Log("\n" + report.String())

			assert.Positive(t, report.Requests)
			assert.Zero(t, report.Errors, "transport errors or timeouts")
			assert.Zero(t, report.Non2xx, "non-2xx statuses: %v", report.Statuses)
			assert.Zero(t, report.Dropped, "arrivals dropped: raise -concurrency")
		})
	}
}
