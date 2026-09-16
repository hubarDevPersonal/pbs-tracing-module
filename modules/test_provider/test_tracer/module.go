// Package testtracer implements the "test_provider.test_tracer" Prebid Server module.
//
// The module traces auctions on the /openrtb2/auction endpoint for partners that match a
// hardcoded rule set and prints one JSON object per traced auction to stdout.
// See docs/02-specification.md and docs/03-design.md in the assessment repository.
//
// STATUS: API skeleton for the red phase of TDD. Every hook is a pass-through stub.
// Functions marked TODO(impl) are implemented in the next phase without changing signatures.
package testtracer

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/modules/moduledeps"
)

const (
	// ModuleCode is the id PBS derives from the directory layout modules/test_provider/test_tracer.
	ModuleCode = "test_provider.test_tracer"

	// auctionEndpoint mirrors hookexecution.EndpointAuction; kept as a literal to avoid importing the executor package.
	auctionEndpoint = "/openrtb2/auction"

	// Module-context keys. Values: entrypointCapture and *AuctionTrace respectively.
	ctxKeyEntrypoint = "test_tracer.entrypoint"
	ctxKeyTrace      = "test_tracer.trace"
)

// Compile-time assertions: the module implements every stage listed in the provided pbs.yaml plan.
var (
	_ hookstage.Entrypoint               = (*Module)(nil)
	_ hookstage.ProcessedAuctionRequest  = (*Module)(nil)
	_ hookstage.BidderRequest            = (*Module)(nil)
	_ hookstage.RawBidderResponse        = (*Module)(nil)
	_ hookstage.AllProcessedBidResponses = (*Module)(nil)
	_ hookstage.AuctionResponse          = (*Module)(nil)
	_ hookstage.Exitpoint                = (*Module)(nil)
)

// entrypointCapture is stored in the module context at the entrypoint stage, where the account
// is not yet known, and attached to the trace once the trigger decision is taken.
type entrypointCapture struct {
	at   time.Time
	body []byte
}

// Module holds process-wide tracing state. One instance is created by Builder per PBS process.
type Module struct {
	tracer  *Tracer
	emitter Emitter
	now     func() time.Time
}

// Builder is the PBS entry point (see modules/builder.go). Module-level configuration is not used;
// rules are hardcoded in rules.go by assessment requirement.
func Builder(_ json.RawMessage, _ moduledeps.ModuleDeps) (interface{}, error) {
	return newModule(defaultRules, newJSONEmitter(os.Stdout), time.Now)
}

// newModule wires the module with injectable rules, output and clock (NFR-05).
func newModule(rules []Rule, emitter Emitter, now func() time.Time) (*Module, error) {
	// TODO(impl): validate rules via newTracer and fail fast.
	return &Module{emitter: emitter, now: now}, nil
}

// HandleEntrypointHook captures the raw incoming request body and its timestamp (FR-04).
func (m *Module) HandleEntrypointHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	_ hookstage.EntrypointPayload,
) (hookstage.HookResult[hookstage.EntrypointPayload], error) {
	// TODO(impl)
	return hookstage.HookResult[hookstage.EntrypointPayload]{ModuleContext: miCtx.ModuleContext}, nil
}

// HandleProcessedAuctionHook takes the trigger decision using miCtx.AccountID (FR-03) and starts the trace.
func (m *Module) HandleProcessedAuctionHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	_ hookstage.ProcessedAuctionRequestPayload,
) (hookstage.HookResult[hookstage.ProcessedAuctionRequestPayload], error) {
	// TODO(impl)
	return hookstage.HookResult[hookstage.ProcessedAuctionRequestPayload]{ModuleContext: miCtx.ModuleContext}, nil
}

// HandleBidderRequestHook records the outgoing request for one bidder (FR-05).
func (m *Module) HandleBidderRequestHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	_ hookstage.BidderRequestPayload,
) (hookstage.HookResult[hookstage.BidderRequestPayload], error) {
	// TODO(impl)
	return hookstage.HookResult[hookstage.BidderRequestPayload]{ModuleContext: miCtx.ModuleContext}, nil
}

// HandleRawBidderResponseHook records the response received from one bidder (FR-06).
func (m *Module) HandleRawBidderResponseHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	_ hookstage.RawBidderResponsePayload,
) (hookstage.HookResult[hookstage.RawBidderResponsePayload], error) {
	// TODO(impl)
	return hookstage.HookResult[hookstage.RawBidderResponsePayload]{ModuleContext: miCtx.ModuleContext}, nil
}

// HandleAllProcessedBidResponsesHook is a pass-through; the stage is present in the plan but not traced.
func (m *Module) HandleAllProcessedBidResponsesHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	_ hookstage.AllProcessedBidResponsesPayload,
) (hookstage.HookResult[hookstage.AllProcessedBidResponsesPayload], error) {
	return hookstage.HookResult[hookstage.AllProcessedBidResponsesPayload]{ModuleContext: miCtx.ModuleContext}, nil
}

// HandleAuctionResponseHook records the final response (FR-07 AC1).
func (m *Module) HandleAuctionResponseHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	_ hookstage.AuctionResponsePayload,
) (hookstage.HookResult[hookstage.AuctionResponsePayload], error) {
	// TODO(impl)
	return hookstage.HookResult[hookstage.AuctionResponsePayload]{ModuleContext: miCtx.ModuleContext}, nil
}

// HandleExitpointHook refines the final response with the exact object being sent (FR-07 AC2) and emits the packet (FR-08).
func (m *Module) HandleExitpointHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	_ hookstage.ExitpointPayload,
) (hookstage.HookResult[hookstage.ExitpointPayload], error) {
	// TODO(impl)
	return hookstage.HookResult[hookstage.ExitpointPayload]{ModuleContext: miCtx.ModuleContext}, nil
}
