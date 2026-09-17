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

`make docker-e2e` does the above end to end and verifies the trace with `cmd/tracecheck`.

## Repository layout

```
cmd/tracecheck/        CLI that verifies an NDJSON trace (used by the e2e scripts and CI-friendly)
cmd/loadgen/           fixed-rate load generator with latency percentiles (used by scripts/perf-docker.sh)
internal/testtracer/   the PBS module package — copied verbatim to <pbs>/modules/test_provider/test_tracer at build time
internal/tracecheck/   verification library behind cmd/tracecheck
internal/loadgen/      load generator library behind cmd/loadgen
deploy/                pbs.perf.yaml (tuned config) and the CoreDNS Corefile for the perf profile
docs/                  assessment text, analysis, specification, design, test plan, runbook
scripts/               install-module.sh, e2e-live.sh, e2e-docker.sh, perf-docker.sh (load + pprof + metrics), profile.sh
Dockerfile             multi-stage: clone PBS @ PBS_REF, inject module, go generate, test, build; runtime with pbs.yaml baked in
pbs.yaml, 01-bid-request-example.json, 02-send-bid-request.sh   assessment inputs, unchanged
```

The module package has no dependency on anything else in this repository, which is what lets it be dropped into the upstream
`modules/` tree unchanged; `go.mod` pins `github.com/prebid/prebid-server/v4` to the same commit the Docker image builds, so
`go test ./...` and `golangci-lint` run here without a PBS checkout.

## Development

```bash
make test        # go test ./... -race
make lint        # golangci-lint run (config: .golangci.yml)
make fmt         # gofumpt + golines
make cover       # coverage of internal/testtracer
make e2e         # live e2e against PBS_DIR=~/Dev/prebid-server
make docker-e2e  # live e2e against the Docker image
make bench       # module overhead per auction (ns/op, allocs)
make perf        # perf profile: tuned config + caching DNS sidecar, load run, CPU profile, metric deltas
```

CI (`.github/workflows/ci.yml`): lint, race tests, coverage, govulncheck (advisory), Docker image build with a `/status` smoke test.

## Rules

Hardcoded in [internal/testtracer/rules.go](internal/testtracer/rules.go). The sample request resolves to
`Account.ID = 664-025-677-881` (`site.publisher.ext.prebid.parentAccount`), which is the first rule.

## Performance and load

See [docs/06-performance.md](docs/06-performance.md): where a request spends its time, the PBS knobs that matter
(`http_client` pools and dialer, adaptive bidder throttling, auction timeouts, GC threshold), DNS caching via a CoreDNS
sidecar (`docker compose --profile perf`), CPU tracking with pprof on the admin port and Prometheus metrics, and the
module's measured overhead.

## Known behaviour with live bidders

With the sample request all four bidders answer HTTP 204 from this network (also when calling appnexus directly), so Prebid
Server never invokes `raw_bidder_response` for them. Item 3 is therefore proven live with a second request,
[testdata/bid-request-live-bid.json](testdata/bid-request-live-bid.json): the sample plus onetag's documented test publisher,
which returns a real $2.00 test creative. Both e2e drivers run it as phase B with strict assertions; phase A keeps the assessment
request verbatim. Details: [docs/01-analysis.md](docs/01-analysis.md) §2.3.
