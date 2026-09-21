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

Each requirement has acceptance criteria (AC). The scenarios that demonstrate them are in [test-specs/](test-specs/).

### FR-01 Module identity and registration
The module is registered as vendor `test_provider`, module `test_tracer`, and is built by `Builder(json.RawMessage, moduledeps.ModuleDeps)`.
- AC1: `Builder` returns a value implementing `hookstage.Entrypoint`, `ProcessedAuctionRequest`, `BidderRequest`, `RawBidderResponse`, `AllProcessedBidResponses`, `AuctionResponse`, `Exitpoint`.
- AC2: With the provided `pbs.yaml`, PBS starts without `Not found hook while building hook execution plan: test_provider.test_tracer` warnings.
- AC3: `Builder` ignores its configuration payload except for the `enabled` flag handled by PBS itself; no module-level config is required.

### FR-02 Hardcoded rules
Rules are declared in source (`rules_default.go`) as a slice of rule objects. At least one rule has `PartnerID == "664-025-677-881"` so the provided sample request triggers tracing.
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
  This is the per-bidder OpenRTB request PBS hands to the adapter, before the adapter's `MakeRequests` may rewrite, split or wrap it
  into HTTP calls; `timestamp` is the hook time, not the moment bytes leave the socket. What the module can observe is bounded by
  the hook stages (§6).
- AC2: Order of entries equals invocation order as observed by the module (concurrent stages ⇒ order is not guaranteed across bidders and is not asserted).
- AC3: Non-traced requests produce no entry and no allocation beyond a context lookup.

### FR-06 Incoming bidder responses (item 3)
For every `raw_bidder_response` invocation of a traced request:
- AC1: Append `{timestamp, bidder, response}` where `response` is a snapshot of `payload.BidderResponse` in the DTO of §5.
  This is the adapter's parsed result (`MakeBids`), i.e. the bids PBS goes on to process, not the HTTP body the bidder returned:
  fields the adapter does not carry over (`id`, `bidid`, `nbr`, response `ext`, …) are not in the trace, and `timestamp` is the
  hook time, after parsing. One adapter HTTP call yields one entry.
- AC2: A bidder that PBS never calls `raw_bidder_response` for (HTTP 204, adapter error, timeout) has no entry; the packet is still printed.

### FR-07 Final auction response (item 4)
- AC1: At `auction_response`, remember `payload.BidResponse` and the invocation time; the body is marshaled once, at `exitpoint`.
- AC2: At `exitpoint`, if `payload.Response` is an `*openrtb2.BidResponse`, the recorded final response is replaced by it (timestamp = exitpoint time). Otherwise the `auction_response` capture is kept.
- AC3: `final_response.body` is the JSON of the object; PBS debug/trace data under `ext` is included as-is.

### FR-08 Output
- AC1: At `exitpoint` of a traced request the module hands exactly one JSON object (§5) to the output; it is written to stdout followed by `\n` in a single `Write` call.
- AC1a: Output is asynchronous. `exitpoint` enqueues the packet and returns without waiting for stdout. The queue is bounded
  (64 packets); when it is full the packet is **dropped**, counted and logged to stderr, and the auction is unaffected. Packets of
  one process are written in the order they were enqueued.
- AC1b: On graceful shutdown of PBS the module drains the queue before returning, so packets of auctions completed just before the
  stop are written. It waits at most 5 s: a stdout that does not drain must not hold PBS's shutdown. Packets still queued when the
  process is killed or when the wait runs out are lost and logged.
- AC2: Output is valid UTF-8 JSON; embedded requests/responses are JSON values, not escaped strings.
- AC3: A second `exitpoint` invocation for the same request does not print again.
- AC4: Nothing is printed for non-traced requests.
- AC5: Timestamps are RFC 3339 with nanosecond precision in UTC (Go `time.RFC3339Nano`).

