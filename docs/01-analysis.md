# 01 — Analysis

Date: 2026-09-16. Prebid Server (PBS) reference: `github.com/prebid/prebid-server/v4`, master @ `f660bedc`, `go 1.25`.
Local toolchain: Go 1.26.2 (darwin/arm64), Docker 20.10.

## 1. Task restated

Build a PBS module, code `test_provider.test_tracer`, that for the `/openrtb2/auction` endpoint collects per auction:

1. the incoming BidRequest and its timestamp;
2. every outgoing BidRequest to a bidder, its timestamp and the bidder name;
3. every incoming BidResponse from a bidder, its timestamp and the bidder name;
4. the final auction response returned to the client and its timestamp;

and prints the collected trace as JSON to stdout. Tracing is driven by hardcoded rules `{PartnerID, Duration, TracePacketsAmount}`,
starts when `Account.ID == PartnerID`, and stops per partner when either the time since the first traced request exceeds `Duration`
or the number of collected traces reaches `TracePacketsAmount`, whichever comes first.

## 2. Provided artefacts

| File | Role | Findings |
|------|------|----------|
| `01-bid-request-example.json` | Sample OpenRTB 2.x request | `test: 1`, `ext.prebid.debug: true`, `ext.prebid.trace: "verbose"`. Four bidders in `imp[0].ext.prebid.bidder`: `aceex`, `appnexus`, `amx`, `adyoulike`. Publisher `id = "33415-10498"` and `ext.prebid.parentAccount = "664-025-677-881"`. |
| `02-send-bid-request.sh` | `curl` wrapper | Same payload inline. `POST http://localhost:8080/openrtb2/auction`. |
| `pbs.yaml` | PBS configuration | Hooks enabled; module `test_provider.test_tracer` enabled; host execution plan for `/openrtb2/auction` lists 7 stages: `entrypoint`, `processed_auction_request`, `bidder_request`, `raw_bidder_response`, `all_processed_bid_responses`, `auction_response`, `exitpoint`. `raw_auction_request` is **not** in the plan. Group timeout 120 000 ms. `account_required: false`, `account_defaults.debug_allow: true`. Adapters `appnexus` and `colossus` allow debug. |

### 2.1 Which value is `Account.ID` for the sample request

`endpoints/openrtb2/auction.go` resolves the account from the raw body before parsing, searching `app`, then `site`, then `dooh`.
For each, `publisher.ext.prebid.parentAccount` wins over `publisher.id` (`getAccountID`). The account service (`account/account.go`)
returns a copy of `account_defaults` with `ID = <resolved id>` when the account is not found and `account_required` is false.

Therefore for the sample request `Account.ID == "664-025-677-881"`, not `"33415-10498"`. The hardcoded rule set must contain
`PartnerID: "664-025-677-881"` for the assessment demo to trigger.

### 2.2 `pbs.yaml` uses non-canonical `groups` syntax

`config.HookExecutionPlan` declares `Groups []HookExecutionGroup`, but the provided file writes `groups:` as a mapping, not a list.
Verified live: viper decodes with `WeaklyTypedInput`, so the mapping is coerced into a one-element slice and the resolved-config log
shows `stages[...].groups: <[]config.HookExecutionGroup Value>` for all seven stages. The file works as-is. The canonical list form
(`groups: [ { timeout: ..., hook_sequence: [...] } ]`) is used in the e2e overlay to avoid relying on the coercion.

### 2.3 The `test: 1` premise did not hold on 2026-09-16

The README states `test: 1` forces at least one valid bid from `appnexus`. A live run of the unmodified sample against PBS master
returned HTTP 200 in ~0.37 s with an empty `seatbid`; `ext.debug.httpcalls` shows all four bidders answering **204 No Content**
(`appnexus` → `http://ib.adnxs.com/openrtb2`, status 204). The appnexus adapter returns `nil` from `MakeBids` on 204, and
`exchange/bidder.go` invokes the `raw_bidder_response` stage only when `MakeBids` returns a non-nil response. Consequence:
with live bidders the module would record stage 2 (outgoing requests) but not stage 3 (bidder responses).

Direct calls to `http://ib.adnxs.com/openrtb2` with the exact body PBS sent, and with variants (no IP/geo, no user, minimal
request, other placement ids, `hb_source` 1/2, member + inv_code, `test: 0`) all returned 204 from this network (egress: Portugal).
The test placement may be region-gated or retired; the reviewer's network may behave differently.

E2E stays **live** (decision of the assignment owner: no mocks). Consequences for verification:

