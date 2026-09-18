# Deliverables — Prebid Server Tracing Module (`test_provider.test_tracer`)

This folder contains the assessment text and the engineering artefacts for the module. Project overview: [../README.md](../README.md).

| # | Document | Purpose |
|---|----------|---------|
| 00 | [00-assessment.md](00-assessment.md) | The original task statement, unchanged. |
| 01 | [01-analysis.md](01-analysis.md) | Facts established from the Prebid Server source and a live run of the provided artefacts. Ambiguities and the decisions taken. |
| 02 | [02-specification.md](02-specification.md) | Functional and non-functional requirements with acceptance criteria, the trace JSON contract, and the rule/state model. |
| 03 | [03-design.md](03-design.md) | Technical design: package layout, types, hook-by-hook behaviour, concurrency model, registration, testability seams. |
| 04 | [04-test-plan.md](04-test-plan.md) | Test strategy and requirement-to-test traceability. Unit, race, in-process integration, and end-to-end levels. |
| 05 | [05-runbook.md](05-runbook.md) | How to build, register, run, and verify the module locally. Troubleshooting. |
| 06 | [06-performance.md](06-performance.md) | Request-time budget, PBS performance knobs (HTTP client pools, throttling, timeouts, GC), DNS caching sidecar, CPU tracking (pprof, Prometheus), module overhead benchmarks and a measured load run. |

Code artefacts produced in this phase:

| Path | Content |
|------|---------|
| [../internal/testtracer/](../internal/testtracer/) | The module package (copied to `<pbs>/modules/test_provider/test_tracer` at build time): implementation, module README, test suite (58 tests, race-clean, 91 % coverage). |
| [../internal/tracecheck/](../internal/tracecheck/), [../cmd/tracecheck/](../cmd/tracecheck/) | Verification library and CLI for NDJSON traces; the assertion step of the e2e scripts. |
| [../Dockerfile](../Dockerfile), [../Makefile](../Makefile), [../docker-compose.yml](../docker-compose.yml) | Build PBS (pinned commit) with the module compiled in and the assessment's `pbs.yaml` baked in; `make docker-e2e` runs the full check. |
| [../deploy/](../deploy/) | `pbs.perf.yaml` tuned configuration and the CoreDNS `Corefile`; wired by the `perf` profile in `docker-compose.yml`. |
| [../cmd/loadgen/](../cmd/loadgen/), [../internal/loadgen/](../internal/loadgen/) | Fixed-rate load generator with latency percentiles. |
| [../testdata/bid-request-live-bid.json](../testdata/bid-request-live-bid.json) | The sample request plus onetag's test publisher under the second rule; phase B of the e2e scripts proves item 3 live. |
| [../scripts/](../scripts/) | `install-module.sh`, `e2e-live.sh` (local checkout), `e2e-docker.sh` (image), `perf-docker.sh`, `profile.sh`: builds PBS with the module, runs it with the provided `pbs.yaml` against live bidders, asserts the NDJSON trace. |

Conventions used throughout:

- Requirement IDs `FR-xx` / `NFR-xx` are defined in the specification and referenced from design and tests.
- Prebid Server facts reference the upstream repository at commit `f660bedc` (module path `github.com/prebid/prebid-server/v4`, Go 1.25) as checked out on 2026-09-16.
