package testtracer

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/adcom1"
	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
	"github.com/prebid/prebid-server/v4/openrtb_ext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// Sanity: the adcom1 import is used so the video/category types stay in sync with openrtb.
var _ = adcom1.CategoryTaxonomy(0)

// M-28. AuctionTrace-level: the same failures surface as errors to callers.
// FR-15 AC2: the trace reports marshal failures to the hook, which decides to log them.
func TestAuctionTrace_MarshalErrorsAreReturned(t *testing.T) {
	tr := newTestTracer(t, testRules(), newFakeClock(testStart))
	trace, ok := tr.Begin(sampleRequestAccountID, "a", time.Time{})
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

// M-16. FR-04..FR-07 / spec §5
func TestAuctionTrace_PacketContainsAllSections(t *testing.T) {
	clock := newFakeClock(testStart)
	rule := Rule{PartnerID: "p", Duration: 10 * time.Minute, TracePacketsAmount: 3}
	tr := newTestTracer(t, []Rule{rule}, clock)
	trace, ok := tr.Begin("p", "auction-1", time.Time{})
	require.True(t, ok)

	incoming := []byte(`{"id":"auction-1","imp":[{"id":"1"}]}`)
	ts := func(sec int) time.Time { return testStart.Add(time.Duration(sec) * time.Second) }

	trace.SetIncomingRequest(ts(1), incoming)
	require.NoError(t, trace.AddBidderRequest(ts(2), "appnexus", &openrtb2.BidRequest{ID: "auction-1", Test: 1}))
	require.NoError(t, trace.AddBidderResponse(ts(3), "appnexus", sampleBidderResponse("appnexus", "bid-1", 2.5)))
	require.NoError(t, trace.SetFinalResponse(ts(4), sampleBidResponse("auction-1", "appnexus")))

	p := trace.Packet(ts(5))

	assert.Equal(t, ModuleCode, p.Module)
	assert.Equal(t, "p", p.PartnerID)
	assert.Equal(t, RuleView{PartnerID: "p", Duration: "10m0s", TracePacketsAmount: 3}, p.Rule)
	assert.Equal(t, 1, p.PacketIndex)
	assert.Equal(t, "auction-1", p.AuctionID)
	assert.True(t, p.StartedAt.Equal(testStart))
	assert.True(t, p.CompletedAt.Equal(ts(5)))

	require.NotNil(t, p.IncomingRequest)
	assert.True(t, p.IncomingRequest.Timestamp.Equal(ts(1)))
	assert.JSONEq(t, string(incoming), string(p.IncomingRequest.Body))

	require.Len(t, p.BidderRequests, 1)
	assert.Equal(t, "appnexus", p.BidderRequests[0].Bidder)
	assert.True(t, p.BidderRequests[0].Timestamp.Equal(ts(2)))
	assert.JSONEq(t, `{"id":"auction-1","imp":null,"test":1}`, string(p.BidderRequests[0].Request))

	require.Len(t, p.BidderResponses, 1)
	assert.Equal(t, "appnexus", p.BidderResponses[0].Bidder)
	assert.True(t, p.BidderResponses[0].Timestamp.Equal(ts(3)))
	assert.Equal(t, "USD", p.BidderResponses[0].Response.Currency)
	require.Len(t, p.BidderResponses[0].Response.Bids, 1)
	assert.Equal(t, "bid-1", p.BidderResponses[0].Response.Bids[0].Bid.ID)
	assert.Equal(t, 2.5, p.BidderResponses[0].Response.Bids[0].Bid.Price)
	assert.Equal(t, "banner", p.BidderResponses[0].Response.Bids[0].BidType)
	assert.Equal(t, "appnexus", p.BidderResponses[0].Response.Bids[0].Seat)

	require.NotNil(t, p.FinalResponse)
	assert.True(t, p.FinalResponse.Timestamp.Equal(ts(4)))
	var final openrtb2.BidResponse
	require.NoError(t, json.Unmarshal(p.FinalResponse.Body, &final))
	assert.Equal(t, "auction-1", final.ID)
	require.Len(t, final.SeatBid, 1)
	assert.Equal(t, "appnexus", final.SeatBid[0].Seat)
}

// M-16. spec §5: arrays are never null even when nothing was collected.
// FR-08 AC2 / spec §5: bidder_requests and bidder_responses are [] when empty, never null.
func TestAuctionTrace_EmptyPacketHasEmptyArrays(t *testing.T) {
	tr := newTestTracer(t, testRules(), newFakeClock(testStart))
	trace, ok := tr.Begin(sampleRequestAccountID, "a", time.Time{})
	require.True(t, ok)

	raw, err := json.Marshal(trace.Packet(testStart))
	require.NoError(t, err)

	var asMap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &asMap))
	assert.JSONEq(t, `[]`, string(asMap["bidder_requests"]))
	assert.JSONEq(t, `[]`, string(asMap["bidder_responses"]))
}

