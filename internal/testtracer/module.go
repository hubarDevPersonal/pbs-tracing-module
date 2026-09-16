// Package testtracer implements the "test_provider.test_tracer" Prebid Server module.
//
// The module traces auctions on the /openrtb2/auction endpoint for partners that match a
// hardcoded rule set and prints one JSON object per traced auction to stdout.
// See docs/02-specification.md and docs/03-design.md in the assessment repository.
//
// The module never rejects requests and never mutates payloads; it only observes (FR-15).
package testtracer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/logger"
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
	if now == nil {
		now = time.Now
	}
	if emitter == nil {
		emitter = newJSONEmitter(os.Stdout)
	}
	tracer, err := newTracer(rules, now)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid tracing rules: %w", ModuleCode, err)
	}
	return &Module{tracer: tracer, emitter: emitter, now: now}, nil
}

// isAuction implements the endpoint scope (FR-14).
func isAuction(miCtx hookstage.ModuleInvocationContext) bool {
	return miCtx.Endpoint == auctionEndpoint
}

// traceFrom returns the active trace of the current request, or nil. Safe on a nil context.
func traceFrom(mc *hookstage.ModuleContext) *AuctionTrace {
	if mc == nil {
		return nil
	}
	v, ok := mc.Get(ctxKeyTrace)
	if !ok || v == nil {
		return nil
	}
	trace, _ := v.(*AuctionTrace)
	return trace
}

func warnf(format string, args ...any) {
	logger.Warnf("["+ModuleCode+"] "+format, args...)
}

// HandleEntrypointHook captures the raw incoming request body and its timestamp (FR-04).
func (m *Module) HandleEntrypointHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	payload hookstage.EntrypointPayload,
) (hookstage.HookResult[hookstage.EntrypointPayload], error) {
	result := hookstage.HookResult[hookstage.EntrypointPayload]{ModuleContext: miCtx.ModuleContext}
	if !isAuction(miCtx) {
		return result, nil
	}
	// The account is unknown at this stage (design §3); stash the raw body and decide later.
	mc := miCtx.ModuleContext
	if mc == nil {
		mc = hookstage.NewModuleContext()
	}
	mc.Set(ctxKeyEntrypoint, entrypointCapture{at: m.now(), body: bytes.Clone(payload.Body)})
	result.ModuleContext = mc
	return result, nil
}

// HandleProcessedAuctionHook takes the trigger decision using miCtx.AccountID (FR-03) and starts the trace.
func (m *Module) HandleProcessedAuctionHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	payload hookstage.ProcessedAuctionRequestPayload,
) (hookstage.HookResult[hookstage.ProcessedAuctionRequestPayload], error) {
	result := hookstage.HookResult[hookstage.ProcessedAuctionRequestPayload]{ModuleContext: miCtx.ModuleContext}
	if !isAuction(miCtx) || payload.Request == nil || payload.Request.BidRequest == nil {
		return result, nil
	}

	trace, ok := m.tracer.Begin(miCtx.AccountID, payload.Request.ID)
	if !ok {
		return result, nil
	}

	mc := miCtx.ModuleContext
	if mc == nil { // plan without an entrypoint stage
		mc = hookstage.NewModuleContext()
	}
	if v, found := mc.Get(ctxKeyEntrypoint); found {
		if capture, ok := v.(entrypointCapture); ok && capture.body != nil {
			trace.SetIncomingRequest(capture.at, capture.body) // FR-04 AC1
		}
		mc.Set(ctxKeyEntrypoint, nil)
	}
	if !trace.hasIncomingRequest() { // FR-04 AC3
		if body, err := json.Marshal(payload.Request.BidRequest); err != nil {
			warnf("auction %s: marshal processed request: %v", trace.AuctionID(), err)
		} else {
			trace.SetIncomingRequest(m.now(), body)
		}
	}
	mc.Set(ctxKeyTrace, trace)
	result.ModuleContext = mc
	return result, nil
}

