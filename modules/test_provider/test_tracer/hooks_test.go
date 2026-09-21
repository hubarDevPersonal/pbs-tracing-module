package testtracer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/openrtb_ext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M-07. FR-04 AC1
func TestEntrypoint_CapturesBodyAndTimestamp(t *testing.T) {
	clock := newFakeClock(testStart)
	m, _ := newTestModule(t, testRules(), clock)
	body := loadSampleRequest(t)

	res, err := m.HandleEntrypointHook(t.Context(), auctionCtx("", nil), entrypointPayload(body))

	require.NoError(t, err)
	require.NotNil(t, res.ModuleContext, "entrypoint must create the module context when PBS passes nil")
	v, ok := res.ModuleContext.Get(ctxKeyEntrypoint)
	require.True(t, ok)
	capture, ok := v.(entrypointCapture)
	require.True(t, ok)
	assert.True(t, capture.at.Equal(testStart))
	assert.Equal(t, body, capture.body)
}

// M-08. FR-04 AC2
func TestEntrypoint_CopiesBody(t *testing.T) {
	m, _ := newTestModule(t, testRules(), newFakeClock(testStart))
	body := []byte(`{"id":"original"}`)

	res, err := m.HandleEntrypointHook(t.Context(), auctionCtx("", nil), entrypointPayload(body))
	require.NoError(t, err)
	require.NotNil(t, res.ModuleContext)
	copy(body, `{"id":"MUTATED!"}`)

	v, ok := res.ModuleContext.Get(ctxKeyEntrypoint)
	require.True(t, ok)
	capture, ok := v.(entrypointCapture)
	require.True(t, ok)
	assert.JSONEq(t, `{"id":"original"}`, string(capture.body))
}

// M-26. FR-14
func TestEntrypoint_IgnoresNonAuctionEndpoint(t *testing.T) {
	m, _ := newTestModule(t, testRules(), newFakeClock(testStart))

	res, err := m.HandleEntrypointHook(t.Context(), invocationCtx("/openrtb2/amp", "", nil), entrypointPayload(loadSampleRequest(t)))

	require.NoError(t, err)
	if res.ModuleContext != nil {
		_, ok := res.ModuleContext.Get(ctxKeyEntrypoint)
		assert.False(t, ok, "nothing must be captured for other endpoints")
	}
}

// M-05. FR-03 AC1
func TestProcessedAuction_StartsTraceForMatchingAccount(t *testing.T) {
	clock := newFakeClock(testStart)
	m, _ := newTestModule(t, testRules(), clock)
	body := loadSampleRequest(t)

	mc := tracedContext(t, m, sampleRequestAccountID, body)

	trace := traceIn(mc)
	require.NotNil(t, trace, "trace must be stored in the module context")
	assert.Equal(t, sampleRequestAccountID, trace.PartnerID())
	assert.Equal(t, "5d394bed0104ca857c702982fe8d95e408820eb2-3", trace.AuctionID())
	assert.Equal(t, 1, trace.PacketIndex())

	st, found := m.tracer.Status(sampleRequestAccountID)
	require.True(t, found)
	assert.Equal(t, 1, st.Packets)
}

// M-07. FR-04 AC1: the entrypoint capture becomes incoming_request.
func TestProcessedAuction_AttachesEntrypointCapture(t *testing.T) {
	clock := newFakeClock(testStart)
	m, _ := newTestModule(t, testRules(), clock)
	body := loadSampleRequest(t)

	entry, err := m.HandleEntrypointHook(t.Context(), auctionCtx("", nil), entrypointPayload(body))
	require.NoError(t, err)
	clock.Advance(50 * time.Millisecond)
	_, err = m.HandleProcessedAuctionHook(t.Context(), auctionCtx(sampleRequestAccountID, entry.ModuleContext),
		hookstage.ProcessedAuctionRequestPayload{Request: requestWrapperFrom(t, body)})
	require.NoError(t, err)

	trace := traceIn(entry.ModuleContext)
	require.NotNil(t, trace)
	p := trace.Packet(clock.Now())
	require.NotNil(t, p.IncomingRequest)
	assert.True(t, p.IncomingRequest.Timestamp.Equal(testStart), "incoming timestamp is the entrypoint time")
	assert.JSONEq(t, string(body), string(p.IncomingRequest.Body))
	assert.True(t, p.StartedAt.Equal(testStart.Add(50*time.Millisecond)), "trace start is the trigger time")
}

