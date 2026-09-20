// Package testtracer implements the "test_provider.test_tracer" Prebid Server module.
//
// The module traces auctions on the /openrtb2/auction endpoint for partners that match a
// hardcoded rule set and prints one JSON object per traced auction to stdout.
// See workspace/02-specification.md and workspace/03-design.md in the assessment repository.
//
// The module never rejects requests and never mutates payloads; it only observes (FR-15).
package testtracer

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"time"

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
// rules are hardcoded in rules_default.go by assessment requirement. Output goes to stdout through a bounded
// queue and one writer goroutine, so the hooks never wait for stdout (design §5).
func Builder(_ json.RawMessage, _ moduledeps.ModuleDeps) (interface{}, error) {
	return newModule(defaultRules, newAsyncEmitter(newJSONEmitter(os.Stdout), defaultQueueSize), time.Now)
}

// Shutdown implements modules.Shutdowner (called by PBS on graceful shutdown): it drains the output
// queue so packets of auctions completed just before the stop are not lost. The interface is not
// asserted at compile time because importing package modules from a module is an import cycle in the
// PBS tree.
func (m *Module) Shutdown() error {
	if c, ok := m.emitter.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
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

// recoverHook turns a panic inside a hook into a logged warning and a successful, empty result
// (FR-15 AC3). PBS recovers hook panics itself, but its executor then waits for the whole group
// timeout before answering the client; with the provided configuration that is two minutes.
func recoverHook(err *error) {
	if r := recover(); r != nil {
		warnf("hook panic recovered: %v\n%s", r, debug.Stack())
		*err = nil
	}
}

func warnf(format string, args ...any) {
	logger.Warnf("["+ModuleCode+"] "+format, args...)
}
