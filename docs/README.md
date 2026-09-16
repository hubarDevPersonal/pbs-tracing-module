# Deliverables — Prebid Server Tracing Module (`test_provider.test_tracer`)

This folder contains the analysis and planning artefacts for the technical assessment described in
[../README.md](../README.md). The implementation phase follows these documents.

| # | Document | Purpose |
|---|----------|---------|
| 01 | [01-analysis.md](01-analysis.md) | Facts established from the Prebid Server source and a live run of the provided artefacts. Ambiguities and the decisions taken. |
| 02 | [02-specification.md](02-specification.md) | Functional and non-functional requirements with acceptance criteria, the trace JSON contract, and the rule/state model. |
| 03 | [03-design.md](03-design.md) | Technical design: package layout, types, hook-by-hook behaviour, concurrency model, registration, testability seams. |
| 04 | [04-test-plan.md](04-test-plan.md) | Test strategy and requirement-to-test traceability. Unit, race, in-process integration, and end-to-end levels. |
| 05 | [05-runbook.md](05-runbook.md) | How to build, register, run, and verify the module locally. Troubleshooting. |

Code artefacts produced in this phase:

| Path | Content |
|------|---------|
| [../modules/test_provider/test_tracer/](../modules/test_provider/test_tracer/) | Drop-in module package for the Prebid Server tree: API skeleton (`module.go`, `rules.go`, `tracer.go`, `output.go`), module README, and the preliminary test suite (red phase of TDD). |
| [../e2e/](../e2e/) | End-to-end driver script: builds PBS with the module, runs it with the provided `pbs.yaml` against live bidders, asserts the NDJSON trace. |

Conventions used throughout:

- Requirement IDs `FR-xx` / `NFR-xx` are defined in the specification and referenced from design and tests.
- Prebid Server facts reference the upstream repository at commit `f660bedc` (module path `github.com/prebid/prebid-server/v4`, Go 1.25) as checked out on 2026-09-16.
