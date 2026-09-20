# pbs-tracing-module

A [Prebid Server](https://github.com/prebid/prebid-server) (Go) module, `test_provider.test_tracer`, that traces auctions on
`/openrtb2/auction` for selected partners and prints one JSON object per traced auction to **stdout**. Built for the technical
assessment in [docs/assessment/00-assessment.md](docs/assessment/00-assessment.md).

Stack: Go ≥ 1.25 (`go.mod` says 1.25; developed with 1.26), Prebid Server v4 (pinned upstream commit as a Go dependency), Docker. No mocks: end-to-end runs against live bidders.

---

## What it does

For every auction whose resolved `Account.ID` matches a hardcoded rule `{PartnerID, Duration, TracePacketsAmount}` the module records

1. the incoming `BidRequest` (raw body) with timestamp;
2. every outgoing `BidRequest` per bidder with timestamp and bidder name;
3. every `BidResponse` received from a bidder with timestamp and bidder name;
4. the final auction response returned to the client with timestamp;

and writes the packet as one NDJSON line at the `exitpoint` stage. Tracing for a partner stops when `Duration` since the first
traced request is exceeded or `TracePacketsAmount` auctions were traced, whichever comes first. The module never rejects requests
and never mutates payloads. Contract and decisions: [docs/02-specification.md](docs/02-specification.md), [docs/01-analysis.md](docs/01-analysis.md).

## Quick start (Docker)

```bash
make docker-build                                   # PBS @ pinned commit + module; module tests run inside the build
docker run --rm -p 8080:8080 pbs-tracer:local 2>pbs.log | tee trace.ndjson
sh docs/assessment/02-send-bid-request.sh           # other terminal; repeat > TracePacketsAmount times
```

`make e2e` builds the image, starts it, sends the requests and checks the trace, the hook outcomes and the PBS log.

## Repository layout

The repository is the module plus what it takes to build, run and test it. There are no other programs in it.

```
modules/test_provider/test_tracer/   the module, at the path PBS requires: copied unchanged into a PBS tree
test/e2e/                            end-to-end suite (build tag e2e): runs the image, drives live auctions, checks stdout
test/load/                           load suite (build tag load) and the constant-rate driver it uses
docs/                                analysis, specification, design, test plan, test specifications, runbook
docs/assessment/                     the task statement and the files it came with (sample request, curl script), unchanged
deploy/                              pbs.perf.yaml (tuned configuration), pbs.load.yaml (same, bidders → stub), CoreDNS Corefile
scripts/                             install-module.sh (into a PBS checkout), perf-docker.sh (load + pprof + metrics), profile.sh
Dockerfile                           clone PBS @ PBS_REF, add the module, go generate, test, build; runtime with pbs.yaml baked in
pbs.yaml                             assessment configuration, unchanged (PBS reads it from its working directory)
```

The module imports only PBS and the standard library. `go.mod` pins `github.com/prebid/prebid-server/v4` to the commit the Docker
image builds, so `go test ./...` and `golangci-lint` run here without a PBS checkout.

## Development

```bash
make test        # module unit, race, integration and overhead tests; unit tests of the e2e and load helpers
make lint        # golangci-lint run, including the e2e and load build tags
make fmt         # gofumpt + golines
make cover       # coverage of the module
make bench       # module cost per traced and untraced auction (ns/op, allocs)
make e2e         # build the image and run the end-to-end suite against live bidders
make load        # load suite against a running PBS (PBS_URL, default http://localhost:8080), live bidders
make load-bench  # bench matrix against stub bidders: hooks off/on, active tracing, 3 partners, large payload, stalled stdout
make highload    # rate ladder to saturation + closed loop, CPU per auction, medians of 3; report → docs/reports/highload.md
make perf        # load suite on the perf profile (tuned config + DNS cache) with CPU profile and metric deltas
```

CI (`.github/workflows/ci.yml`): lint, race tests, coverage, govulncheck (advisory), Docker image build with a `/status` smoke test.

## Rules

Hardcoded in [modules/test_provider/test_tracer/rules_default.go](modules/test_provider/test_tracer/rules_default.go). The sample request resolves
to `Account.ID = 664-025-677-881` (`site.publisher.ext.prebid.parentAccount`), which is the first rule.

## Testing and load

Strategy: [docs/04-test-plan.md](docs/04-test-plan.md). Scenarios per level, independent of the code:
[docs/test-specs/](docs/test-specs/). Running the load suite and the perf profile: [docs/05-runbook.md](docs/05-runbook.md) §8.

## Performance

Measured on a 4-CPU Docker VM against stub bidders answering in 10 ms, three runs per cell, medians
([docs/06-highload-report.md](docs/06-highload-report.md)):

| | hooks off | module on, nothing traced | every auction traced |
|---|---:|---:|---:|
| CPU per auction at 800 auctions/s | 1.79 ms | 1.94 ms (+0.2 ms) | 2.20 ms (+0.4 ms) |
| p99 at 800 auctions/s | 15.5 ms | 18.3 ms | 20.0 ms |
| highest sustained step | 1600/s | 1600/s | 800/s |
| closed-loop throughput, 128 in flight | 2389/s | 2230/s | 1244/s |
| share of PBS CPU on paths through the module (closed loop) | 0 | 0.04 % | 2.8 % |

The module's own hooks cost tens of microseconds. On untraced traffic the cost is the copy of the request body at `entrypoint`
plus PBS's hook execution, about 0.2 ms of CPU per auction. With every auction traced the limit is not the module but the
stdout path: on this VM the container log driver absorbs about 1300 packets/s (13 MB/s), above that the queue drops packets and
the whole server, sharing the VM's CPUs with the log driver, slows down. Rules bound how many auctions are traced, so this is a
ceiling on the tracing rate, not on the server.

## Assessment requirements and their evidence

Every line of [the task](docs/assessment/00-assessment.md) with what proves it. Scenario ids are defined in
[docs/test-specs/](docs/test-specs/): `M` module in process, `E` end to end against the image and live bidders,
`L` load. `make test` runs every `M` scenario and `L-02`, `L-03`; `make bench` `L-01`; `make e2e` the `E` ones; `make load`
`L-04` against live bidders; `make load-bench` `L-05` to `L-09` against stub bidders.

| Task | Evidence | Limit to know |
|------|----------|---------------|
| A custom PBS module in Go, registered through the hooks framework | M-01, M-32 (PBS's real hook executor), E-01 (no "Not found hook", every hook `success`) | |
| 1. Incoming BidRequest and its timestamp | M-07, M-08, M-09, E-03 | body as PBS received it at `entrypoint`, before stored-request merge |
| 2. Outgoing BidRequest per bidder, timestamp, bidder name | M-10, M-11, M-12, E-03 | the per-bidder request the hook exposes, before the adapter builds its HTTP call; timestamp is the hook time |
| 3. Incoming BidResponse per bidder, timestamp, bidder name | M-13, M-14, E-05 | the adapter's parsed result the hook exposes, not the HTTP body; a bidder answering 204 has no entry (PBS never calls the hook) |
| 4. Final auction response and its timestamp | M-15, E-03 | the object PBS encodes to the client, including `ext.debug` when requested |
| Trace as JSON on stdout | M-16, M-17, M-18, M-19, M-31, M-35, E-06 | one NDJSON line per auction, written through a bounded queue |
| Hardcoded rules `{PartnerID, Duration, TracePacketsAmount}` | M-02, M-03, M-04; [rules_default.go](modules/test_provider/test_tracer/rules_default.go) | invalid rules stop PBS at startup |
| Trigger: `Account.ID` equals a rule's `PartnerID` | M-05, M-06, E-02, E-04 | decided on PBS's own resolved account, at `processed_auction_request` |
| Stop: time since the first traced BidRequest exceeds `Duration` | M-20, M-23 | measured between request arrivals, from the incoming timestamp of the first traced request |
| Stop: `TracePacketsAmount` traces collected | M-21, M-22, M-25, M-36, E-02, E-05 | slot reserved at trigger time, so concurrency never overshoots; given back after 5 minutes if the auction never completed |
| `Account.ID` maps to `PartnerID` | M-04, [analysis §2.1](docs/01-analysis.md) | the sample resolves to `parentAccount` `664-025-677-881`, not `publisher.id` |
| `test: 1` yields an appnexus bid | not reproducible from any network tried, [analysis §2.3](docs/01-analysis.md) | item 3 is proven live with onetag's test publisher (phase B of `make e2e`) |
| Only `/openrtb2/auction` | M-26 | checked by the plan and by every hook |
| Fit for a high-load server (implied) | L-01 … L-12; [docs/06-highload-report.md](docs/06-highload-report.md) | hooks cost tens of µs and ≤ 0.8 ms of CPU per auction with every auction traced; a stalled or saturated stdout drops packets instead of delaying auctions |

## Decisions and limits

Where the implementation had to choose, or cannot do what a literal reading asks. All are argued in
[docs/01-analysis.md](docs/01-analysis.md) §4 and §5.

- **A packet is one auction**, not one collected event. `TracePacketsAmount` counts auctions (D1).
- **Items 2 and 3 are hook payloads.** PBS hooks see the per-bidder request before the adapter's `MakeRequests` and the adapter's
  result after `MakeBids`; the HTTP bodies never reach a module. Capturing them is a PBS-core change (D15).
- **A stdout that stops draining loses packets**, it never delays a response. The queue holds 64 packets; overflow is counted and
  logged; `Shutdown` drains on graceful stop (D14).
- **A slot reserved by an auction PBS fails with 4xx/5xx after the trigger is given back after 5 minutes.** `exitpoint` does not
  run on that path, so nothing is written and the loss is detected late; until then the slot counts (D13, FR-10 AC4).
- **The window is measured between request arrivals**, the timestamps the trace reports, and `elapsed == Duration` is still inside
  it (D3).
- **Anyone who knows a `PartnerID` can trigger a trace** when `account_required` is `false`, as in the provided configuration: the
  account comes from the request body. In production the rules should name accounts that exist in the account store (analysis §5.7).
- **If stdout is a pipe whose reader exits, PBS dies**: Go terminates a process on `EPIPE` on fd 1. Run with stdout on a file or a
  log driver (analysis §5.8).
- **`make load-bench` builds the module with a different rule set** (build tag `loadbench`): three partners with limits that outlast
  a run. The production image never uses that tag.
- **Rule state is per process.** Replicas and restarts have independent windows and limits; the task asks for no cluster-wide quota.
- **The sample's four bidders never bid** from any network tried (analysis §2.3.1), so item 3 cannot be shown with the sample
  request alone. `make e2e` proves it with a second request that adds onetag's test publisher.
- **Traces started before a stop condition complete and are written** (D3).

## Known behaviour with live bidders

With the sample request all four bidders answer HTTP 204 from this network (also when calling appnexus directly), so Prebid
Server never invokes `raw_bidder_response` for them. Item 3 is therefore proven live with a second request,
[testdata/bid-request-live-bid.json](testdata/bid-request-live-bid.json): the sample plus onetag's documented test publisher,
which returns a real $2.00 test creative. The end-to-end suite runs it as phase B with strict assertions; phase A keeps the
assessment request verbatim. Details: [docs/01-analysis.md](docs/01-analysis.md) §2.3.
