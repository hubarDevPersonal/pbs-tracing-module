package testtracer

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestTracer(t *testing.T, rules []Rule, clock *fakeClock) *Tracer {
	t.Helper()
	tr, err := newTracer(rules, clock.Now)
	require.NoError(t, err)
	require.NotNil(t, tr)
	return tr
}

// M-02. FR-02
func TestNewTracer_RejectsInvalidRules(t *testing.T) {
	_, err := newTracer([]Rule{{PartnerID: "", Duration: time.Second, TracePacketsAmount: 1}}, time.Now)
	assert.Error(t, err)
}

// M-05. FR-03 / FR-13
func TestTracerBegin_UnknownPartnerIsNotTraced(t *testing.T) {
	tr := newTestTracer(t, testRules(), newFakeClock(testStart))

	trace, ok := tr.Begin("unknown-partner", "auction-1")

	assert.False(t, ok)
	assert.Nil(t, trace)
	_, found := tr.Status("unknown-partner")
	assert.False(t, found, "unknown partners must not allocate state")
}

// M-20. FR-03 / FR-09: the first traced request opens the window.
func TestTracerBegin_FirstPacketStartsWindow(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, testRules(), clock)

	trace, ok := tr.Begin(sampleRequestAccountID, "auction-1")

	require.True(t, ok)
	require.NotNil(t, trace)
	assert.Equal(t, sampleRequestAccountID, trace.PartnerID())
	assert.Equal(t, "auction-1", trace.AuctionID())
	assert.Equal(t, 1, trace.PacketIndex())

	st, found := tr.Status(sampleRequestAccountID)
	require.True(t, found)
	assert.Equal(t, 1, st.Packets)
	assert.True(t, st.FirstTracedAt.Equal(testStart))
	assert.Equal(t, StopReasonNone, st.StopReason)
}

// M-21. FR-10
func TestTracerBegin_AmountLimitStopsTracing(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Hour, TracePacketsAmount: 2}}, clock)

	first, ok1 := tr.Begin("p", "a1")
	second, ok2 := tr.Begin("p", "a2")
	third, ok3 := tr.Begin("p", "a3")

	require.True(t, ok1)
	require.True(t, ok2)
	assert.False(t, ok3)
	assert.Nil(t, third)
	assert.Equal(t, 1, first.PacketIndex())
	assert.Equal(t, 2, second.PacketIndex())

	st, _ := tr.Status("p")
	assert.Equal(t, 2, st.Packets, "packets must never exceed TracePacketsAmount")
	assert.Equal(t, StopReasonAmount, st.StopReason)
}

// M-20. FR-09: stop when elapsed > Duration (strictly "exceeds").
func TestTracerBegin_DurationLimitStopsTracing(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: 10 * time.Second, TracePacketsAmount: 100}}, clock)

	_, ok := tr.Begin("p", "a1")
	require.True(t, ok)

	clock.Advance(10 * time.Second) // elapsed == Duration
	_, ok = tr.Begin("p", "a2")
	assert.True(t, ok, "elapsed == Duration is still inside the window")

	clock.Advance(time.Nanosecond) // elapsed > Duration
	_, ok = tr.Begin("p", "a3")
	assert.False(t, ok)

	st, _ := tr.Status("p")
	assert.Equal(t, 2, st.Packets)
	assert.Equal(t, StopReasonDuration, st.StopReason)
}

// M-23. FR-11: whichever condition is met first wins.
func TestTracerBegin_WhicheverComesFirst(t *testing.T) {
	t.Run("duration before amount", func(t *testing.T) {
		clock := newFakeClock(testStart)
		tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Second, TracePacketsAmount: 10}}, clock)
		_, ok := tr.Begin("p", "a1")
		require.True(t, ok)
		clock.Advance(2 * time.Second)
		_, ok = tr.Begin("p", "a2")
		assert.False(t, ok)
		st, _ := tr.Status("p")
		assert.Equal(t, StopReasonDuration, st.StopReason)
	})

	t.Run("amount before duration", func(t *testing.T) {
		clock := newFakeClock(testStart)
		tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Hour, TracePacketsAmount: 1}}, clock)
		_, ok := tr.Begin("p", "a1")
		require.True(t, ok)
		_, ok = tr.Begin("p", "a2")
		assert.False(t, ok)
		st, _ := tr.Status("p")
		assert.Equal(t, StopReasonAmount, st.StopReason)
	})
}

// M-23. FR-11 AC1
func TestTracerBegin_StoppedPartnerDoesNotRearm(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Second, TracePacketsAmount: 1}}, clock)
	_, ok := tr.Begin("p", "a1")
	require.True(t, ok)

	for _, step := range []time.Duration{0, time.Second, time.Hour, 24 * time.Hour} {
		clock.Advance(step)
		_, ok = tr.Begin("p", "again")
		assert.False(t, ok, "stopped partner re-armed after %s", step)
	}
	st, _ := tr.Status("p")
	assert.Equal(t, 1, st.Packets)
}

// M-23. FR-11 AC2
func TestTracerBegin_PartnersAreIndependent(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{
		{PartnerID: "a", Duration: time.Hour, TracePacketsAmount: 1},
		{PartnerID: "b", Duration: time.Hour, TracePacketsAmount: 2},
	}, clock)

	_, okA1 := tr.Begin("a", "1")
	_, okA2 := tr.Begin("a", "2")
	_, okB1 := tr.Begin("b", "1")
	_, okB2 := tr.Begin("b", "2")
	_, okB3 := tr.Begin("b", "3")

	assert.True(t, okA1)
	assert.False(t, okA2)
	assert.True(t, okB1)
	assert.True(t, okB2)
	assert.False(t, okB3)
}

// M-16. FR-04..FR-07 / spec §5
func TestAuctionTrace_PacketContainsAllSections(t *testing.T) {
	clock := newFakeClock(testStart)
	rule := Rule{PartnerID: "p", Duration: 10 * time.Minute, TracePacketsAmount: 3}
	tr := newTestTracer(t, []Rule{rule}, clock)
	trace, ok := tr.Begin("p", "auction-1")
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
	trace, ok := tr.Begin(sampleRequestAccountID, "a")
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
	trace, ok := tr.Begin(sampleRequestAccountID, "a")
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
	trace, ok := tr.Begin(sampleRequestAccountID, "a")
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
	trace, ok := tr.Begin(sampleRequestAccountID, "a")
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
