# Deliverables — Prebid Server Tracing Module (`test_provider.test_tracer`)

This folder is the working space of the assessment: the task statement with the files it came with (`assessment/`) and the engineering artefacts for the module. Project overview: [../README.md](../README.md).

| # | Document | Purpose |
|---|----------|---------|
| 00 | [00-assessment.md](assessment/00-assessment.md) | The original task statement, unchanged. |
| 01 | [01-analysis.md](01-analysis.md) | Facts established from the Prebid Server source and a live run of the provided artefacts. Ambiguities and the decisions taken. |
| 02 | [02-specification.md](02-specification.md) | Functional and non-functional requirements with acceptance criteria, the trace JSON contract, and the rule/state model. |
| 03 | [03-design.md](03-design.md) | Technical design: package layout, types, hook-by-hook behaviour, concurrency model, registration, testability seams. |
| 04 | [04-test-plan.md](04-test-plan.md) | Test strategy: levels, environments, entry and exit criteria, how tests stay tied to requirements. |
| — | [test-specs/](test-specs/) | Test specifications: Given / When / Then scenarios per level (module, end to end, load), derived from the requirements only. |
| 05 | [05-runbook.md](05-runbook.md) | How to build, register, run, verify and load-test the module locally. Troubleshooting. |
| 06 | [06-highload-report.md](06-highload-report.md) | What the module costs under load: rate ladder to saturation, closed-loop ceiling, CPU per auction, hook timeouts, stdout limits. Raw run output in [reports/](reports/). |

Code:

| Path | Content |
|------|---------|
| [../modules/test_provider/test_tracer/](../modules/test_provider/test_tracer/) | The module at the path PBS requires: implementation, module README, unit, race, integration and overhead tests. |
| [../test/e2e/](../test/e2e/) | End-to-end suite: runs the Docker image, drives live auctions, verifies the trace against the JSON contract. |
| [../test/load/](../test/load/) | Load suite (live bidders), the bench matrix with stub bidders, and the constant-rate driver both use. |
| [../Dockerfile](../Dockerfile), [../Makefile](../Makefile), [../docker-compose.yml](../docker-compose.yml) | Build PBS (pinned commit) with the module compiled in and the assessment's `pbs.yaml` baked in. |
| [../deploy/](../deploy/) | `pbs.perf.yaml` tuned configuration and the CoreDNS `Corefile` (the `perf` profile in `docker-compose.yml`); `pbs.load.yaml`, the same configuration with the bidders pointed at the bench's stub. |
| [../testdata/bid-request-live-bid.json](../testdata/bid-request-live-bid.json) | The sample request plus onetag's test publisher under the second rule; exercises bidder responses live. |
| [../scripts/](../scripts/) | `install-module.sh` (into a PBS checkout), `perf-docker.sh` (load suite + CPU profile + metrics on the perf profile), `profile.sh`. |

Conventions used throughout:

- Requirement IDs `FR-xx` / `NFR-xx` are defined in the specification. Scenario ids `M-xx`, `E-xx`, `L-xx` are defined in the
  test specifications. Tests name both in their doc comments.
- Prebid Server facts reference the upstream repository at commit `f660bedc` (module path `github.com/prebid/prebid-server/v4`, Go 1.25) as checked out on 2026-09-16.