### FR-09 Stop condition — time limit
Let `first` be the timestamp of the partner's first traced request: the `entrypoint` time of that request, i.e. the
`incoming_request.timestamp` it reports (the `processed_auction_request` time when the entrypoint capture is unavailable). A new
trace for a request that arrived at `arrived` (its own entrypoint time, same fallback) is refused when `arrived - first > Duration`:
the window is measured between request arrivals, the times the trace reports, not between trigger decisions. When several first
auctions of a partner race, the window opens at the incoming timestamp of the one that reserves its slot first.
- AC1: `now - first == Duration` still traces; `Duration + 1ns` does not.
- AC2: The partner is marked stopped with reason `duration_exceeded`.

### FR-10 Stop condition — amount limit
A trace **reserves** one of the partner's `TracePacketsAmount` slots when it starts and **confirms** it when its packet is written
at `exitpoint`. A new trace is refused while every slot is reserved or confirmed.
- AC1: With `TracePacketsAmount = 2`, the 1st and 2nd matching requests are traced, the 3rd is not.
- AC2: Under concurrent requests the number of slots held never exceeds `TracePacketsAmount` (reservation at start, under one lock).
- AC3: The partner is marked stopped with reason `amount_reached` while all slots are held.
- AC4: A reservation that is not confirmed within 5 minutes — PBS failed the auction after the trigger, or the `entrypoint` hook
  timed out and the module lost its context, so `exitpoint` never wrote the packet — is given back: the next matching request
  takes the slot and the partner is no longer stopped for amount. A confirmed slot is never given back. The amount therefore counts collected packets, and an auction that PBS would still be running after 5 minutes is the
  only way to exceed it, by one.

### FR-11 Whichever occurs first; no re-arm
- AC1: Once stopped for duration the partner is never traced again during the process lifetime, regardless of clock progress; once
  stopped for amount it is traced again only through FR-10 AC4, and only while its window is open.
- AC2: Partners are independent: stopping one does not affect another.

### FR-12 In-flight traces complete
- AC1: A trace started before a stop condition is fully collected and printed at its `exitpoint`.
- If PBS fails the auction with 4xx/5xx after the trace started, `exitpoint` is not invoked, nothing is printed, and the slot is
  given back after the lease of FR-10 AC4 (analysis §5.6). The same holds when the `entrypoint` hook timed out: the executor
  then keeps a nil module context for the whole request, so the trace never reaches `exitpoint` (analysis §3.3).

### FR-13 Zero side effects for non-traced requests
- AC1: No output, no mutation, no error for requests with no matching rule or a stopped partner.

### FR-14 Endpoint scope
- AC1: For `miCtx.Endpoint != "/openrtb2/auction"` every hook is a no-op even if the plan invokes it.

### FR-15 Never interfere with the auction
- AC1: Every `HookResult` has `Reject == false`, `NbrCode == 0`, and an empty `ChangeSet`.
- AC2: Hooks return `nil` error on internal problems (e.g., marshal failure) and log the problem to stderr instead; a failing hook must never fail the auction.
- AC3: Hooks never panic on nil `ModuleContext`, nil payload fields, or unexpected payload types; a panic that happens anyway is
  recovered inside the hook and logged, and the hook returns success, so PBS does not wait for the group timeout.

### FR-16 Concurrency safety
- AC1: Module-level partner state and per-request trace state are safe under concurrent hook invocations (`go test -race`).
- AC2: Concurrent `exitpoint` writes from different requests never interleave within a line.

## 3. Non-functional requirements

| ID | Requirement |
|----|-------------|
| NFR-01 | Hook latency: O(size of payload) marshalling only; no network or disk I/O; no hook waits for stdout (FR-08 AC1a). Once no request can be traced any more (every partner stopped for good, or every partner started and every window closed) `entrypoint` no longer copies request bodies. |
| NFR-02 | Memory: module-level state is, per rule, one partner state and at most `TracePacketsAmount` slot reservations of a few dozen bytes each, plus the output queue (FR-08 AC1a); it never holds a trace. The entrypoint body copy is released at `processed_auction_request` for requests that are not traced, and a trace when its request ends — whether its packet was written or PBS abandoned the auction after the trigger. |
| NFR-03 | Compatibility: builds with the PBS module's Go version (1.25) and the v4 module path; no new third-party dependencies. |
| NFR-04 | Code quality: `gofmt`, `go vet` clean; unit tests in the same package; concurrency tests named `TestRace*` per PBS's own `docs/developers/automated-tests.md`. |
| NFR-05 | Testability: clock (`func() time.Time`) and output writer (`io.Writer`) are injectable; production wiring uses `time.Now` and `os.Stdout`. |
| NFR-06 | Observability: internal errors are logged via PBS `logger` (glog → stderr) with the module code prefix; no logging on the happy path other than the trace itself. |
| NFR-07 | Module rules compliance (docs.prebid.org): the module creates no bids, adds nothing to creatives, makes no outbound calls and does not mutate payloads; user data in traces is written only to the local process stdout and is never transmitted. |

