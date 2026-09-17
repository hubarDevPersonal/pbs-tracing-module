package testtracer

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/openrtb_ext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An invalid json.RawMessage makes encoding/json fail, which is the only way the module's snapshot
// marshaling can error with real PBS types. FR-15 AC2: such failures are logged, never returned.
var brokenExt = json.RawMessage(`{"not":"closed"`)

func TestHooks_MarshalFailuresAreLoggedNotReturned(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	body := loadSampleRequest(t)
	mc := tracedContext(t, m, sampleRequestAccountID, body)
	ctx := t.Context()
	ic := auctionCtx(sampleRequestAccountID, mc)

	brokenReq := &openrtb_ext.RequestWrapper{BidRequest: &openrtb2.BidRequest{ID: "x", Ext: brokenExt}}
	res, err := m.HandleBidderRequestHook(ctx, ic, hookstage.BidderRequestPayload{Request: brokenReq, Bidder: "appnexus"})
	require.NoError(t, err)
	assert.False(t, res.Reject)

	brokenResp := &adapters.BidderResponse{Currency: "USD", Bids: []*adapters.TypedBid{{Bid: &openrtb2.Bid{ID: "b", Ext: brokenExt}, BidType: openrtb_ext.BidTypeBanner}}}
	_, err = m.HandleRawBidderResponseHook(ctx, ic, hookstage.RawBidderResponsePayload{BidderResponse: brokenResp, Bidder: "appnexus"})
	require.NoError(t, err)

	brokenFinal := &openrtb2.BidResponse{ID: "f", Ext: brokenExt}
	_, err = m.HandleAuctionResponseHook(ctx, ic, hookstage.AuctionResponsePayload{BidResponse: brokenFinal})
	require.NoError(t, err)
	_, err = m.HandleExitpointHook(ctx, ic, hookstage.ExitpointPayload{Response: brokenFinal, W: httptest.NewRecorder()})
	require.NoError(t, err)

	packets := out.Packets(t)
	require.Len(t, packets, 1, "the packet is still emitted with whatever was captured")
	assert.Empty(t, packets[0].BidderRequests)
	assert.Empty(t, packets[0].BidderResponses)
	assert.Nil(t, packets[0].FinalResponse)
}

// FR-04 AC3 error branch: the processed-request fallback cannot marshal → no incoming_request, trace still starts.
func TestProcessedAuction_FallbackMarshalFailureIsLogged(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	broken := &openrtb_ext.RequestWrapper{BidRequest: &openrtb2.BidRequest{ID: "broken", Ext: brokenExt}}

	res, err := m.HandleProcessedAuctionHook(t.Context(), auctionCtx(sampleRequestAccountID, nil), hookstage.ProcessedAuctionRequestPayload{Request: broken})
	require.NoError(t, err)
	trace := traceIn(res.ModuleContext)
	require.NotNil(t, trace)
	assert.Nil(t, trace.Packet(testStart).IncomingRequest)

	final := sampleBidResponse("r")
	_, err = m.HandleExitpointHook(t.Context(), auctionCtx(sampleRequestAccountID, res.ModuleContext), hookstage.ExitpointPayload{Response: final, W: httptest.NewRecorder()})
	require.NoError(t, err)
	require.Len(t, out.Packets(t), 1)
}

// AuctionTrace-level: the same failures surface as errors to callers.
func TestAuctionTrace_MarshalErrorsAreReturned(t *testing.T) {
	tr := newTestTracer(t, testRules(), newFakeClock(testStart))
	trace, ok := tr.Begin(sampleRequestAccountID, "a")
	require.True(t, ok)

	assert.Error(t, trace.AddBidderRequest(testStart, "b", &openrtb2.BidRequest{ID: "x", Ext: brokenExt}))
	assert.Error(t, trace.AddBidderResponse(testStart, "b", &adapters.BidderResponse{Bids: []*adapters.TypedBid{{Bid: &openrtb2.Bid{Ext: brokenExt}}}}))
	assert.Error(
		t,
		trace.AddBidderResponse(testStart, "b", &adapters.BidderResponse{Bids: []*adapters.TypedBid{{Bid: &openrtb2.Bid{ID: "b"}, BidMeta: &openrtb_ext.ExtBidPrebidMeta{DChain: brokenExt}}}}),
	)
	assert.Error(t, trace.SetFinalResponse(testStart, &openrtb2.BidResponse{Ext: brokenExt}))
	assert.True(t, trace.tryMarkEmitted(), "first mark wins")
	assert.False(t, trace.tryMarkEmitted(), "second mark must be refused")
}