// M-05. FR-03 AC1 / FR-13
func TestProcessedAuction_NoTraceForUnknownAccount(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))

	mc := tracedContext(t, m, "33415-10498", loadSampleRequest(t)) // publisher.id, not the resolved account

	assert.Nil(t, traceIn(mc))
	_, found := m.tracer.Status("33415-10498")
	assert.False(t, found)
	assert.Empty(t, out.Lines())
}

// M-09. FR-04 AC3
func TestProcessedAuction_FallsBackWhenEntrypointCaptureMissing(t *testing.T) {
	// a clock that advances on every read exposes any second now() taken after Tracer.Begin
	cur := testStart
	ticking := func() time.Time { cur = cur.Add(time.Microsecond); return cur }
	m, err := newModule(testRules(), newJSONEmitter(&syncBuffer{}), ticking)
	require.NoError(t, err)
	wrapper := requestWrapperFrom(t, loadSampleRequest(t))

	res, err := m.HandleProcessedAuctionHook(t.Context(), auctionCtx(sampleRequestAccountID, nil),
		hookstage.ProcessedAuctionRequestPayload{Request: wrapper})

	require.NoError(t, err)
	require.NotNil(t, res.ModuleContext, "module must create the context when the plan has no entrypoint stage")
	trace := traceIn(res.ModuleContext)
	require.NotNil(t, trace)

	p := trace.Packet(ticking())
	require.NotNil(t, p.IncomingRequest)
	assert.True(t, p.IncomingRequest.Timestamp.Equal(p.StartedAt), "fallback incoming timestamp must be the trace start (FR-04 AC3)")
	want, err := json.Marshal(wrapper.BidRequest)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(p.IncomingRequest.Body))
}

// M-21. FR-10 via the hook path
func TestProcessedAuction_RespectsStopConditions(t *testing.T) {
	m, _ := newTestModule(t, []Rule{{PartnerID: sampleRequestAccountID, Duration: time.Hour, TracePacketsAmount: 1}}, newFakeClock(testStart))
	body := loadSampleRequest(t)

	first := tracedContext(t, m, sampleRequestAccountID, body)
	second := tracedContext(t, m, sampleRequestAccountID, body)

	assert.NotNil(t, traceIn(first))
	assert.Nil(t, traceIn(second))
}

// M-10. FR-05 AC1
func TestBidderRequest_RecordsOutgoingRequestForTracedAuction(t *testing.T) {
	clock := newFakeClock(testStart)
	m, _ := newTestModule(t, testRules(), clock)
	body := loadSampleRequest(t)
	mc := tracedContext(t, m, sampleRequestAccountID, body)

	clock.Advance(time.Second)
	outgoing := requestWrapperFrom(t, body)
	outgoing.ID = "outgoing-for-appnexus"
	_, err := m.HandleBidderRequestHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
		hookstage.BidderRequestPayload{Request: outgoing, Bidder: "appnexus"})
	require.NoError(t, err)

	trace := traceIn(mc)
	require.NotNil(t, trace)
	p := trace.Packet(clock.Now())
	require.Len(t, p.BidderRequests, 1)
	assert.Equal(t, "appnexus", p.BidderRequests[0].Bidder)
	assert.True(t, p.BidderRequests[0].Timestamp.Equal(testStart.Add(time.Second)))
	want, _ := json.Marshal(outgoing.BidRequest)
	assert.JSONEq(t, string(want), string(p.BidderRequests[0].Request))
}

// M-06, M-12. FR-05 AC3 / FR-15
func TestBidderRequest_IsNoopWithoutActiveTrace(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	body := loadSampleRequest(t)

	res, err := m.HandleBidderRequestHook(t.Context(), auctionCtx("unknown", hookstage.NewModuleContext()),
		hookstage.BidderRequestPayload{Request: requestWrapperFrom(t, body), Bidder: "appnexus"})

	require.NoError(t, err)
	assert.False(t, res.Reject)
	assert.Empty(t, res.ChangeSet.Mutations())
	assert.Empty(t, out.Lines())
}

