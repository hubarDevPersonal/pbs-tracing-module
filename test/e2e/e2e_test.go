//go:build e2e

package e2e

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Expectations come from the hardcoded rules and the request fixtures, restated here on purpose: the
// suite checks the running server against the specification, not against the module's source.
const (
	sampleAuctionID = "5d394bed0104ca857c702982fe8d95e408820eb2-3"
	samplePartner   = "664-025-677-881" // rule: 3 packets in 10 minutes
	samplePackets   = 3

	liveBidAuctionID = "live-bid-1"
	liveBidPartner   = "33415-10498" // rule: 1 packet in 30 seconds
)

var sampleBidders = []string{"aceex", "adyoulike", "amx", "appnexus"}

// E-01 … E-06 (docs/test-specs/e2e.md). TestE2E_TraceOnStdout runs the image with the assessment's pbs.yaml
// against live bidders.
//
// Phase A sends the assessment request more often than its rule allows plus one request of an unknown
// account: exactly samplePackets packets appear; items 1, 2 and 4 are strict, item 3 is checked for shape
// only because the sample's bidders answer 204. Phase B sends a request that adds onetag's test
// publisher, which returns a real bid, under the one-packet rule: item 3 is strict and the second request
// is not traced.
//
//	make e2e    # builds the image, then: go test -tags e2e -count=1 -v ./test/e2e
func TestE2E_TraceOnStdout(t *testing.T) {
	sample, err := os.ReadFile("../../01-bid-request-example.json")
	require.NoError(t, err)
	liveBid, err := os.ReadFile("../../testdata/bid-request-live-bid.json")
	require.NoError(t, err)
	unknownAccount := bytes.ReplaceAll(sample, []byte(samplePartner), []byte("not-a-partner"))

	server := startPBS(t)

	t.Run("phase A: assessment request", func(t *testing.T) { // E-01, E-02, E-03, E-04, E-06
		for range samplePackets + 2 {
			require.NoError(t, checkHookOutcomes(server.auction(t, sample)))
		}
		require.NoError(t, checkHookOutcomes(server.auction(t, unknownAccount)))

		trace, log := server.settledLogs(t, samplePackets)
		require.NoError(t, checkPBSLog(log))
		responses, err := verifyTrace(trace, expectation{
			Packets:   samplePackets,
			PartnerID: samplePartner,
			AuctionID: sampleAuctionID,
			Bidders:   sampleBidders,
		})
		require.NoError(t, err)
		t.Logf("bidder responses recorded: %d (the sample's live bidders usually answer 204)", responses)
	})

	t.Run("phase B: live bid", func(t *testing.T) { // E-01, E-05, E-06
		before, _ := logs(t)
		for range 2 {
			require.NoError(t, checkHookOutcomes(server.auction(t, liveBid)))
		}

		trace, log := server.settledLogs(t, bytes.Count(before, []byte("\n"))+1)
		require.NoError(t, checkPBSLog(log))
		responses, err := verifyTrace(trace[len(before):], expectation{
			Packets:    1,
			PartnerID:  liveBidPartner,
			AuctionID:  liveBidAuctionID,
			Bidders:    append([]string{"onetag"}, sampleBidders...),
			StrictBids: true,
		})
		require.NoError(t, err)
		assert.Positive(t, responses)
	})
}

// settledLogs waits until stdout holds at least the given number of lines and then one more poll
// interval, so a packet written after the last response is not missed.
func (p *pbs) settledLogs(t *testing.T, lines int) (stdout, stderr []byte) {
	t.Helper()
	require.Eventually(t, func() bool {
		out, _ := logs(t)
		return bytes.Count(out, []byte("\n")) >= lines
	}, 10*time.Second, 200*time.Millisecond, "fewer than %d trace lines on stdout", lines)
	time.Sleep(500 * time.Millisecond)
	return logs(t)
}
