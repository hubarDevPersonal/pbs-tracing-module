package testtracer

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/config"
	"github.com/prebid/prebid-server/v4/hooks"
	"github.com/prebid/prebid-server/v4/hooks/hookexecution"
	metricsConfig "github.com/prebid/prebid-server/v4/metrics/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// providedPlanJSON is the host_execution_plan from the assessment's pbs.yaml (canonical list form)
// expressed with the JSON tags of config.HookExecutionPlan.
const providedPlanJSON = `{
  "endpoints": {
    "/openrtb2/auction": {
      "stages": {
        "entrypoint":                  {"groups": [{"timeout": 120000, "hook_sequence": [{"module_code": "test_provider.test_tracer", "hook_impl_code": "test_provider_test_tracer"}]}]},
        "processed_auction_request":   {"groups": [{"timeout": 120000, "hook_sequence": [{"module_code": "test_provider.test_tracer", "hook_impl_code": "test_provider_test_tracer"}]}]},
        "bidder_request":              {"groups": [{"timeout": 120000, "hook_sequence": [{"module_code": "test_provider.test_tracer", "hook_impl_code": "test_provider_test_tracer"}]}]},
        "raw_bidder_response":         {"groups": [{"timeout": 120000, "hook_sequence": [{"module_code": "test_provider.test_tracer", "hook_impl_code": "test_provider_test_tracer"}]}]},
        "all_processed_bid_responses": {"groups": [{"timeout": 120000, "hook_sequence": [{"module_code": "test_provider.test_tracer", "hook_impl_code": "test_provider_test_tracer"}]}]},
        "auction_response":            {"groups": [{"timeout": 120000, "hook_sequence": [{"module_code": "test_provider.test_tracer", "hook_impl_code": "test_provider_test_tracer"}]}]},
        "exitpoint":                   {"groups": [{"timeout": 120000, "hook_sequence": [{"module_code": "test_provider.test_tracer", "hook_impl_code": "test_provider_test_tracer"}]}]}
      }
    }
  }
}`

// newExecutor builds the real PBS hook stack (repository → plan builder → executor) around the module,
// exactly as router.go / auction.go do, but without an HTTP server.
func newExecutor(t *testing.T, m *Module) hookexecution.HookStageExecutor {
	t.Helper()
	var plan config.HookExecutionPlan
	require.NoError(t, json.Unmarshal([]byte(providedPlanJSON), &plan))

	repo, err := hooks.NewHookRepository(map[string]interface{}{ModuleCode: m})
	require.NoError(t, err)

	planBuilder := hooks.NewExecutionPlanBuilder(config.Hooks{Enabled: true, HostExecutionPlan: plan}, repo)
	return hookexecution.NewHookExecutor(planBuilder, hookexecution.EndpointAuction, &metricsConfig.NilMetricsEngine{})
}

// driveAuction replays the stage order of endpoints/openrtb2/auction.go and exchange/bidder.go.
func driveAuction(t *testing.T, ex hookexecution.HookStageExecutor, accountID string, body []byte, bidders []string, final *openrtb2.BidResponse) {
	t.Helper()
	newBody, reject := ex.ExecuteEntrypointStage(entrypointPayload(body).Request, body)
	require.Nil(t, reject)
	require.Equal(t, body, newBody, "module must not change the body")

	ex.SetAccount(&config.Account{ID: accountID})

	wrapper := requestWrapperFrom(t, body)
	require.NoError(t, ex.ExecuteProcessedAuctionStage(wrapper))

	for _, b := range bidders {
		require.Nil(t, ex.ExecuteBidderRequestStage(requestWrapperFrom(t, body), b))
		require.Nil(t, ex.ExecuteRawBidderResponseStage(sampleBidderResponse(b, "bid-"+b, 1.5), b))
	}
	ex.ExecuteAllProcessedBidResponsesStage(nil)
	ex.ExecuteAuctionResponseStage(final)
	got := ex.ExecuteExitpointStage(final, httptest.NewRecorder())
	require.Same(t, final, got, "module must return the response untouched")
}

func assertOutcomesClean(t *testing.T, ex hookexecution.HookStageExecutor, wantStages int) {
	t.Helper()
	outcomes := ex.GetOutcomes()
	require.Len(t, outcomes, wantStages)
	for _, stage := range outcomes {
		require.NotEmpty(t, stage.Groups, "stage %s has no groups → module hook not found in the plan", stage.Stage)
		for _, g := range stage.Groups {
			require.NotEmpty(t, g.InvocationResults)
			for _, r := range g.InvocationResults {
				assert.Equalf(t, ModuleCode, r.HookID.ModuleCode, "stage %s", stage.Stage)
				assert.Equalf(t, hookexecution.StatusSuccess, r.Status, "stage %s: %v", stage.Stage, r.Errors)
				assert.Emptyf(t, r.Errors, "stage %s", stage.Stage)
				assert.Equalf(t, hookexecution.ActionNone, r.Action, "stage %s must not mutate", stage.Stage)
			}
		}
	}
}