// M-13. FR-06 AC1
func TestRawBidderResponse_RecordsIncomingResponse(t *testing.T) {
	clock := newFakeClock(testStart)
	m, _ := newTestModule(t, testRules(), clock)
	mc := tracedContext(t, m, sampleRequestAccountID, loadSampleRequest(t))

	clock.Advance(2 * time.Second)
	_, err := m.HandleRawBidderResponseHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
		hookstage.RawBidderResponsePayload{BidderResponse: sampleBidderResponse("appnexus", "bid-42", 3.14), Bidder: "appnexus"})
	require.NoError(t, err)

	trace := traceIn(mc)
	require.NotNil(t, trace)
	p := trace.Packet(clock.Now())
	require.Len(t, p.BidderResponses, 1)
	assert.Equal(t, "appnexus", p.BidderResponses[0].Bidder)
	assert.True(t, p.BidderResponses[0].Timestamp.Equal(testStart.Add(2*time.Second)))
	require.Len(t, p.BidderResponses[0].Response.Bids, 1)
	assert.Equal(t, "bid-42", p.BidderResponses[0].Response.Bids[0].Bid.ID)
	assert.Equal(t, 3.14, p.BidderResponses[0].Response.Bids[0].Bid.Price)
}

// M-15. FR-07 AC1
func TestAuctionResponse_RecordsFinalResponse(t *testing.T) {
	clock := newFakeClock(testStart)
	m, _ := newTestModule(t, testRules(), clock)
	mc := tracedContext(t, m, sampleRequestAccountID, loadSampleRequest(t))

	clock.Advance(3 * time.Second)
	_, err := m.HandleAuctionResponseHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
		hookstage.AuctionResponsePayload{BidResponse: sampleBidResponse("final-1", "appnexus")})
	require.NoError(t, err)

	trace := traceIn(mc)
	require.NotNil(t, trace)
	resp, at := trace.AuctionResponse()
	require.NotNil(t, resp)
	assert.Equal(t, "final-1", resp.ID)
	assert.True(t, at.Equal(testStart.Add(3*time.Second)))
	assert.Nil(t, trace.Packet(clock.Now()).FinalResponse, "marshaled at exitpoint, not before")
}

// M-16. FR-08 AC1 / FR-05 / FR-06 / FR-07 end-to-end through the hooks
func TestExitpoint_EmitsSinglePacketWithAllSections(t *testing.T) {
	clock := newFakeClock(testStart)
	m, out := newTestModule(t, testRules(), clock)
	body := loadSampleRequest(t)

	runFullAuction(t, m, sampleRequestAccountID, body, []string{"appnexus", "amx"}, sampleBidResponse("5d394bed0104ca857c702982fe8d95e408820eb2-3", "appnexus"))

	packets := out.Packets(t)
	require.Len(t, packets, 1)
	p := packets[0]
	assert.Equal(t, ModuleCode, p.Module)
	assert.Equal(t, sampleRequestAccountID, p.PartnerID)
	assert.Equal(t, 1, p.PacketIndex)
	assert.Equal(t, "5d394bed0104ca857c702982fe8d95e408820eb2-3", p.AuctionID)
	require.NotNil(t, p.IncomingRequest)
	assert.JSONEq(t, string(body), string(p.IncomingRequest.Body))
	assert.Len(t, p.BidderRequests, 2)
	assert.Len(t, p.BidderResponses, 2)
	require.NotNil(t, p.FinalResponse)
	assert.Contains(t, string(p.FinalResponse.Body), `"seat":"appnexus"`)
}

// M-14. FR-06 AC2: a bidder without raw_bidder_response (HTTP 204) still yields a packet.
func TestExitpoint_EmitsPacketWhenBidderReturnedNoResponse(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	body := loadSampleRequest(t)
	mc := tracedContext(t, m, sampleRequestAccountID, body)

	_, err := m.HandleBidderRequestHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
		hookstage.BidderRequestPayload{Request: requestWrapperFrom(t, body), Bidder: "appnexus"})
	require.NoError(t, err)
	final := &openrtb2.BidResponse{ID: "no-bids"}
	_, err = m.HandleAuctionResponseHook(t.Context(), auctionCtx(sampleRequestAccountID, mc), hookstage.AuctionResponsePayload{BidResponse: final})
	require.NoError(t, err)
	_, err = m.HandleExitpointHook(t.Context(), auctionCtx(sampleRequestAccountID, mc), hookstage.ExitpointPayload{Response: final, W: httptest.NewRecorder()})
	require.NoError(t, err)

	packets := out.Packets(t)
	require.Len(t, packets, 1)
	assert.Len(t, packets[0].BidderRequests, 1)
	assert.Empty(t, packets[0].BidderResponses)
	assert.NotNil(t, packets[0].FinalResponse)
}

