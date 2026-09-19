# test_provider.test_tracer

Prebid Server module that traces auctions on `/openrtb2/auction` for selected partners and prints one JSON object per traced
auction to **stdout**. Built for the "Prebid Server Tracing Module" technical assessment.

## What is collected

For every traced auction:

1. the incoming `BidRequest` (raw body as received) with timestamp;
2. every outgoing `BidRequest` per bidder with timestamp and bidder name;
3. every `BidResponse` received from a bidder with timestamp and bidder name;
4. the final auction response returned to the client with timestamp.

Output is newline-delimited JSON; see `workspace/02-specification.md` §5 in the assessment repository for the contract.

## Trigger and stop rules

Rules are **hardcoded** in `rules_default.go`:

```go
{PartnerID: "664-025-677-881", Duration: 10 * time.Minute, TracePacketsAmount: 3}
```

- A trace starts when the request's resolved `Account.ID` equals a rule's `PartnerID`.
- Tracing for a partner stops when the time since the partner's first traced request exceeds `Duration`, or when
  `TracePacketsAmount` auctions have been traced — whichever comes first. A stopped partner is not re-armed.
- Rules are validated at startup; an invalid rule set prevents Prebid Server from starting.

`Account.ID` is resolved by Prebid Server itself (`publisher.ext.prebid.parentAccount` before `publisher.id`, for app → site → dooh).

## Configuration

```yaml
hooks:
  enabled: true
  modules:
    test_provider:
      test_tracer:
        enabled: true
  host_execution_plan:
    endpoints:
      /openrtb2/auction:
        stages:
          entrypoint:                   { groups: [ { timeout: 120000, hook_sequence: [ { module_code: test_provider.test_tracer, hook_impl_code: test_provider_test_tracer } ] } ] }
          processed_auction_request:    { groups: [ { timeout: 120000, hook_sequence: [ { module_code: test_provider.test_tracer, hook_impl_code: test_provider_test_tracer } ] } ] }
          bidder_request:               { groups: [ { timeout: 120000, hook_sequence: [ { module_code: test_provider.test_tracer, hook_impl_code: test_provider_test_tracer } ] } ] }
          raw_bidder_response:          { groups: [ { timeout: 120000, hook_sequence: [ { module_code: test_provider.test_tracer, hook_impl_code: test_provider_test_tracer } ] } ] }
          all_processed_bid_responses:  { groups: [ { timeout: 120000, hook_sequence: [ { module_code: test_provider.test_tracer, hook_impl_code: test_provider_test_tracer } ] } ] }
          auction_response:             { groups: [ { timeout: 120000, hook_sequence: [ { module_code: test_provider.test_tracer, hook_impl_code: test_provider_test_tracer } ] } ] }
          exitpoint:                    { groups: [ { timeout: 120000, hook_sequence: [ { module_code: test_provider.test_tracer, hook_impl_code: test_provider_test_tracer } ] } ] }
```

The module reads no module-level or account-level configuration.

## Stages

| Stage | Role |
|-------|------|
| `entrypoint` | capture raw body + timestamp (account unknown yet) |
| `processed_auction_request` | trigger decision on `AccountID`; start trace |
| `bidder_request` | record the per-bidder OpenRTB request PBS hands to the adapter (before the adapter builds its HTTP calls) |
| `raw_bidder_response` | record the adapter's parsed result (after it read the HTTP response; not invoked by PBS for HTTP 204 / adapter errors) |
| `all_processed_bid_responses` | pass-through |
| `auction_response` | record final response |
| `exitpoint` | refine final response with the object being sent; print the packet |

The module never rejects requests and never mutates payloads.

What the hooks expose is what the trace holds: items 2 and 3 are the objects at the module stages, not the HTTP bodies exchanged
with bidders, which no PBS hook can see. Their timestamps are hook times.

## Output

Packets go to stdout through a bounded queue (64) drained by one goroutine, so no hook ever waits for stdout. If the queue is full
the packet is dropped and a warning with the drop count goes to stderr. `Shutdown` (called by PBS on graceful stop) drains the queue.

## Rules

`rules_default.go` holds the production rule set. `rules_loadbench.go` (build tag `loadbench`) replaces it with three partners
whose limits outlast a load run; only `make load-bench` builds with that tag.

## Layout

| File | Holds |
|------|-------|
| `module.go` | `Builder`, `Module`, `Shutdown`, module-context keys |
| `hooks.go` | the seven hook handlers, one per stage |
| `rules.go`, `rules_default.go`, `rules_loadbench.go` | `Rule`, validation, the production rule set, the bench rule set (tag `loadbench`) |
| `tracer.go` | `Tracer`: per-partner state, trigger and stop conditions |
| `trace.go` | `AuctionTrace`: per-request collector and snapshots |
| `packet.go` | `TracePacket` and the DTOs of the JSON contract |
| `emitter.go` | NDJSON writer and the bounded asynchronous queue in front of stdout |
| `<file>_test.go` | unit tests next to the file they cover; plus `integration_test.go`, `race_test.go`, `bench_test.go`, `helpers_test.go` |

Tests live in the same package and directory: PBS's coverage check and the module guide expect them there.

## Building

From the repository root: `make docker-build` (PBS at the pinned commit + this module), or `PBS_DIR=<pbs> scripts/install-module.sh`
(copies this directory to `<pbs>/modules/test_provider/test_tracer` and runs `go generate ./modules/...`) and build PBS as usual.

## Running the tests

```bash
make test                                                     # from the repository root, no PBS checkout needed
go test ./modules/test_provider/test_tracer -race -run '^TestRace' -count 3
```

## Maintainer

Artem Hubar — technical assessment submission.
