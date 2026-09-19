package testtracer

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
)

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
	// Once every partner is stopped no request can be traced, so the copy is skipped (NFR-01).
	if m.tracer.Exhausted() {
		return result, nil
	}
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

	mc := miCtx.ModuleContext
	if mc == nil { // plan without an entrypoint stage
		mc = hookstage.NewModuleContext()
	}
	capture, _ := mc.Get(ctxKeyEntrypoint)
	mc.Set(ctxKeyEntrypoint, nil) // the copy is not needed past this point, traced or not (NFR-02)
	incoming, _ := capture.(entrypointCapture)

	trace, ok := m.tracer.Begin(miCtx.AccountID, payload.Request.ID, incoming.at)
	if !ok {
		return result, nil
	}
	if incoming.body != nil {
		trace.SetIncomingRequest(incoming.at, incoming.body) // FR-04 AC1
	}
	if !trace.hasIncomingRequest() { // FR-04 AC3: the processed request stands in, stamped with the trace start
		if body, err := json.Marshal(payload.Request.BidRequest); err != nil {
			warnf("auction %s: marshal processed request: %v", trace.AuctionID(), err)
		} else {
			trace.SetIncomingRequest(trace.StartedAt(), body)
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