// M-15. FR-07 AC2
func TestExitpoint_UsesExitpointResponseWhenAvailable(t *testing.T) {
	clock := newFakeClock(testStart)
	m, out := newTestModule(t, testRules(), clock)
	mc := tracedContext(t, m, sampleRequestAccountID, loadSampleRequest(t))

	_, err := m.HandleAuctionResponseHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
		hookstage.AuctionResponsePayload{BidResponse: sampleBidResponse("from-auction-response")})
	require.NoError(t, err)
	clock.Advance(time.Second)
	_, err = m.HandleExitpointHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
		hookstage.ExitpointPayload{Response: sampleBidResponse("from-exitpoint"), W: httptest.NewRecorder()})
	require.NoError(t, err)

	packets := out.Packets(t)
	require.Len(t, packets, 1)
	require.NotNil(t, packets[0].FinalResponse)
	assert.Contains(t, string(packets[0].FinalResponse.Body), `"from-exitpoint"`)
	assert.True(t, packets[0].FinalResponse.Timestamp.Equal(testStart.Add(time.Second)))
	assert.True(t, packets[0].CompletedAt.Equal(testStart.Add(time.Second)))
}

// M-15. FR-07 AC2 (fallback branch)
func TestExitpoint_FallsBackToAuctionResponseWhenPayloadIsNotBidResponse(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	mc := tracedContext(t, m, sampleRequestAccountID, loadSampleRequest(t))

	_, err := m.HandleAuctionResponseHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
		hookstage.AuctionResponsePayload{BidResponse: sampleBidResponse("from-auction-response")})
	require.NoError(t, err)
	_, err = m.HandleExitpointHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
		hookstage.ExitpointPayload{Response: map[string]any{"custom": true}, W: httptest.NewRecorder()})
	require.NoError(t, err)

	packets := out.Packets(t)
	require.Len(t, packets, 1)
	require.NotNil(t, packets[0].FinalResponse)
	assert.Contains(t, string(packets[0].FinalResponse.Body), `"from-auction-response"`)
	assert.True(t, packets[0].FinalResponse.Timestamp.Equal(testStart), "the auction_response capture keeps its own timestamp")
}

// M-06, M-18. FR-08 AC4 / FR-13
func TestExitpoint_EmitsNothingWithoutActiveTrace(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))

	for _, mc := range []*hookstage.ModuleContext{nil, hookstage.NewModuleContext()} {
		_, err := m.HandleExitpointHook(t.Context(), auctionCtx("unknown", mc),
			hookstage.ExitpointPayload{Response: sampleBidResponse("x"), W: httptest.NewRecorder()})
		require.NoError(t, err)
	}
	assert.Empty(t, out.Lines())
}

// M-17. FR-08 AC3 / NFR-02
func TestExitpoint_DoesNotEmitTwice(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	mc := runFullAuction(t, m, sampleRequestAccountID, loadSampleRequest(t), []string{"appnexus"}, sampleBidResponse("r", "appnexus"))

	_, err := m.HandleExitpointHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
		hookstage.ExitpointPayload{Response: sampleBidResponse("r", "appnexus"), W: httptest.NewRecorder()})
	require.NoError(t, err)

	assert.Len(t, out.Lines(), 1)
	assert.Nil(t, traceIn(mc), "trace must be released from the module context after emission")
}