// HandleBidderRequestHook records the outgoing request for one bidder (FR-05).
func (m *Module) HandleBidderRequestHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	payload hookstage.BidderRequestPayload,
) (hookstage.HookResult[hookstage.BidderRequestPayload], error) {
	result := hookstage.HookResult[hookstage.BidderRequestPayload]{ModuleContext: miCtx.ModuleContext}
	if !isAuction(miCtx) {
		return result, nil
	}
	trace := traceFrom(miCtx.ModuleContext)
	if trace == nil {
		return result, nil
	}
	if payload.Request == nil || payload.Request.BidRequest == nil {
		warnf("auction %s: bidder_request for %q has no request", trace.AuctionID(), payload.Bidder)
		return result, nil
	}
	if err := trace.AddBidderRequest(m.now(), payload.Bidder, payload.Request.BidRequest); err != nil {
		warnf("auction %s: %v", trace.AuctionID(), err)
	}
	return result, nil
}

// HandleRawBidderResponseHook records the response received from one bidder (FR-06).
func (m *Module) HandleRawBidderResponseHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	payload hookstage.RawBidderResponsePayload,
) (hookstage.HookResult[hookstage.RawBidderResponsePayload], error) {
	result := hookstage.HookResult[hookstage.RawBidderResponsePayload]{ModuleContext: miCtx.ModuleContext}
	if !isAuction(miCtx) {
		return result, nil
	}
	trace := traceFrom(miCtx.ModuleContext)
	if trace == nil {
		return result, nil
	}
	if payload.BidderResponse == nil {
		warnf("auction %s: raw_bidder_response for %q has no response", trace.AuctionID(), payload.Bidder)
		return result, nil
	}
	if err := trace.AddBidderResponse(m.now(), payload.Bidder, payload.BidderResponse); err != nil {
		warnf("auction %s: %v", trace.AuctionID(), err)
	}
	return result, nil
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
	payload hookstage.AuctionResponsePayload,
) (hookstage.HookResult[hookstage.AuctionResponsePayload], error) {
	result := hookstage.HookResult[hookstage.AuctionResponsePayload]{ModuleContext: miCtx.ModuleContext}
	if !isAuction(miCtx) {
		return result, nil
	}
	trace := traceFrom(miCtx.ModuleContext)
	if trace == nil || payload.BidResponse == nil {
		return result, nil
	}
	if err := trace.SetFinalResponse(m.now(), payload.BidResponse); err != nil {
		warnf("auction %s: %v", trace.AuctionID(), err)
	}
	return result, nil
}

// HandleExitpointHook refines the final response with the exact object being sent (FR-07 AC2) and emits the packet (FR-08).
func (m *Module) HandleExitpointHook(
	_ context.Context,
	miCtx hookstage.ModuleInvocationContext,
	payload hookstage.ExitpointPayload,
) (hookstage.HookResult[hookstage.ExitpointPayload], error) {
	result := hookstage.HookResult[hookstage.ExitpointPayload]{ModuleContext: miCtx.ModuleContext}
	if !isAuction(miCtx) {
		return result, nil
	}
	trace := traceFrom(miCtx.ModuleContext)
	if trace == nil {
		return result, nil
	}
	// The exitpoint payload is the very object PBS encodes to the client (FR-07 AC2).
	if resp, ok := payload.Response.(*openrtb2.BidResponse); ok && resp != nil {
		if err := trace.SetFinalResponse(m.now(), resp); err != nil {
			warnf("auction %s: %v", trace.AuctionID(), err)
		}
	}
	if !trace.tryMarkEmitted() { // FR-08 AC3
		return result, nil
	}
	if err := m.emitter.Emit(trace.Packet(m.now())); err != nil {
		warnf("auction %s: %v", trace.AuctionID(), err)
	}
	miCtx.ModuleContext.Set(ctxKeyTrace, nil) // NFR-02: release the per-request state
	return result, nil
}
