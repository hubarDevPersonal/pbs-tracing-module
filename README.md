# pbs-tracing-module

A [Prebid Server](https://github.com/prebid/prebid-server) (Go) module, `test_provider.test_tracer`, that traces auctions on
`/openrtb2/auction` for selected partners and prints one JSON object per traced auction to **stdout**. Built for the technical
assessment in [docs/00-assessment.md](docs/00-assessment.md).

Stack: Go 1.25, Prebid Server v4 (pinned upstream commit as a Go dependency), Docker. No mocks: end-to-end runs against live bidders.

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
sh 02-send-bid-request.sh                           # other terminal; repeat > TracePacketsAmount times
```

`make e2e` builds the image, starts it, sends the requests and checks the trace, the hook outcomes and the PBS log.

## Repository layout

The repository is the module plus what it takes to build, run and test it. There are no other programs in it.

```
modules/test_provider/test_tracer/   the module, at the path PBS requires: copied unchanged into a PBS tree
test/e2e/                            end-to-end suite (build tag e2e): runs the image, drives live auctions, checks stdout
test/load/                           load suite (build tag load) and the constant-rate driver it uses
docs/                                assessment text, analysis, specification, design, test plan, test specifications, runbook
deploy/                              pbs.perf.yaml (tuned configuration) and the CoreDNS Corefile of the perf profile
scripts/                             install-module.sh (into a PBS checkout), perf-docker.sh (load + pprof + metrics), profile.sh
Dockerfile                           clone PBS @ PBS_REF, add the module, go generate, test, build; runtime with pbs.yaml baked in
pbs.yaml, 01-bid-request-example.json, 02-send-bid-request.sh   assessment inputs, unchanged
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
make load        # load suite against a running PBS (PBS_URL, default http://localhost:8080)
make perf        # load suite on the perf profile (tuned config + DNS cache) with CPU profile and metric deltas
```

CI (`.github/workflows/ci.yml`): lint, race tests, coverage, govulncheck (advisory), Docker image build with a `/status` smoke test.

## Rules

Hardcoded in [modules/test_provider/test_tracer/rules.go](modules/test_provider/test_tracer/rules.go). The sample request resolves
to `Account.ID = 664-025-677-881` (`site.publisher.ext.prebid.parentAccount`), which is the first rule.

## Testing and load

Strategy: [docs/04-test-plan.md](docs/04-test-plan.md). Scenarios per level, independent of the code:
[docs/test-specs/](docs/test-specs/). Running the load suite and the perf profile: [docs/05-runbook.md](docs/05-runbook.md) §8.

## Known behaviour with live bidders

With the sample request all four bidders answer HTTP 204 from this network (also when calling appnexus directly), so Prebid
Server never invokes `raw_bidder_response` for them. Item 3 is therefore proven live with a second request,
[testdata/bid-request-live-bid.json](testdata/bid-request-live-bid.json): the sample plus onetag's documented test publisher,
which returns a real $2.00 test creative. The end-to-end suite runs it as phase B with strict assertions; phase A keeps the
assessment request verbatim. Details: [docs/01-analysis.md](docs/01-analysis.md) §2.3.