// M-27. FR-15 AC1
func TestHooks_NeverRejectNeverMutate(t *testing.T) {
	m, _ := newTestModule(t, testRules(), newFakeClock(testStart))
	body := loadSampleRequest(t)
	ctx := t.Context()

	entry, err := m.HandleEntrypointHook(ctx, auctionCtx("", nil), entrypointPayload(body))
	require.NoError(t, err)
	assert.False(t, entry.Reject)
	assert.Zero(t, entry.NbrCode)
	assert.Empty(t, entry.ChangeSet.Mutations())
	mc := entry.ModuleContext

	processed, err := m.HandleProcessedAuctionHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.ProcessedAuctionRequestPayload{Request: requestWrapperFrom(t, body)})
	require.NoError(t, err)
	assert.False(t, processed.Reject)
	assert.Empty(t, processed.ChangeSet.Mutations())

	bidderReq, err := m.HandleBidderRequestHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.BidderRequestPayload{Request: requestWrapperFrom(t, body), Bidder: "appnexus"})
	require.NoError(t, err)
	assert.False(t, bidderReq.Reject)
	assert.Empty(t, bidderReq.ChangeSet.Mutations())

	bidderResp, err := m.HandleRawBidderResponseHook(
		ctx,
		auctionCtx(sampleRequestAccountID, mc),
		hookstage.RawBidderResponsePayload{BidderResponse: sampleBidderResponse("appnexus", "b", 1), Bidder: "appnexus"},
	)
	require.NoError(t, err)
	assert.False(t, bidderResp.Reject)
	assert.Empty(t, bidderResp.ChangeSet.Mutations())

	all, err := m.HandleAllProcessedBidResponsesHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.AllProcessedBidResponsesPayload{})
	require.NoError(t, err)
	assert.False(t, all.Reject)
	assert.Empty(t, all.ChangeSet.Mutations())

	final := sampleBidResponse("r", "appnexus")
	auctionResp, err := m.HandleAuctionResponseHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.AuctionResponsePayload{BidResponse: final})
	require.NoError(t, err)
	assert.False(t, auctionResp.Reject)
	assert.Empty(t, auctionResp.ChangeSet.Mutations())

	exit, err := m.HandleExitpointHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.ExitpointPayload{Response: final, W: httptest.NewRecorder()})
	require.NoError(t, err)
	assert.False(t, exit.Reject)
	assert.Empty(t, exit.ChangeSet.Mutations())
}

// M-29. FR-15 AC3
func TestHooks_ToleratesNilModuleContextAndNilPayloads(t *testing.T) {
	m, _ := newTestModule(t, testRules(), newFakeClock(testStart))
	ctx := t.Context()
	mc := (*hookstage.ModuleContext)(nil)

	assert.NotPanics(t, func() {
		_, err := m.HandleEntrypointHook(ctx, auctionCtx("", mc), hookstage.EntrypointPayload{})
		assert.NoError(t, err)
		_, err = m.HandleProcessedAuctionHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.ProcessedAuctionRequestPayload{})
		assert.NoError(t, err)
		_, err = m.HandleBidderRequestHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.BidderRequestPayload{})
		assert.NoError(t, err)
		_, err = m.HandleRawBidderResponseHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.RawBidderResponsePayload{})
		assert.NoError(t, err)
		_, err = m.HandleAllProcessedBidResponsesHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.AllProcessedBidResponsesPayload{})
		assert.NoError(t, err)
		_, err = m.HandleAuctionResponseHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.AuctionResponsePayload{})
		assert.NoError(t, err)
		_, err = m.HandleExitpointHook(ctx, auctionCtx(sampleRequestAccountID, mc), hookstage.ExitpointPayload{})
		assert.NoError(t, err)
	})

	// a traced request whose bidder payloads are nil must not panic either
	traced := tracedContext(t, m, sampleRequestAccountID, loadSampleRequest(t))
	assert.NotPanics(t, func() {
		_, err := m.HandleBidderRequestHook(ctx, auctionCtx(sampleRequestAccountID, traced), hookstage.BidderRequestPayload{Bidder: "appnexus"})
		assert.NoError(t, err)
		_, err = m.HandleRawBidderResponseHook(ctx, auctionCtx(sampleRequestAccountID, traced), hookstage.RawBidderResponsePayload{Bidder: "appnexus"})
		assert.NoError(t, err)
		_, err = m.HandleAuctionResponseHook(ctx, auctionCtx(sampleRequestAccountID, traced), hookstage.AuctionResponsePayload{})
		assert.NoError(t, err)
	})
}

