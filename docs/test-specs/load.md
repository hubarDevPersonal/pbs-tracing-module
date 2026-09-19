# Load scenarios (L)

The module sits on the path of every auction. Rules bound how many auctions are traced, so its steady-state cost is the cost of an
auction that is **not** traced, and its only blocking call is the stdout write of a traced one (NFR-01). These scenarios check both.

L-01 to L-03 run in process on every test run. L-04 needs a running Prebid Server and live bidders, and runs on demand.

**L-01 Per-auction cost is measured** — NFR-01
- Given the module with four bidders
- When a traced auction, an untraced auction and one packet write are repeated
- Then time, bytes and allocations per operation are reported. Timing depends on the machine and is compared between runs, not
  against a fixed number

**L-02 An untraced auction stays within its allocation budget** — NFR-01, FR-05 AC3
- Given an account without a rule and four bidders
- When the whole auction runs through all seven hooks
- Then it makes at most 20 allocations, most of them the entrypoint body copy that happens before the account is known

**L-03 A stalled stdout holds only traced auctions** — NFR-01, FR-13 AC1
- Given a traced auction blocked in its stdout write
- When 100 untraced auctions run meanwhile
- Then all of them complete while the traced one is still blocked, and it completes once the write returns

**L-04 PBS with the module sustains a constant auction rate** — NFR-01, FR-15 AC1
- Given a running server with the module, the provided or the perf configuration, and live bidders
- When the sample request, then the live-bid request, are each sent at a constant arrival rate (default 5 per second) for a fixed
  time (default 30 s), with enough concurrency to absorb the bidders' latency
- Then every auction is answered with a 2xx status, no request fails or times out, and no arrival is dropped for lack of a free
  worker. Latency percentiles are reported, not asserted: live bidders dominate them

The default rate is a courtesy limit towards third-party bidders (5 auctions × 4 bidders ≈ 20 outbound requests per second).
Capacity of PBS itself needs stubbed bidders and is out of scope.
