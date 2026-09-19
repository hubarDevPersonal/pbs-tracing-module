package testtracer

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/adcom1"
	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/openrtb_ext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M-26. FR-14: an active trace must not be touched by invocations for another endpoint.
func TestHooks_IgnoreOtherEndpointEvenWithActiveTrace(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	body := loadSampleRequest(t)
	mc := tracedContext(t, m, sampleRequestAccountID, body)
	amp := invocationCtx("/openrtb2/amp", sampleRequestAccountID, mc)
	ctx := t.Context()

	_, err := m.HandleBidderRequestHook(ctx, amp, hookstage.BidderRequestPayload{Request: requestWrapperFrom(t, body), Bidder: "appnexus"})
	require.NoError(t, err)
	_, err = m.HandleRawBidderResponseHook(ctx, amp, hookstage.RawBidderResponsePayload{BidderResponse: sampleBidderResponse("appnexus", "b", 1), Bidder: "appnexus"})
	require.NoError(t, err)
	_, err = m.HandleAllProcessedBidResponsesHook(ctx, amp, hookstage.AllProcessedBidResponsesPayload{})
	require.NoError(t, err)
	_, err = m.HandleAuctionResponseHook(ctx, amp, hookstage.AuctionResponsePayload{BidResponse: sampleBidResponse("r")})
	require.NoError(t, err)
	_, err = m.HandleExitpointHook(ctx, amp, hookstage.ExitpointPayload{Response: sampleBidResponse("r"), W: httptest.NewRecorder()})
	require.NoError(t, err)

	trace := traceIn(mc)
	require.NotNil(t, trace, "trace must still be pending")
	p := trace.Packet(testStart)
	assert.Empty(t, p.BidderRequests)
	assert.Empty(t, p.BidderResponses)
	assert.Nil(t, p.FinalResponse)
	assert.Empty(t, out.Lines())
}

// M-29. FR-15 AC3: nil payload fields inside a traced request are skipped, never fatal.
func TestHooks_NilPayloadsInsideTracedRequestAreSkipped(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	body := loadSampleRequest(t)
	mc := tracedContext(t, m, sampleRequestAccountID, body)
	ctx := t.Context()

	_, err := m.HandleBidderRequestHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.BidderRequestPayload{Bidder: "appnexus"})
	require.NoError(t, err)
	_, err = m.HandleBidderRequestHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.BidderRequestPayload{Request: &openrtb_ext.RequestWrapper{}, Bidder: "appnexus"})
	require.NoError(t, err)
	_, err = m.HandleRawBidderResponseHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.RawBidderResponsePayload{Bidder: "appnexus"})
	require.NoError(t, err)
	_, err = m.HandleAuctionResponseHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.AuctionResponsePayload{})
	require.NoError(t, err)
	_, err = m.HandleExitpointHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.ExitpointPayload{Response: nil, W: httptest.NewRecorder()})
	require.NoError(t, err)

	packets := out.Packets(t)
	require.Len(t, packets, 1, "the packet is still emitted")
	assert.Empty(t, packets[0].BidderRequests)
	assert.Empty(t, packets[0].BidderResponses)
	assert.Nil(t, packets[0].FinalResponse)
}

// M-28. FR-15 AC2: an output failure is logged, not returned; the auction is unaffected.
func TestExitpoint_EmitFailureDoesNotFailTheHook(t *testing.T) {
	m, err := newModule(testRules(), newJSONEmitter(failingWriter{err: errors.New("stdout closed")}), newFakeClock(testStart).Now)
	require.NoError(t, err)
	body := loadSampleRequest(t)
	mc := tracedContext(t, m, sampleRequestAccountID, body)

	final := sampleBidResponse("r", "appnexus")
	res, err := m.HandleExitpointHook(t.Context(), auctionCtx(sampleRequestAccountID, mc), hookstage.ExitpointPayload{Response: final, W: httptest.NewRecorder()})

	require.NoError(t, err)
	assert.False(t, res.Reject)
	assert.Nil(t, traceIn(mc), "state is released even when the write failed")
}