// M-32. FR-01, FR-03..FR-08, FR-15 (through the real executor)
func TestIntegration_SampleRequestIsTracedEndToEnd(t *testing.T) {
	clock := newFakeClock(testStart)
	m, out := newTestModule(t, testRules(), clock)
	ex := newExecutor(t, m)
	body := loadSampleRequest(t)
	bidders := []string{"aceex", "appnexus", "amx", "adyoulike"}

	driveAuction(t, ex, sampleRequestAccountID, body, bidders, sampleBidResponse("5d394bed0104ca857c702982fe8d95e408820eb2-3", "appnexus"))

	// 1 entrypoint + 1 processed + 4 bidder_request + 4 raw_bidder_response + 1 all + 1 auction_response + 1 exitpoint
	assertOutcomesClean(t, ex, 13)

	packets := out.Packets(t)
	require.Len(t, packets, 1)
	p := packets[0]
	assert.Equal(t, sampleRequestAccountID, p.PartnerID)
	assert.Equal(t, 1, p.PacketIndex)
	require.NotNil(t, p.IncomingRequest)
	assert.JSONEq(t, string(body), string(p.IncomingRequest.Body))
	assert.Len(t, p.BidderRequests, len(bidders))
	assert.Len(t, p.BidderResponses, len(bidders))
	require.NotNil(t, p.FinalResponse)
	assert.Contains(t, string(p.FinalResponse.Body), `"seat":"appnexus"`)
}

// M-25. FR-10 / FR-13
func TestIntegration_SecondAuctionBeyondLimitProducesNoOutput(t *testing.T) {
	m, out := newTestModule(t, []Rule{{PartnerID: sampleRequestAccountID, Duration: time.Hour, TracePacketsAmount: 1}}, newFakeClock(testStart))
	body := loadSampleRequest(t)

	driveAuction(t, newExecutor(t, m), sampleRequestAccountID, body, []string{"appnexus"}, sampleBidResponse("r1", "appnexus"))
	second := newExecutor(t, m)
	driveAuction(t, second, sampleRequestAccountID, body, []string{"appnexus"}, sampleBidResponse("r2", "appnexus"))

	assertOutcomesClean(t, second, 7)
	assert.Len(t, out.Lines(), 1)
}

// M-24. FR-12: a trace that started inside the window is completed even if the partner is stopped meanwhile.
func TestIntegration_InFlightTraceCompletesAfterPartnerStopped(t *testing.T) {
	clock := newFakeClock(testStart)
	m, out := newTestModule(t, []Rule{{PartnerID: sampleRequestAccountID, Duration: time.Second, TracePacketsAmount: 10}}, clock)
	body := loadSampleRequest(t)
	ex := newExecutor(t, m)

	_, reject := ex.ExecuteEntrypointStage(entrypointPayload(body).Request, body)
	require.Nil(t, reject)
	ex.SetAccount(&config.Account{ID: sampleRequestAccountID})
	require.NoError(t, ex.ExecuteProcessedAuctionStage(requestWrapperFrom(t, body)))

	clock.Advance(5 * time.Second) // window expires while bidders are still being called
	_, ok := m.tracer.Begin(sampleRequestAccountID, "another")
	require.False(t, ok, "partner must be stopped by now")

	require.Nil(t, ex.ExecuteBidderRequestStage(requestWrapperFrom(t, body), "appnexus"))
	require.Nil(t, ex.ExecuteRawBidderResponseStage(sampleBidderResponse("appnexus", "b", 1), "appnexus"))
	final := sampleBidResponse("late", "appnexus")
	ex.ExecuteAuctionResponseStage(final)
	ex.ExecuteExitpointStage(final, httptest.NewRecorder())

	packets := out.Packets(t)
	require.Len(t, packets, 1)
	assert.Len(t, packets[0].BidderRequests, 1)
	assert.Len(t, packets[0].BidderResponses, 1)
	assert.NotNil(t, packets[0].FinalResponse)
}

// M-27, M-28. FR-15 AC2: hook outcomes never carry errors, even for non-traced requests.
func TestIntegration_OutcomesHaveNoErrors(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	ex := newExecutor(t, m)

	driveAuction(t, ex, "not-a-partner", loadSampleRequest(t), []string{"appnexus"}, sampleBidResponse("r"))

	assertOutcomesClean(t, ex, 7)
	assert.Empty(t, out.Lines())
}