- items 1, 2 and 4 are always produced and are asserted strictly;
- item 3 is asserted on shape only and reported as a count; `STRICT_BIDS=1` turns "no live bidder responded" into a failure for networks where appnexus test mode works;
- the unit and integration tests cover item 3 deterministically with in-code `adapters.BidderResponse` values.

### 2.4 Behaviour with the module absent

With the config as provided but no module registered, PBS starts normally and logs, per request, one warning per planned stage:
`Not found hook while building hook execution plan: test_provider.test_tracer test_provider_test_tracer`. This is the signature of a
mis-registered module and is useful as a negative check in the runbook.

### 2.5 stdout is clean

PBS logs through glog to stderr. Nothing else writes to stdout, so newline-delimited JSON on stdout is trivially separable
(`./prebid-server 2>pbs.log 1>trace.ndjson`).

## 3. Facts from the PBS source that constrain the design

### 3.1 Module registration

- A module lives at `modules/<vendor>/<module>/module.go` and exports `Builder(cfg json.RawMessage, deps moduledeps.ModuleDeps) (interface{}, error)`.
- `go generate modules/modules.go` runs `modules/generator/buildergen.go`, which scans that path pattern and regenerates `modules/builder.go`.
  Directory names `test_provider/test_tracer` map to module id `test_provider.test_tracer` and config key `hooks.modules.test_provider.test_tracer`.
- `modules.Build` instantiates a module only if `hooks.modules.<vendor>.<module>.enabled == true`; a module that implements no hook interface is a fatal startup error.
- `hook_impl_code` in the plan is a free label passed to the module as `ModuleInvocationContext.HookImplCode`; it is used in metrics and debug output.
- Optional `Shutdown() error` (`modules.Shutdowner`) is called on server shutdown.

### 3.2 Stage payloads and what each offers

| Stage | Payload (`hooks/hookstage`) | Account available | Per-bidder | Mutable | Rejectable | Relevance |
|-------|-----------------------------|-------------------|------------|---------|------------|-----------|
| `entrypoint` | `{Request *http.Request; Body []byte}` | **No** (`AccountID == ""`) | no | body | yes | Raw incoming request bytes + timestamp (item 1). |
| `raw_auction_request` | `[]byte` | yes | no | body | yes | Not in the provided plan. Not used. |
| `processed_auction_request` | `{Request *openrtb_ext.RequestWrapper}` | yes | no | request | yes | First stage where `AccountID` is known → trigger decision. Fallback source for item 1. |
| `bidder_request` | `{Request *RequestWrapper; Bidder string}` | yes | **yes** | request | yes | Item 2. |
| `raw_bidder_response` | `{BidderResponse *adapters.BidderResponse; Bidder string}` | yes | **yes** | response | yes | Item 3. Skipped for 204 / adapter errors. |
| `all_processed_bid_responses` | `{Responses map[BidderName]*PbsOrtbSeatBid}` | yes | no | no | no | Not required; implemented as no-op because the plan lists it. |
| `auction_response` | `{BidResponse *openrtb2.BidResponse}` | yes | no | no | no | Item 4 (typed). Runs before `ext.prebid.modules` debug enrichment. |
| `exitpoint` | `{Response any; W http.ResponseWriter}` | yes | no | response | no | Item 4 (exact object that is JSON-encoded). Last hook → flush point. |

### 3.3 Execution semantics (`hooks/hookexecution`)

- Every hook of a group runs in its own goroutine with `ctx = context.WithTimeout(context.Background(), groupTimeout)`; a hook that overruns is discarded and recorded as a timeout.
- `ModuleInvocationContext` is rebuilt for every invocation. `Endpoint` is always set. `AccountID` and `AccountConfig` are set only after `SetAccount` (i.e. from `raw_auction_request` on). `ModuleContext` is the pointer the module returned in a previous stage of the **same request**, or `nil` on first use.
- After each stage the executor stores `HookResult.ModuleContext` per module (`moduleContexts.put`); the first stored pointer is kept and later values are merged into it with `SetAll`. Storing a pointer to a per-request struct in the context therefore survives all stages, including concurrent per-bidder stages.
- `hookstage.ModuleContext` is `sync.RWMutex`-guarded; `Get`/`Set` on a nil receiver are safe no-ops.
- A hook error is recorded as a failure outcome; the auction proceeds. `Reject == true` is honoured only on rejectable stages. Payload changes are applied only via `ChangeSet` mutations. A tracing module returns no mutations and never rejects.
- Panics inside hooks are recovered and logged by the executor. The module must still be panic-free.
- The exchange starts one goroutine per bidder (`exchange/exchange.go`); `bidder_request` and `raw_bidder_response` for different bidders run concurrently. A bidder that issues several HTTP calls yields several `raw_bidder_response` invocations.
- `bidder_request` payloads may be a privacy-scrubbed copy of the request when activity controls deny user FPD or precise geo (`handleModuleActivities`). The trace records what PBS exposes to modules.
- `auction_response` and `exitpoint` are both invoked from `sendAuctionResponse`, also on hook rejection paths. Neither runs when PBS fails the request early with 4xx/5xx (`writeError`). `exitpoint` receives the very object passed to the JSON encoder, after `response.ext.prebid.modules` enrichment.
- Hook outcomes appear in `response.ext.prebid.modules` when the request has `ext.prebid.debug: true` and the account allows debug (`trace: "verbose"` adds debug messages). The sample request enables this, which makes hook execution visible in the HTTP response for verification.