// M-33. NFR-02: the entrypoint copy of a request that is not traced is released at
// processed_auction_request, not kept until the request context dies.
func TestProcessedAuction_ReleasesEntrypointCaptureWhenNotTraced(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	body := loadSampleRequest(t)

	entry, err := m.HandleEntrypointHook(context.Background(), auctionCtx("", nil), entrypointPayload(body))
	require.NoError(t, err)
	mc := entry.ModuleContext
	v, found := mc.Get(ctxKeyEntrypoint)
	require.True(t, found)
	require.NotNil(t, v, "the copy exists between entrypoint and the trigger decision")

	_, err = m.HandleProcessedAuctionHook(context.Background(), auctionCtx("not-a-partner", mc), hookstage.ProcessedAuctionRequestPayload{Request: requestWrapperFrom(t, body)})
	require.NoError(t, err)

	v, _ = mc.Get(ctxKeyEntrypoint)
	assert.Nil(t, v, "the copy is released once the request is known not to be traced")
	assert.Empty(t, out.Lines())
}

// M-34. NFR-01: once every partner is stopped, entrypoint no longer copies the body of any request.
func TestEntrypoint_SkipsBodyCopyWhenAllPartnersAreStopped(t *testing.T) {
	m, _ := newTestModule(t, []Rule{{PartnerID: "p", Duration: time.Hour, TracePacketsAmount: 1}}, newFakeClock(testStart))
	body := loadSampleRequest(t)

	trace, ok := m.tracer.Begin("p", "a", time.Time{})
	require.True(t, ok)
	m.tracer.Complete(trace)
	require.True(t, m.tracer.Exhausted(testStart))

	res, err := m.HandleEntrypointHook(context.Background(), auctionCtx("", nil), entrypointPayload(body))
	require.NoError(t, err)
	assert.Nil(t, res.ModuleContext, "no context is created and no body is copied")
	assert.False(t, res.Reject)
}

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

// An invalid json.RawMessage makes encoding/json fail, which is the only way the module's snapshot
// marshaling can error with real PBS types. FR-15 AC2: such failures are logged, never returned.
var brokenExt = json.RawMessage(`{"not":"closed"`)

// M-28. FR-15 AC2: a marshal failure inside a hook is logged, the hook still succeeds.
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

// M-09, M-28. FR-04 AC3 error branch: the processed-request fallback cannot marshal → no incoming_request, trace still starts.
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

// M-39. FR-10 AC2, AC4: the provided pbs.yaml lets an auction run up to 10 minutes, longer than the slot
// lease. Three auctions still running when their leases end give their slots to three new ones; only the
// new ones write, so the partner never gets more than TracePacketsAmount packets.
func TestExitpoint_AuctionThatOutlivedItsLeaseWritesNothing(t *testing.T) {
	clock := newFakeClock(testStart)
	m, out := newTestModule(t, []Rule{{PartnerID: sampleRequestAccountID, Duration: 10 * time.Minute, TracePacketsAmount: 3}}, clock)
	body := loadSampleRequest(t)

	slow := make([]*hookstage.ModuleContext, 0, 3)
	for range 3 {
		slow = append(slow, tracedContext(t, m, sampleRequestAccountID, body))
	}
	clock.Advance(slotLease + time.Second)
	reusedAt := clock.Now()
	fresh := make([]*hookstage.ModuleContext, 0, 3)
	for range 3 {
		mc := tracedContext(t, m, sampleRequestAccountID, body)
		require.NotNil(t, traceIn(mc), "the lease is over, the slot is given back")
		fresh = append(fresh, mc)
	}
	for _, mc := range append(slow, fresh...) {
		_, err := m.HandleExitpointHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
			hookstage.ExitpointPayload{Response: sampleBidResponse("r"), W: httptest.NewRecorder()})
		require.NoError(t, err)
	}

	packets := out.Packets(t)
	require.Len(t, packets, 3, "never more packets than TracePacketsAmount")
	indices := make([]int, 0, len(packets))
	for _, p := range packets {
		assert.False(t, p.StartedAt.Before(reusedAt), "only the auctions that hold the slots write")
		indices = append(indices, p.PacketIndex)
	}
	assert.ElementsMatch(t, []int{1, 2, 3}, indices)
	for _, mc := range slow {
		assert.Nil(t, traceIn(mc), "the trace of an auction that lost its slot is released")
	}
}