## 4. Rule and state model

```text
Rule           { PartnerID, Duration, TracePacketsAmount }              // immutable, hardcoded
PartnerState   { firstTracedAt time.Time, packets int, stopReason, pending []Reservation }   // per PartnerID, mutex-guarded
Reservation    { startedAt time.Time, emitted bool }                    // one slot; the state never holds a trace

Begin(partnerID, incomingAt, now):                                     // incomingAt: entrypoint time of the request
  rule, ok := rules[partnerID];            if !ok            → not traced
  st := state[partnerID] (create on first use)
  if st.stopReason == duration_exceeded                      → not traced
  if st.packets == 0: st.firstTracedAt = incomingAt
  elif incomingAt - st.firstTracedAt > rule.Duration: st.stopReason = duration_exceeded; st.pending = [] → not traced
  if st.packets >= rule.TracePacketsAmount:
    give back reservations older than the lease (now - startedAt > 5 min) that never wrote a packet
    if still st.packets >= rule.TracePacketsAmount: st.stopReason = amount_reached → not traced
  st.packets++; st.pending += reservation{startedAt: now}    // slot reserved; the trace points at it
  if st.packets == rule.TracePacketsAmount: st.stopReason = amount_reached      // further requests refused
  → traced, packetIndex = st.packets

Complete(trace):  st.pending -= trace's reservation          // slot confirmed at exitpoint
```

State diagram per partner: `idle → tracing → stopped(duration_exceeded | amount_reached)`; `duration_exceeded` is terminal,
`amount_reached` returns to `tracing` when a lease expires (FR-10 AC4) and is terminal once every slot is confirmed.

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
- Truncation, sampling, redaction of PII in traces. Items 1 and 4 are written as PBS received and sent them; PBS's activity
  controls scrub only the `processed_auction_request` and `bidder_request` payloads, so user ids, IPs and geo in the raw request
  and in the response reach stdout. Deploy with stdout going to a store with the same access rules as the request logs.
- Tracing of `all_processed_bid_responses` content (stage is implemented as a pass-through only).
- The bytes exchanged with bidders over HTTP, as items 2 and 3. PBS module hooks expose the per-bidder OpenRTB request before the
  adapter builds its HTTP calls and the adapter's parsed result after it read the HTTP response; the HTTP bodies themselves live
  inside the exchange, outside any hook. Capturing them *there* needs a change to PBS core, not a module. Items 2 and 3 are
  therefore the objects the hooks expose (FR-05, FR-06), and the JSON contract names them `request` and `response` of the bidder
  stage, not wire data.

  They do reach the packet by another route, and the provided configuration takes it: when a request carries
  `ext.prebid.debug: true` and the account allows debug — both true for the assessment's sample and `pbs.yaml` — PBS puts the
  wire-level exchange into the response it sends, under `ext.debug.httpcalls`: per bidder the URI, the status and the request and
  response bodies. Item 4 is that response verbatim (FR-07 AC3), so the packet carries the HTTP bodies inside `final_response`.
  Verified on a live run of the image with the sample request: four `httpcalls` entries, each `status: 204` with a ~1.9 KB
  request body. This is a debug path the caller switches on per request, not a guarantee, which is why the contract still does
  not present it as items 2 and 3.
- Guaranteed delivery of every packet. A non-blocking `exitpoint`, bounded memory and no loss with a stdout that never drains cannot
  all hold at once; the module keeps the first two (FR-08 AC1a).
