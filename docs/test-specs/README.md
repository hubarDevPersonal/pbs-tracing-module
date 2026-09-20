# Test specifications

Scenarios that demonstrate the acceptance criteria of [../02-specification.md](../02-specification.md). Strategy, levels and exit
criteria: [../04-test-plan.md](../04-test-plan.md).

| File | Ids | Level |
|------|-----|-------|
| [module.md](module.md) | `M-01` … | unit, race and integration: the module in process, including PBS's real hook executor |
| [e2e.md](e2e.md) | `E-01` … | Prebid Server with the module, the provided `pbs.yaml`, live bidders |
| [load.md](load.md) | `L-01` … | cost and blocking behaviour of the module; PBS with the module under a sustained auction rate |

Rules for these files:

- Scenarios are derived from the requirements only. They name observable behaviour, never functions, files or internal types.
- Ids are stable. A retired scenario keeps its id with the note "retired"; a new one takes the next free id.
- Each scenario lists the acceptance criteria it covers. Every acceptance criterion is covered by at least one scenario.
- Tests reference scenario ids in their doc comments (`// M-21. FR-10 AC1`); searching for an id finds its tests.
