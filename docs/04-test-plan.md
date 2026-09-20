# 04 — Test Plan

Requirements: [02-specification.md](02-specification.md). Test specifications: [test-specs/](test-specs/).

This document states how the module is tested: levels, environments, entry and exit criteria, and how tests stay tied to
requirements. What each test checks is in the test specifications. Results belong to CI, not here.

## 1. Separation of concerns

- **Requirements** (`02-specification.md`) say what the module must do: `FR-xx` / `NFR-xx` with acceptance criteria `ACn`.
- **Test specifications** (`test-specs/`) say how each acceptance criterion is demonstrated, as Given / When / Then scenarios with
  stable ids (`M-xx` module, `E-xx` end to end, `L-xx` load). They are written from the requirements alone and name no code: no
  functions, files or internal types. Whoever writes them needs the specification, not the implementation.
- **Tests** implement scenarios. Every test names the scenario ids and acceptance criteria it covers in its doc comment, so the
  requirement → scenario → test chain is found by searching the code, and no hand-maintained matrix goes stale.
- **The e2e suite decodes the trace with its own types**, restated from the JSON contract (spec §5), and never imports the module.
  A change in the module that breaks the contract fails the e2e suite instead of silently updating it.

A change of behaviour starts in the specification, then the test specifications, then the tests, then the code.

## 2. Levels

| Level | Scope | Environment | Scenarios |
|-------|-------|-------------|-----------|
| Unit | rules, tracer state, trace collector, JSON output, each hook in isolation | in process; fake clock, in-memory writer | [module.md](test-specs/module.md) |
| Race | shared state under concurrent hooks and requests | in process, `-race`, repeated runs | [module.md](test-specs/module.md) |
| Integration | the module driven by PBS's real hook executor and plan builder, with the stage list of the provided `pbs.yaml` | in process, no HTTP server | [module.md](test-specs/module.md) |
| End to end | PBS built at the pinned commit with the module compiled in, the provided `pbs.yaml` unchanged, **live bidders** | Docker image, started by the test | [e2e.md](test-specs/e2e.md) |
| Load | the module's steady-state cost and blocking behaviour; PBS with the module under a sustained auction rate; a bench matrix against stub bidders (hooks off/on, active tracing, partners, payload size, stalled stdout) | in process (every run); a running PBS with live bidders and the bench image with stub bidders (on demand) | [load.md](test-specs/load.md) |

No mocks of PBS and no mock bidders: the in-process levels use PBS's own packages, and the end-to-end and load levels use the real
server against live bidders (decision of the assignment owner).

## 3. How each level runs

| Level | Command | When |
|-------|---------|------|
| Unit, race, integration, in-process load criteria | `make test` | every change; CI on every push and pull request |
| Race, repeated | `go test ./modules/test_provider/test_tracer -race -run '^TestRace' -count 3` | CI |
| Coverage | `make cover` | CI |
| Benchmarks | `make bench` | on changes to the hook path; compared against the previous run |
| End to end | `make e2e` (build tag `e2e`) | before merging module changes; needs Docker and outbound internet |
| Load against PBS | `make load` or `make perf` (build tag `load`) | before merging changes to the hook path or the PBS configuration |
| Bench matrix | `make load-bench` (build tag `load`, image built with `loadbench`) | before merging changes to the hook path; numbers compared with the previous run |
| High-load run | `make highload` (same stand; rate ladder, closed loop, CPU per auction, report) | before a release; the checked-in report is refreshed when the hook path changes |

The `e2e` and `load` suites sit behind build tags because they need Docker, network and minutes of wall time. The helpers they use
(trace verification, the load driver) have unit tests in the default suite.

## 4. Entry and exit criteria

Entry: the module builds inside the pinned PBS tree (`go generate`, `go vet`, `gofmt` in the Docker build stage).

Exit, all required:

- every scenario in `test-specs/` has at least one test that names it;
- unit, race and integration levels green with `-race`; `go vet` and `golangci-lint` clean, including the `e2e` and `load` tags;
- statement coverage ≥ 90 % for each implemented hook and for the package (official Go module guide, PBS `scripts/check_coverage.sh`);
- end-to-end green against a freshly built image;
- load criteria of `load.md` met: the in-process ones in every run, the ones against PBS before a release.

## 5. Test data

- `modules/test_provider/test_tracer/testdata/bid_request.json` is a verbatim copy of `docs/assessment/01-bid-request-example.json`. Its account resolves
  to `664-025-677-881`.
- `testdata/bid-request-live-bid.json` is the sample plus onetag's documented test publisher, with `parentAccount` removed so the account
  resolves to `33415-10498`. onetag returns a real test bid, which exercises bidder responses live.
- Bidder responses at the in-process levels are built in code. Time at the unit level comes from a fake clock.

## 6. Known limits of the suite

- The sample's four bidders answer 204 from every network tried (analysis §2.3.1), so the assessment request alone never exercises
  bidder responses live. The live-bid request covers it; the in-process levels cover it deterministically.
- Latency against live bidders is dominated by the bidders and is reported, not asserted. Timing budgets apply only in process.
- The bench compares configurations at one moderate rate against stub bidders; the rate at which PBS saturates depends on the host and
  is not asserted, as the assessment sets no target.