## 4. Ambiguities and decisions

| # | Topic | Options | Decision | Rationale |
|---|-------|---------|----------|-----------|
| D1 | What is one "trace packet" counted against `TracePacketsAmount`? | (a) one collected event (any of items 1–4); (b) one auction with all its events | **(b)** one auction = one packet | Counting events would cut an auction mid-way and print partial objects; (b) yields coherent JSON objects and a predictable count. Flagged to reviewer. |
| D2 | Scope of counters | per request / per process | **per partner, process-wide, for the lifetime of the process** | Wording "time elapsed since the first traced BidRequest for that partner" implies cross-request state. |
| D3 | When is `Duration` evaluated and what happens to in-flight traces? | on every hook / at trigger time | **at trigger time only**; in-flight auctions complete and are printed | Simple, deterministic, no torn packets. Boundary: stop when `elapsed > Duration` (strictly "exceeds"). |
| D4 | Re-arming after stop | never / after Duration | **never** | Rules are hardcoded; the task defines stop conditions only. |
| D5 | Where to decide the trigger | `entrypoint` (parse account from body) / `processed_auction_request` | **`processed_auction_request`**, using `miCtx.AccountID` | Reuses PBS's own account resolution (parentAccount, stored requests, defaults). Avoids duplicating logic. |
| D6 | Source of item 1 | raw bytes at `entrypoint` / processed request | **raw entrypoint body + entrypoint timestamp**, attached retroactively when the trace starts; fallback to the processed request if the entrypoint capture is missing | The raw body is what the client sent; the fallback covers plans without `entrypoint`. |
| D7 | Source of item 4 | `auction_response` / `exitpoint` | capture at `auction_response`; **replace with the `exitpoint` payload when it is an `*openrtb2.BidResponse`**; print at `exitpoint` | `exitpoint` is literally what is sent, including debug ext. Both stages always run together. |
| D8 | Output format | pretty JSON / NDJSON | **one JSON object per line (NDJSON)**, UTF-8, `\n`-terminated | Machine-readable, safe under concurrency, no interleaving. |
| D9 | Timestamps | unix ms / RFC 3339 | **RFC 3339 with nanoseconds, UTC** | Human- and machine-readable; unambiguous. |
| D10 | Bidder response representation | marshal `adapters.BidderResponse` | **explicit snake_case DTO** | `adapters.BidderResponse` / `TypedBid` have no JSON tags. |
| D11 | Amount limit under concurrent requests | count on completion / reserve on start | **reserve the slot at trigger time** | Guarantees the count never exceeds `TracePacketsAmount` even with parallel requests. |
| D12 | Endpoint scoping | rely on plan / check in module | **both** — module ignores `miCtx.Endpoint != "/openrtb2/auction"` | Defence in depth; costs one string compare. |
| D13 | Traces whose `exitpoint` never runs (early 4xx/5xx) | print partial / drop | **drop** and log a warning to stderr | Rare; a partial packet has no final response by definition. |

## 5. Risks and open questions for the reviewer

1. D1 (packet = auction) is the main interpretation risk. The counter is isolated in one function so switching to per-event counting is a small change.
2. Trace bodies can be large (full requests and responses). Hardcoded rules bound the total; no truncation is applied.
3. Live bidders return 204 for the sample from this network; the module cannot trace a response that PBS never produces. E2E is live by decision; item 3 is proven at unit/integration level and soft-asserted in e2e.
4. Hook timeouts are generous (120 s) in the provided config; the module still keeps hooks allocation-light and non-blocking except for the stdout write.
5. The provided `groups` mapping relies on viper's weak typing; harmless today, brittle if PBS tightens decoding.