// M-08, M-10. FR-05 AC1 / FR-04 AC2: inputs are snapshotted at call time.
func TestAuctionTrace_SnapshotsAreImmutable(t *testing.T) {
	tr := newTestTracer(t, testRules(), newFakeClock(testStart))
	trace, ok := tr.Begin(sampleRequestAccountID, "a", time.Time{})
	require.True(t, ok)

	body := []byte(`{"id":"before"}`)
	trace.SetIncomingRequest(testStart, body)
	copy(body, `{"id":"AFTER!"}`)

	req := &openrtb2.BidRequest{ID: "before"}
	require.NoError(t, trace.AddBidderRequest(testStart, "b", req))
	req.ID = "after"

	resp := sampleBidderResponse("b", "before", 1)
	require.NoError(t, trace.AddBidderResponse(testStart, "b", resp))
	resp.Bids[0].Bid.ID = "after"

	final := sampleBidResponse("before")
	require.NoError(t, trace.SetFinalResponse(testStart, final))
	final.ID = "after"

	p := trace.Packet(testStart)
	assert.JSONEq(t, `{"id":"before"}`, string(p.IncomingRequest.Body))
	assert.Contains(t, string(p.BidderRequests[0].Request), `"before"`)
	assert.Equal(t, "before", p.BidderResponses[0].Response.Bids[0].Bid.ID)
	assert.Contains(t, string(p.FinalResponse.Body), `"before"`)
}

// M-11. FR-05 AC2
func TestAuctionTrace_PreservesInvocationOrder(t *testing.T) {
	tr := newTestTracer(t, testRules(), newFakeClock(testStart))
	trace, ok := tr.Begin(sampleRequestAccountID, "a", time.Time{})
	require.True(t, ok)

	for _, b := range []string{"aceex", "appnexus", "amx", "adyoulike"} {
		require.NoError(t, trace.AddBidderRequest(testStart, b, &openrtb2.BidRequest{ID: b}))
		require.NoError(t, trace.AddBidderResponse(testStart, b, sampleBidderResponse(b, b, 1)))
	}

	p := trace.Packet(testStart)
	reqOrder := make([]string, 0, len(p.BidderRequests))
	respOrder := make([]string, 0, len(p.BidderResponses))
	for _, r := range p.BidderRequests {
		reqOrder = append(reqOrder, r.Bidder)
	}
	for _, r := range p.BidderResponses {
		respOrder = append(respOrder, r.Bidder)
	}
	assert.Equal(t, []string{"aceex", "appnexus", "amx", "adyoulike"}, reqOrder)
	assert.Equal(t, []string{"aceex", "appnexus", "amx", "adyoulike"}, respOrder)
}

// M-29. FR-15 AC3
func TestAuctionTrace_NilInputsAreSafe(t *testing.T) {
	tr := newTestTracer(t, testRules(), newFakeClock(testStart))
	trace, ok := tr.Begin(sampleRequestAccountID, "a", time.Time{})
	require.True(t, ok)

	assert.Error(t, trace.AddBidderRequest(testStart, "b", nil))
	assert.Error(t, trace.AddBidderResponse(testStart, "b", nil))
	assert.Error(t, trace.SetFinalResponse(testStart, nil))
	assert.NotPanics(t, func() { trace.SetIncomingRequest(testStart, nil) })

	// a response with a nil TypedBid entry must not panic either
	assert.NotPanics(t, func() {
		_ = trace.AddBidderResponse(testStart, "b", &adapters.BidderResponse{Currency: "USD", Bids: []*adapters.TypedBid{nil}})
	})

	p := trace.Packet(testStart)
	assert.Empty(t, p.BidderRequests)
	assert.Nil(t, p.FinalResponse)
}
