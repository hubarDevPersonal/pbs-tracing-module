# 02 — Specification

Module id: `test_provider.test_tracer`. Go package: `modules/test_provider/test_tracer` (package `testtracer`).
Scope: Prebid Server `/openrtb2/auction` endpoint only. Terminology follows [01-analysis.md](01-analysis.md) §4.

## 1. Definitions

| Term | Meaning |
|------|---------|
| Partner | The value of `Account.ID` as resolved by PBS for a request. Maps 1:1 to `PartnerID` in a rule. |
| Rule | Hardcoded triple `{PartnerID string, Duration time.Duration, TracePacketsAmount int}`. |
| Trace (packet) | All data collected for one auction (one HTTP request to `/openrtb2/auction`): items 1–4 below. One packet is printed as one JSON object. |
| Traced request | An auction for which a trace was started. |
| Trace window | Per-partner interval starting at the timestamp of the partner's first traced request. |

## 2. Functional requirements

Each requirement has acceptance criteria (AC). Test IDs are assigned in [04-test-plan.md](04-test-plan.md).

### FR-01 Module identity and registration
The module is registered as vendor `test_provider`, module `test_tracer`, and is built by `Builder(json.RawMessage, moduledeps.ModuleDeps)`.
- AC1: `Builder` returns a value implementing `hookstage.Entrypoint`, `ProcessedAuctionRequest`, `BidderRequest`, `RawBidderResponse`, `AllProcessedBidResponses`, `AuctionResponse`, `Exitpoint`.
- AC2: With the provided `pbs.yaml`, PBS starts without `Not found hook while building hook execution plan: test_provider.test_tracer` warnings.
- AC3: `Builder` ignores its configuration payload except for the `enabled` flag handled by PBS itself; no module-level config is required.

### FR-02 Hardcoded rules
Rules are declared in source (`rules.go`) as a slice of rule objects. At least one rule has `PartnerID == "664-025-677-881"` so the provided sample request triggers tracing.
- AC1: `Builder` fails (returns error → PBS refuses to start) if any rule has empty `PartnerID`, `Duration <= 0`, `TracePacketsAmount <= 0`, or a `PartnerID` that appears in more than one rule.
- AC2: An empty rule set is valid and makes the module a no-op.

### FR-03 Trigger condition
A trace starts for a request iff, at `processed_auction_request`, `miCtx.AccountID` equals a rule's `PartnerID` (exact, case-sensitive string comparison) **and** that partner is not stopped (FR-09..FR-11).
- AC1: Request with `Account.ID = "664-025-677-881"` → traced; `"33415-10498"` (publisher id) → not traced unless a rule lists it.
- AC2: The decision is taken once per request; later stages only check whether a trace object exists in the module context.
- AC3: Requests rejected or failed before `processed_auction_request` are never traced.

### FR-04 Incoming BidRequest (item 1)
- AC1: The trace contains `incoming_request.body` = the request body **as received by PBS at `entrypoint`** (post-gzip, before stored-request merge), and `incoming_request.timestamp` = time of the `entrypoint` invocation.
- AC2: The body is copied; later mutations of the payload slice do not alter the trace.
- AC3: If the entrypoint capture is unavailable (stage absent from the plan or context lost), the module falls back to the marshalled processed request and the `processed_auction_request` timestamp.

### FR-05 Outgoing bidder requests (item 2)
For every `bidder_request` invocation of a traced request:
- AC1: Append `{timestamp, bidder, request}` where `request` is the JSON of `payload.Request.BidRequest` **at hook time** (snapshot) and `bidder` is `payload.Bidder`.
- AC2: Order of entries equals invocation order as observed by the module (concurrent stages ⇒ order is not guaranteed across bidders and is not asserted).
- AC3: Non-traced requests produce no entry and no allocation beyond a context lookup.

### FR-06 Incoming bidder responses (item 3)
For every `raw_bidder_response` invocation of a traced request:
- AC1: Append `{timestamp, bidder, response}` where `response` is a snapshot of `payload.BidderResponse` in the DTO of §5.
- AC2: A bidder that PBS never calls `raw_bidder_response` for (HTTP 204, adapter error, timeout) has no entry; the packet is still printed.

### FR-07 Final auction response (item 4)
- AC1: At `auction_response`, record `{timestamp, body}` from `payload.BidResponse`.
- AC2: At `exitpoint`, if `payload.Response` is an `*openrtb2.BidResponse`, the recorded final response is replaced by it (timestamp = exitpoint time). Otherwise the `auction_response` capture is kept.
- AC3: `final_response.body` is the JSON of the object; PBS debug/trace data under `ext` is included as-is.

### FR-08 Output
- AC1: At `exitpoint` of a traced request the module writes exactly one JSON object (§5) followed by `\n` to stdout, in a single `Write` call.
- AC2: Output is valid UTF-8 JSON; embedded requests/responses are JSON values, not escaped strings.
- AC3: A second `exitpoint` invocation for the same request does not print again.
- AC4: Nothing is printed for non-traced requests.
- AC5: Timestamps are RFC 3339 with nanosecond precision in UTC (Go `time.RFC3339Nano`).

### FR-09 Stop condition — time limit
Let `first` be the timestamp of the partner's first traced request (the `processed_auction_request` time of that request). A new trace is refused when `now - first > Duration`.
- AC1: `now - first == Duration` still traces; `Duration + 1ns` does not.
- AC2: The partner is marked stopped with reason `duration_exceeded`.

