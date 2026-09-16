# 03 — Technical Design

Implements [02-specification.md](02-specification.md). PBS facts from [01-analysis.md](01-analysis.md) §3.

## 1. Package layout (drop-in to the PBS tree)

```text
modules/test_provider/test_tracer/
├── module.go            # Builder, Module, the seven hook handlers, module-context keys
├── rules.go             # Rule type, hardcoded defaultRules, validateRules
├── tracer.go            # Tracer (per-partner state, stop conditions), AuctionTrace (per-request collector)
├── output.go            # TracePacket DTOs, Emitter interface, jsonEmitter (NDJSON to io.Writer)
├── README.md            # PBS-style module documentation
├── testdata/bid_request.json
├── *_test.go            # unit, integration, race tests (same package)
```

Package name `testtracer` (Go style: no underscores). Directory names are dictated by PBS's generator regex `^([^/]+)/([^/]+)/module.go$`.

## 2. Types and responsibilities

```go
// rules.go
type Rule struct {
    PartnerID          string
    Duration           time.Duration
    TracePacketsAmount int
}
var defaultRules = []Rule{
    {PartnerID: "664-025-677-881", Duration: 10 * time.Minute, TracePacketsAmount: 3}, // sample request
    {PartnerID: "33415-10498",     Duration: 30 * time.Second, TracePacketsAmount: 1}, // publisher id, demo of a second partner
}
func validateRules(rules []Rule) error            // FR-02

// tracer.go
type StopReason string                              // "", "duration_exceeded", "amount_reached"
type Tracer struct {                                // module-global, one per Module
    mu       sync.Mutex
    rules    map[string]Rule                        // PartnerID → Rule
    partners map[string]*partnerState               // PartnerID → state, created lazily
    now      func() time.Time
}
type partnerState struct { firstTracedAt time.Time; packets int; stopReason StopReason }
type PartnerStatus struct { Packets int; FirstTracedAt time.Time; StopReason StopReason }   // read-only view for tests/ops
func newTracer(rules []Rule, now func() time.Time) (*Tracer, error)
func (t *Tracer) Begin(partnerID, auctionID string) (*AuctionTrace, bool)   // FR-03, FR-09..FR-11, D11
func (t *Tracer) Status(partnerID string) (PartnerStatus, bool)

type AuctionTrace struct {                          // per traced request, shared by concurrent bidder hooks
    mu              sync.Mutex
    partnerID       string
    rule            Rule
    packetIndex     int
    auctionID       string
    startedAt       time.Time
    incoming        *RequestPacket
    bidderRequests  []BidderRequestPacket
    bidderResponses []BidderResponsePacket
    final           *ResponsePacket
    emitted         bool
}
func (a *AuctionTrace) SetIncomingRequest(at time.Time, body []byte)                                   // FR-04
func (a *AuctionTrace) AddBidderRequest(at time.Time, bidder string, req *openrtb2.BidRequest) error   // FR-05
func (a *AuctionTrace) AddBidderResponse(at time.Time, bidder string, resp *adapters.BidderResponse) error // FR-06
func (a *AuctionTrace) SetFinalResponse(at time.Time, resp *openrtb2.BidResponse) error                // FR-07
func (a *AuctionTrace) Packet(completedAt time.Time) TracePacket                                        // snapshot for output

// output.go
type TracePacket struct { ... }                     // §5 of the specification, JSON tags snake_case
type Emitter interface { Emit(TracePacket) error }
type jsonEmitter struct { mu sync.Mutex; w io.Writer }
func newJSONEmitter(w io.Writer) *jsonEmitter       // marshal → append '\n' → single Write under mutex (FR-08, FR-16)

// module.go
const (
    ModuleCode       = "test_provider.test_tracer"
    auctionEndpoint  = "/openrtb2/auction"          // == hookexecution.EndpointAuction; literal avoids importing hookexecution
    ctxKeyEntrypoint = "test_tracer.entrypoint"     // value: entrypointCapture
    ctxKeyTrace      = "test_tracer.trace"          // value: *AuctionTrace
)
type entrypointCapture struct { at time.Time; body []byte }
type Module struct { tracer *Tracer; emitter Emitter; now func() time.Time }
func Builder(_ json.RawMessage, _ moduledeps.ModuleDeps) (interface{}, error)   // newModule(defaultRules, newJSONEmitter(os.Stdout), time.Now)
func newModule(rules []Rule, emitter Emitter, now func() time.Time) (*Module, error)
```

