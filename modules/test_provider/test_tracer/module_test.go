package testtracer

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

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

// M-35. FR-08: Shutdown drains the asynchronous output, so a packet emitted right before the
// process stops is still written.
func TestModule_ShutdownDrainsQueuedPackets(t *testing.T) {
	out := &syncBuffer{}
	m, err := newModule(testRules(), newAsyncEmitter(newJSONEmitter(out), 8), newFakeClock(testStart).Now)
	require.NoError(t, err)
	f := newBenchFixture(t)

	runAuctionForBench(m, sampleRequestAccountID, f)
	require.NoError(t, m.Shutdown())
	require.NoError(t, m.Shutdown(), "Shutdown is idempotent")

	require.Len(t, out.Packets(t), 1)
	sync, syncErr := newModule(testRules(), newJSONEmitter(out), time.Now)
	require.NoError(t, syncErr)
	assert.NoError(t, sync.Shutdown(), "a synchronous emitter has nothing to drain")
}