### FR-10 Stop condition — amount limit
A new trace is refused when the number of traces **started** for the partner equals `TracePacketsAmount`.
- AC1: With `TracePacketsAmount = 2`, the 1st and 2nd matching requests are traced, the 3rd is not.
- AC2: Under concurrent requests the number of started traces never exceeds `TracePacketsAmount` (slot reservation at start).
- AC3: The partner is marked stopped with reason `amount_reached`.

### FR-11 Whichever occurs first; no re-arm
- AC1: Once stopped for either reason the partner is never traced again during the process lifetime, regardless of clock progress.
- AC2: Partners are independent: stopping one does not affect another.

### FR-12 In-flight traces complete
- AC1: A trace started before a stop condition is fully collected and printed at its `exitpoint`.

### FR-13 Zero side effects for non-traced requests
- AC1: No output, no mutation, no error for requests with no matching rule or a stopped partner.

### FR-14 Endpoint scope
- AC1: For `miCtx.Endpoint != "/openrtb2/auction"` every hook is a no-op even if the plan invokes it.

### FR-15 Never interfere with the auction
- AC1: Every `HookResult` has `Reject == false`, `NbrCode == 0`, and an empty `ChangeSet`.
- AC2: Hooks return `nil` error on internal problems (e.g., marshal failure) and log the problem to stderr instead; a failing hook must never fail the auction.
- AC3: Hooks never panic on nil `ModuleContext`, nil payload fields, or unexpected payload types.

### FR-16 Concurrency safety
- AC1: Module-level partner state and per-request trace state are safe under concurrent hook invocations (`go test -race`).
- AC2: Concurrent `exitpoint` writes from different requests never interleave within a line.

## 3. Non-functional requirements

| ID | Requirement |
|----|-------------|
| NFR-01 | Hook latency: O(size of payload) marshalling only; no network or disk I/O; the only blocking call is the stdout write at `exitpoint`. |
| NFR-02 | Memory: module-level state bounded by number of rules; per-request state released after `exitpoint` (context key cleared). |
| NFR-03 | Compatibility: builds with the PBS module's Go version (1.25) and the v4 module path; no new third-party dependencies. |
| NFR-04 | Code quality: `gofmt`, `go vet` clean; unit tests in the same package; concurrency tests named `TestRace*` per PBS `docs/developers/automated-tests.md`. |
| NFR-05 | Testability: clock (`func() time.Time`) and output writer (`io.Writer`) are injectable; production wiring uses `time.Now` and `os.Stdout`. |
| NFR-06 | Observability: internal errors are logged via PBS `logger` (glog → stderr) with the module code prefix; no logging on the happy path other than the trace itself. |

## 4. Rule and state model

```text
Rule           { PartnerID, Duration, TracePacketsAmount }              // immutable, hardcoded
PartnerState   { firstTracedAt time.Time, packets int, stopReason }     // per PartnerID, module-global, mutex-guarded

Begin(partnerID, now):
  rule, ok := rules[partnerID];            if !ok            → not traced
  st := state[partnerID] (create on first use)
  if st.stopReason != ""                                     → not traced
  if st.packets == 0: st.firstTracedAt = now
  elif now - st.firstTracedAt > rule.Duration: st.stopReason = duration_exceeded → not traced
  if st.packets >= rule.TracePacketsAmount: st.stopReason = amount_reached      → not traced
  st.packets++                                               // slot reserved
  if st.packets == rule.TracePacketsAmount: st.stopReason = amount_reached      // further requests refused
  → traced, packetIndex = st.packets
```

State diagram per partner: `idle → tracing → stopped(duration_exceeded | amount_reached)`; `stopped` is terminal.

## 5. Trace JSON contract

One object per line. Field order as listed. `omitempty` only where marked.

```jsonc
{
  "module": "test_provider.test_tracer",
  "partner_id": "664-025-677-881",
  "rule": { "partner_id": "664-025-677-881", "duration": "10m0s", "trace_packets_amount": 3 },
  "packet_index": 1,                               // 1-based, per partner
  "auction_id": "5d394bed0104ca857c702982fe8d95e408820eb2-3",   // BidRequest.id
  "started_at":   "2026-09-16T10:56:54.603016Z",   // processed_auction_request time (trace start)
  "completed_at": "2026-09-16T10:56:54.970112Z",   // exitpoint time
  "incoming_request": { "timestamp": "…", "body": { /* raw BidRequest JSON */ } },
  "bidder_requests": [
    { "timestamp": "…", "bidder": "appnexus", "request": { /* BidRequest sent to bidder */ } }
  ],
  "bidder_responses": [
    { "timestamp": "…", "bidder": "appnexus",
      "response": { "currency": "USD",
                    "bids": [ { "bid": { /* openrtb2.Bid */ }, "bid_type": "banner",
                                "bid_meta": { /* omitempty */ }, "bid_video": { /* omitempty */ },
                                "deal_priority": 0, "seat": "appnexus" } ],
                    "fledge_auction_configs": [ /* omitempty */ ] } }
  ],
  "final_response": { "timestamp": "…", "body": { /* openrtb2.BidResponse as sent */ } }
}
```

Rules: `bidder_requests` and `bidder_responses` are always arrays (empty `[]`, never `null`). `incoming_request` and `final_response`
are objects; `final_response` is `null` only in the degenerate case where neither `auction_response` nor a typed `exitpoint` payload was seen.

## 6. Out of scope

- `/openrtb2/amp`, `/openrtb2/video`, and any non-auction endpoint.
- Runtime configuration of rules (account config, YAML), persistence across restarts, re-arming.
- Truncation, sampling, redaction of PII in traces.
- Tracing of `all_processed_bid_responses` content (stage is implemented as a pass-through only).