Snapshots: `AddBidderRequest`/`AddBidderResponse`/`SetFinalResponse` marshal **at hook time** with the standard `encoding/json`
(output is identical to PBS's jsoniter for these types and has no dependency on the `RawMessageExtension` registered in `main`) and store `json.RawMessage`. The exchange keeps mutating request/response objects after the hook returns, so storing pointers
would produce non-deterministic traces. `SetIncomingRequest` copies the byte slice for the same reason.

### 2.1 Note on the `ModuleContext` API

The official Go module guide still documents `hookstage.ModuleContext` as a plain map and advises modules that touch the parallel
stages to create a thread-safe value (`*sync.Map`) in an `Entrypoint` hook. In the v4 source the type is a `sync.RWMutex`-guarded
struct with `Get`/`Set`, so the map literal from the docs no longer compiles. The design keeps the documented intent — the shared
per-request value is created at `entrypoint` and every field of `AuctionTrace` is guarded by its own mutex — and uses the v4 API.

## 3. Hook-by-hook behaviour

All handlers: check `miCtx.Endpoint == auctionEndpoint` first (FR-14); on mismatch return an empty result with `ModuleContext: miCtx.ModuleContext`.
All handlers return `Reject=false`, no mutations, `nil` error (FR-15). Internal errors → `logger.Warningf("[%s] ...", ModuleCode, ...)`.

| Stage | Behaviour |
|-------|-----------|
| `entrypoint` | `mc := miCtx.ModuleContext; if mc == nil { mc = hookstage.NewModuleContext() }`; `mc.Set(ctxKeyEntrypoint, entrypointCapture{at: now(), body: bytes.Clone(payload.Body)})`; return `ModuleContext: mc`. Account is unknown here by design (§3.3 of analysis), so no rule evaluation. |
| `processed_auction_request` | `trace, ok := tracer.Begin(miCtx.AccountID, payload.Request.ID)`; if `!ok` return. Incoming request: use `entrypointCapture` from context if present, else marshal `payload.Request.BidRequest` with `now()`. `mc.Set(ctxKeyTrace, trace)`. If `mc == nil` (plan without entrypoint) create it. |
| `bidder_request` | `trace := traceFrom(mc)`; if nil return. `trace.AddBidderRequest(now(), payload.Bidder, payload.Request.BidRequest)`. |
| `raw_bidder_response` | `trace.AddBidderResponse(now(), payload.Bidder, payload.BidderResponse)`. |
| `all_processed_bid_responses` | pass-through (returns `ModuleContext: mc`). Present only because the plan lists the stage. |
| `auction_response` | `trace.SetFinalResponse(now(), payload.BidResponse)`. |
| `exitpoint` | if `resp, ok := payload.Response.(*openrtb2.BidResponse); ok` → `trace.SetFinalResponse(now(), resp)`. Then `packet := trace.Packet(now())`; `emitter.Emit(packet)` guarded by `trace.emitted` (FR-08 AC3); `mc.Set(ctxKeyTrace, nil)` to release memory (NFR-02). |

`traceFrom(mc)`: `v, ok := mc.Get(ctxKeyTrace); t, _ := v.(*AuctionTrace); return t` — nil-safe for nil `mc` and nil stored value.

## 4. Sequence (traced request, two bidders)

```mermaid
sequenceDiagram
    participant C as Client
    participant PBS as PBS auction endpoint
    participant EX as hook executor
    participant M as test_tracer
    participant T as Tracer (global)
    C->>PBS: POST /openrtb2/auction
    PBS->>EX: entrypoint(body)
    EX->>M: HandleEntrypointHook (AccountID="")
    M-->>EX: ModuleContext{entrypoint: {ts, body}}
    PBS->>PBS: resolve Account.ID, load account
    PBS->>EX: processed_auction_request(req)
    EX->>M: HandleProcessedAuctionHook (AccountID="664-…")
    M->>T: Begin("664-…", req.id)
    T-->>M: *AuctionTrace (packet #n) | refused
    M-->>EX: ModuleContext{trace: *AuctionTrace}
    par per bidder (goroutines)
        PBS->>EX: bidder_request(appnexus)
        EX->>M: AddBidderRequest(ts, "appnexus", snapshot)
        PBS->>EX: raw_bidder_response(appnexus)
        EX->>M: AddBidderResponse(ts, "appnexus", snapshot)
    and
        PBS->>EX: bidder_request(amx) / raw_bidder_response(amx)
    end
    PBS->>EX: all_processed_bid_responses
    PBS->>EX: auction_response(BidResponse)
    EX->>M: SetFinalResponse(ts, resp)
    PBS->>PBS: enrich ext.prebid.modules (debug)
    PBS->>EX: exitpoint(response any, w)
    EX->>M: SetFinalResponse(ts, resp) → Packet → Emit(stdout)
    PBS-->>C: JSON response
```

## 5. Concurrency model

| Shared object | Writers | Protection |
|---------------|---------|------------|
| `Tracer.partners`, `Tracer.rules` | `Begin` from concurrent requests | `Tracer.mu` around the whole decision (check-and-reserve is atomic → FR-10 AC2) |
| `hookstage.ModuleContext` | executor + hooks | PBS's own `RWMutex`; the module only stores pointers |
| `AuctionTrace` fields | concurrent `bidder_request` / `raw_bidder_response` goroutines, then `auction_response`, `exitpoint` | `AuctionTrace.mu`; marshalling happens **outside** the lock, append inside |
| stdout | `exitpoint` of concurrent requests | `jsonEmitter.mu` + single `Write` of the complete line |

No goroutines are created by the module. No channels. No blocking except the `Write`.

## 6. Time

`Module.now` and `Tracer.now` are the same injected function (`time.Now` in production). Every recorded timestamp is `now().UTC()`.
Durations are compared with monotonic-clock-backed `time.Time` values (`Sub`), so wall-clock adjustments do not break the window.

## 7. Registration and configuration

1. Copy the package to `<pbs>/modules/test_provider/test_tracer/`.
2. Run `go generate ./modules/...` (executes `modules/generator/buildergen.go`) to regenerate `modules/builder.go`; it adds
   `"test_provider": {"test_tracer": test_providerTest_tracer.Builder}`.
3. Configuration is already present in the provided `pbs.yaml`:
   `hooks.enabled: true`, `hooks.modules.test_provider.test_tracer.enabled: true`, and the host execution plan for the seven stages.
4. The `hook_impl_code` `test_provider_test_tracer` is opaque to the module.

No account-level configuration is read (`miCtx.AccountConfig` ignored).

## 8. Error handling matrix

| Situation | Behaviour |
|-----------|-----------|
| `payload.Request` / `BidRequest` nil at a traced stage | skip recording, warn to stderr, return success |
| Marshal error | skip that entry, warn, return success |
| `payload.Response` at exitpoint not `*openrtb2.BidResponse` | keep `auction_response` capture (FR-07 AC2) |
| No trace in context at exitpoint | return success, print nothing |
| `Emit` write error | warn to stderr; trace is lost; auction unaffected |
| Rule validation error | `Builder` returns error → PBS fails fast at startup with `failed to init "test_provider.test_tracer" module` |

## 9. Performance notes

- Per traced request: 1 body copy + (1 + bidders×2 + 1) JSON marshals + 1 final marshal. Non-traced requests: one map lookup at
  `processed_auction_request`, one context `Get` per later hook.
- No allocation for non-traced requests beyond the executor's own `HookResult`.
- Trace memory is released at `exitpoint` by clearing the context key; the `ModuleContext` itself is owned by the executor and dies with the request.

## 10. Packaging (Docker)

`Dockerfile` at the repository root builds upstream PBS at a pinned commit (`PBS_REF`, default `f660bedc`) with the module copied
into `modules/test_provider`, regenerates `modules/builder.go`, runs `gofmt`/`go vet`/the module tests inside the build stage, and
produces a release image derived from the upstream `Dockerfile` (ubuntu:22.04, non-root user, `static/` and `stored_requests/data`).
The assessment's `pbs.yaml` is copied unchanged next to the binary, so `docker run -p 8080:8080 pbs-tracer:local` is the whole runbook.
stdout of the container carries only trace packets; PBS logs go to stderr (`docker logs c 2>/dev/null` isolates the trace).

## 11. Alternatives considered

- **Trigger at `entrypoint` by parsing the body for the account id**: rejected (D5) — duplicates PBS resolution rules (app/site/dooh, parentAccount, stored requests).
- **Print at `auction_response` instead of `exitpoint`**: rejected (D7) — `exitpoint` is closer to the wire and both stages always run together.
- **Marshal `adapters.BidderResponse` directly**: rejected (D10) — no JSON tags, PascalCase keys, nested pointers to PBS-internal types.
- **Count events instead of auctions**: rejected (D1); the counter lives in one place (`Tracer.Begin`) if the reviewer prefers otherwise.
