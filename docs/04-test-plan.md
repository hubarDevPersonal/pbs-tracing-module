# 04 — Test Plan

Requirements: [02-specification.md](02-specification.md). Design: [03-design.md](03-design.md).
Test code lives in `internal/testtracer/*_test.go` (same package, `testify`); `internal/tracecheck` has its own unit tests. This phase delivers the tests in the
**red** state against an API skeleton; the implementation phase turns them green without changing the tests' intent.

## 1. Levels

| Level | Where | What it proves | Tooling |
|-------|-------|----------------|---------|
| L1 Unit | `rules_test.go`, `tracer_test.go`, `output_test.go`, `module_test.go`, `module_branches_test.go` | Rule validation, stop conditions with a fake clock, snapshot semantics, JSON contract, each hook in isolation | `make test` |
| L2 Race | `race_test.go` (`TestRace*`) | Slot reservation under contention, concurrent bidder hooks on one trace, non-interleaved output | `go test ./internal/testtracer -race -run '^TestRace' -count 3` |
| L3 Integration (in-process) | `integration_test.go` | The module driven by the real `hookexecution` executor and plan builder built from the provided `pbs.yaml` stage list; module-context propagation across stages; outcomes have no errors | `go test -run Integration` |
| L4 End-to-end | `e2e/` | PBS with the module registered, the provided `pbs.yaml` and `02-send-bid-request.sh` unchanged, **live bidders**; asserts NDJSON on stdout, hook outcomes in the HTTP response and absence of `Not found hook` warnings. Assertions are implemented in `internal/tracecheck` (unit-tested) and run through `cmd/tracecheck`. Two drivers: local checkout (`scripts/e2e-live.sh`) and Docker image (`scripts/e2e-docker.sh`) | `make e2e` / `make docker-e2e` |
| L5 Manual | [05-runbook.md](05-runbook.md) | Reviewer walkthrough with the provided `02-send-bid-request.sh` | shell |

Exit criteria for the implementation phase: L1–L3 green with `-race`, `go vet` clean, L4 green on a machine with Go and the PBS checkout,
coverage ≥ 90 % statements for **each implemented hook** (official Go module guide) and for the package as a whole (PBS `scripts/check_coverage.sh`).

## 2. Traceability matrix

| Requirement | Tests |
|-------------|-------|
| FR-01 registration | `TestBuilder_ReturnsModuleImplementingAllPlannedStages`, L4 `no "Not found hook" warnings` |
| FR-02 rules | `TestValidateRules` (table), `TestDefaultRulesAreValid`, `TestDefaultRulesCoverSampleRequestAccount`, `TestNewTracer_RejectsInvalidRules`, `TestNewModule_FailsOnInvalidRules` |
| FR-03 trigger | `TestTracerBegin_UnknownPartnerIsNotTraced`, `TestTracerBegin_FirstPacketStartsWindow`, `TestProcessedAuction_StartsTraceForMatchingAccount`, `TestProcessedAuction_NoTraceForUnknownAccount`, `TestIntegration_SampleRequestIsTracedEndToEnd` |
| FR-04 incoming request | `TestEntrypoint_CapturesBodyAndTimestamp`, `TestEntrypoint_CopiesBody`, `TestProcessedAuction_AttachesEntrypointCapture`, `TestProcessedAuction_FallsBackWhenEntrypointCaptureMissing` |
| FR-05 bidder requests | `TestBidderRequest_RecordsOutgoingRequestForTracedAuction`, `TestBidderRequest_IsNoopWithoutActiveTrace`, `TestAuctionTrace_SnapshotsAreImmutable`, `TestAuctionTrace_PreservesInvocationOrder` |
| FR-06 bidder responses | `TestRawBidderResponse_RecordsIncomingResponse`, `TestAuctionTrace_PacketContainsAllSections`, `TestExitpoint_EmitsPacketWhenBidderReturnedNoResponse` |
| FR-07 final response | `TestAuctionResponse_RecordsFinalResponse`, `TestExitpoint_UsesExitpointResponseWhenAvailable`, `TestExitpoint_FallsBackToAuctionResponseWhenPayloadIsNotBidResponse` |
| FR-08 output | `TestJSONEmitter_WritesOneLinePerPacket`, `TestJSONEmitter_PacketSchema`, `TestJSONEmitter_TimestampsAreRFC3339NanoUTC`, `TestJSONEmitter_WriteErrorIsReturned`, `TestExitpoint_EmitsSinglePacketWithAllSections`, `TestExitpoint_DoesNotEmitTwice`, `TestExitpoint_EmitsNothingWithoutActiveTrace`, `TestAuctionTrace_EmptyPacketHasEmptyArrays` |
| FR-09 time limit | `TestTracerBegin_DurationLimitStopsTracing` (boundary: `== Duration` traced, `+1ns` refused) |
| FR-10 amount limit | `TestTracerBegin_AmountLimitStopsTracing`, `TestRaceTracerBeginNeverExceedsAmount`, `TestProcessedAuction_RespectsStopConditions` |
| FR-11 whichever first / no re-arm | `TestTracerBegin_WhicheverComesFirst`, `TestTracerBegin_StoppedPartnerDoesNotRearm`, `TestTracerBegin_PartnersAreIndependent` |
| FR-12 in-flight completes | `TestIntegration_InFlightTraceCompletesAfterPartnerStopped` |
| FR-13 zero side effects | `TestProcessedAuction_NoTraceForUnknownAccount`, `TestExitpoint_EmitsNothingWithoutActiveTrace`, `TestIntegration_SecondAuctionBeyondLimitProducesNoOutput` |
| FR-14 endpoint scope | `TestEntrypoint_IgnoresNonAuctionEndpoint`, `TestModule_TracesOnlyAuctionEndpoint` |
| FR-15 never interfere | `TestHooks_NeverRejectNeverMutate`, `TestHooks_ToleratesNilModuleContextAndNilPayloads`, `TestAuctionTrace_NilInputsAreSafe`, `TestIntegration_OutcomesHaveNoErrors` |
| FR-16 concurrency | `TestRaceAuctionTraceConcurrentAppends`, `TestRaceModuleConcurrentBidderHooks`, `TestRaceJSONEmitterConcurrentEmits`, `TestRaceModuleConcurrentRequests` |
| NFR-04 | `golangci-lint` (gofumpt, golines, gosec, revive, …) and `go vet` in `make lint` and CI |
| NFR-05 | all L1 tests use `fakeClock` and `bytes.Buffer`/`syncBuffer` |

