package testtracer

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/modules/moduledeps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M-01. FR-01 AC1
func TestBuilder_ReturnsModuleImplementingAllPlannedStages(t *testing.T) {
	module, err := Builder(json.RawMessage(`{"enabled": true}`), moduledeps.ModuleDeps{})
	require.NoError(t, err)
	require.NotNil(t, module)

	_, ok := module.(hookstage.Entrypoint)
	assert.True(t, ok, "entrypoint")
	_, ok = module.(hookstage.ProcessedAuctionRequest)
	assert.True(t, ok, "processed_auction_request")
	_, ok = module.(hookstage.BidderRequest)
	assert.True(t, ok, "bidder_request")
	_, ok = module.(hookstage.RawBidderResponse)
	assert.True(t, ok, "raw_bidder_response")
	_, ok = module.(hookstage.AllProcessedBidResponses)
	assert.True(t, ok, "all_processed_bid_responses")
	_, ok = module.(hookstage.AuctionResponse)
	assert.True(t, ok, "auction_response")
	_, ok = module.(hookstage.Exitpoint)
	assert.True(t, ok, "exitpoint")

	m, ok := module.(*Module)
	require.True(t, ok)
	assert.NotNil(t, m.tracer, "Builder must wire the tracer")
	assert.NotNil(t, m.emitter, "Builder must wire the emitter")
	assert.NotNil(t, m.now, "Builder must wire the clock")
}

// M-02. FR-02 AC1
func TestNewModule_FailsOnInvalidRules(t *testing.T) {
	_, err := newModule([]Rule{{PartnerID: "p", Duration: 0, TracePacketsAmount: 1}}, newJSONEmitter(&syncBuffer{}), time.Now)
	assert.Error(t, err)
}

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
	p := trace.Packet(clock.Now())
	require.NotNil(t, p.FinalResponse)
	assert.True(t, p.FinalResponse.Timestamp.Equal(testStart.Add(3*time.Second)))
	assert.Contains(t, string(p.FinalResponse.Body), `"final-1"`)
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

// M-26. FR-14 AC1
func TestModule_TracesOnlyAuctionEndpoint(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	body := loadSampleRequest(t)
	amp := func(mc *hookstage.ModuleContext) hookstage.ModuleInvocationContext {
		return invocationCtx("/openrtb2/amp", sampleRequestAccountID, mc)
	}

	entry, err := m.HandleEntrypointHook(t.Context(), amp(nil), entrypointPayload(body))
	require.NoError(t, err)
	mc := entry.ModuleContext
	if mc == nil {
		mc = hookstage.NewModuleContext()
	}
	_, err = m.HandleProcessedAuctionHook(t.Context(), amp(mc), hookstage.ProcessedAuctionRequestPayload{Request: requestWrapperFrom(t, body)})
	require.NoError(t, err)
	_, err = m.HandleExitpointHook(t.Context(), amp(mc), hookstage.ExitpointPayload{Response: sampleBidResponse("amp"), W: httptest.NewRecorder()})
	require.NoError(t, err)

	assert.Nil(t, traceIn(mc))
	_, found := m.tracer.Status(sampleRequestAccountID)
	assert.False(t, found, "other endpoints must not consume the partner's packet budget")
	assert.Empty(t, out.Lines())
}

// M-03. FR-02 AC2: an empty rule set is valid and makes the module a no-op for every account.
func TestModule_EmptyRuleSetTracesNothing(t *testing.T) {
	m, out := newTestModule(t, nil, newFakeClock(testStart))
	f := newBenchFixture(t)

	for _, account := range []string{sampleRequestAccountID, "partner-two", ""} {
		runAuctionForBench(m, account, f)
	}
	assert.Empty(t, out.Lines())
}