// M-13. FR-06 / D10: optional TypedBid parts and FLEDGE configs are carried into the DTO.
func TestAuctionTrace_BidderResponseSnapshotCoversOptionalFields(t *testing.T) {
	tr := newTestTracer(t, testRules(), newFakeClock(testStart))
	trace, ok := tr.Begin(sampleRequestAccountID, "a", time.Time{})
	require.True(t, ok)

	resp := &adapters.BidderResponse{
		Currency: "EUR",
		Bids: []*adapters.TypedBid{{
			Bid:          &openrtb2.Bid{ID: "v1", ImpID: "i", Price: 4},
			BidType:      openrtb_ext.BidTypeVideo,
			BidMeta:      &openrtb_ext.ExtBidPrebidMeta{AdvertiserDomains: []string{"adv.example"}, MediaType: "video"},
			BidVideo:     &openrtb_ext.ExtBidPrebidVideo{Duration: 30, PrimaryCategory: "IAB1"},
			DealPriority: 5,
			Seat:         "appnexus",
		}},
		FledgeAuctionConfigs: []*openrtb_ext.FledgeAuctionConfig{{ImpId: "i", Bidder: "appnexus", Config: json.RawMessage(`{"seller":"https://s.example"}`)}},
	}
	require.NoError(t, trace.AddBidderResponse(testStart, "appnexus", resp))

	p := trace.Packet(testStart)
	require.Len(t, p.BidderResponses, 1)
	v := p.BidderResponses[0].Response
	assert.Equal(t, "EUR", v.Currency)
	require.Len(t, v.Bids, 1)
	assert.Equal(t, "video", v.Bids[0].BidType)
	assert.Equal(t, 5, v.Bids[0].DealPriority)
	assert.Contains(t, string(v.Bids[0].BidMeta), `"adv.example"`)
	assert.Contains(t, string(v.Bids[0].BidVideo), `"duration":30`)
	assert.Contains(t, string(v.FledgeAuctionConfigs), `"seller"`)

	// the whole packet must still serialize as one valid JSON document
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	assert.True(t, json.Valid(raw))
}

// M-09. FR-04: a non-JSON entrypoint body must not break the packet.
func TestAuctionTrace_NonJSONIncomingBodyIsWrappedAsString(t *testing.T) {
	tr := newTestTracer(t, testRules(), newFakeClock(testStart))
	trace, ok := tr.Begin(sampleRequestAccountID, "a", time.Time{})
	require.True(t, ok)

	trace.SetIncomingRequest(testStart, []byte("this is not json"))

	raw, err := json.Marshal(trace.Packet(testStart))
	require.NoError(t, err)
	var obj struct {
		IncomingRequest struct {
			Body string `json:"body"`
		} `json:"incoming_request"`
	}
	require.NoError(t, json.Unmarshal(raw, &obj))
	assert.Equal(t, "this is not json", obj.IncomingRequest.Body)
}

// M-19. FR-08 AC5 via the hooks: mixed-zone clock still yields UTC output.
func TestExitpoint_NormalisesTimestampsToUTC(t *testing.T) {
	zone := time.FixedZone("EEST", 3*3600)
	clock := newFakeClock(time.Date(2026, 9, 16, 13, 0, 0, 0, zone))
	m, out := newTestModule(t, testRules(), clock)
	runFullAuction(t, m, sampleRequestAccountID, loadSampleRequest(t), []string{"appnexus"}, sampleBidResponse("r", "appnexus"))

	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out.Lines()[0]), &obj))
	var started string
	require.NoError(t, json.Unmarshal(obj["started_at"], &started))
	assert.Equal(t, "2026-09-16T10:00:00Z", started)
}

// Sanity: the adcom1 import is used so the video/category types stay in sync with openrtb.
var _ = adcom1.CategoryTaxonomy(0)