## 2.1 Status of the suite (2026-09-16)

Executed inside a PBS master checkout and inside the Docker build stage (`go vet` clean, `gofmt` clean):

| Metric | Value |
|--------|-------|
| Top-level tests | 61 (66 incl. subtests) |
| Result | all green, also under `-race` (full suite and `-run '^TestRace' -count 3`) |
| Statement coverage, package | 95.4 % |
| Coverage per hook | entrypoint, processed_auction_request, bidder_request, raw_bidder_response, all_processed_bid_responses, auction_response 100 %; exitpoint 93.3 % |

Every hook meets the ≥ 90 % bar of the official Go module guide. The marshal-failure branches (FR-15 AC2) are exercised with an
invalid `json.RawMessage` in `Ext`, the one way `encoding/json` fails on the real PBS types (`module_errors_test.go`).

## 3. Test data

- `testdata/bid_request.json` — verbatim copy of `01-bid-request-example.json`. Resolved `Account.ID` is `664-025-677-881`.
- Bidder responses are constructed in code (`adapters.BidderResponse{Currency: "USD", Bids: [...]}`) — no live network.
- Fake clock: `fakeClock{Now(), Advance(d)}`, start `2026-09-16T10:00:00Z`.

## 4. Integration test design (L3)

Builds the stack PBS itself uses, with no HTTP server:

```go
repo, _ := hooks.NewHookRepository(map[string]interface{}{ModuleCode: module})
planBuilder := hooks.NewExecutionPlanBuilder(config.Hooks{Enabled: true, HostExecutionPlan: planFromProvidedYAML}, repo)
executor := hookexecution.NewHookExecutor(planBuilder, hookexecution.EndpointAuction, &metricsConfig.NilMetricsEngine{})
```

then drives the stages in the order `auction.go` / `exchange` do: `ExecuteEntrypointStage` → `SetAccount(&config.Account{ID: "664-025-677-881"})`
→ `ExecuteProcessedAuctionStage` → per bidder `ExecuteBidderRequestStage` + `ExecuteRawBidderResponseStage` → `ExecuteAllProcessedBidResponsesStage`
→ `ExecuteAuctionResponseStage` → `ExecuteExitpointStage(resp, httptest.NewRecorder())`. Assertions: exactly one NDJSON line, all four
sections present, `executor.GetOutcomes()` has only `StatusSuccess` results with empty `Errors`.

The plan is the stage list from the provided `pbs.yaml`, expressed as JSON and unmarshalled into `config.HookExecutionPlan`, so the test
fails if the config keys used in the assessment stop matching the PBS config schema.

## 5. End-to-end (L4) — `scripts/e2e-*.sh`, live bidders

Preconditions: `PBS_DIR` points at a PBS checkout (v4 module path); Go ≥ 1.25; outbound internet; ports 8080/6060 free. No mocks by decision.

1. `scripts/install-module.sh` (copy + `go generate`), `go vet`, module tests, `go build` (live) — or `docker build` (image), where the same steps run inside the build stage.
2. Start PBS with the **provided** `pbs.yaml`, stdout → `trace.ndjson`, stderr → `pbs.log`.
3. Send `01-bid-request-example.json` N times (N > `TracePacketsAmount` of the sample partner), then one request with an unknown account.
4. Assert with `tracecheck -expect N -responses 'resp-*.json' -pbs-log pbs.log trace.ndjson`:
   - `trace.ndjson` has exactly `TracePacketsAmount` lines, each valid JSON, `partner_id == 664-025-677-881`, `packet_index` 1..N;
   - item 1: `incoming_request.body.id` equals the sample id;
   - item 2: `bidder_requests` covers exactly `{aceex, appnexus, amx, adyoulike}`, each with the same auction id and a timestamp ≥ the incoming one;
   - item 3: every `bidder_responses` entry has a known bidder and the DTO shape; total count is reported. With live bidders answering 204 the list may be empty (PBS never calls `raw_bidder_response`); `STRICT_BIDS=1` makes an empty total a failure;
   - item 4: `final_response.body.id` equals the sample id and contains `ext.debug`, proving it is the enriched response the client received;
   - every HTTP response has `ext.prebid.modules` entries for `test_provider.test_tracer` with status `success` and no `failure`/`timeout`;
   - `pbs.log` contains no `Not found hook` warnings;
   - the unknown-account request adds no line.

## 6. Manual checks (L5) — see runbook

Includes the observation that with live bidders `bidder_responses` may be empty (204), which is expected and documented (FR-06 AC2).

## 7. Non-goals of the test suite

No load/performance tests; no tests of PBS internals beyond what the integration test needs; no assertions on ordering across bidders.
