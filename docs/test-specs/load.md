# Load scenarios (L)

The module sits on the path of every auction. Rules bound how many auctions are traced, so its steady-state cost is the cost of an
auction that is **not** traced, and no hook waits for stdout: packets go through a bounded queue (NFR-01, FR-08 AC1a). These
scenarios check both.

L-01 to L-03 run in process on every test run. L-04 needs a running Prebid Server and live bidders. L-05 to L-09 form the
bench: Prebid Server from the image built with bench rules (three partners whose limits outlast the run), stub bidders on the
host answering every call with a bid after a fixed delay, one fresh server per scenario. L-04 and the bench run on demand.

**L-01 Per-auction cost is measured** — NFR-01
- Given the module with four bidders
- When a traced auction, an untraced auction and one packet write are repeated
- Then time, bytes and allocations per operation are reported. Timing depends on the machine and is compared between runs, not
  against a fixed number

**L-02 An untraced auction stays within its allocation budget** — NFR-01, FR-05 AC3
- Given an account without a rule and four bidders
- When the whole auction runs through all seven hooks
- Then it makes at most 20 allocations, most of them the entrypoint body copy that happens before the account is known

**L-03 A stalled stdout holds no auction** — NFR-01, FR-08 AC1a
- Given the production output path and a stdout that does not drain
- When more traced auctions run than the output queue holds, and untraced auctions run meanwhile
- Then every auction completes without waiting; the packets beyond the queue are dropped and counted; once stdout drains again the
  queued packets are written

**L-04 PBS with the module sustains a constant auction rate** — NFR-01, FR-15 AC1
- Given a running server with the module, the provided or the perf configuration, and live bidders
- When the sample request, then the live-bid request, are each sent at a constant arrival rate (default 5 per second) for a fixed
  time (default 30 s), with enough concurrency to absorb the bidders' latency
- Then every auction is answered with a 2xx status, no request fails or times out, and no arrival is dropped for lack of a free
  worker. Latency percentiles are reported, not asserted: live bidders dominate them

The default rate is a courtesy limit towards third-party bidders (5 auctions × 4 bidders ≈ 20 outbound requests per second).

## Bench (stub bidders)

Every bench scenario sends auctions at a constant rate (default 100 per second for 20 s) and requires: every auction answered
with 200 and the stub bids, no transport error or timeout, no arrival dropped by the generator. Latency percentiles, hook calls
and mean hook time, GC cycles, heap in use and container memory are reported side by side; the assessment sets no numeric SLO,
so latency is compared between scenarios, not asserted against a target.

**L-05 Hooks off against hooks on with nothing traced** — NFR-01, FR-05 AC3, FR-13 AC1
- Given one server with hooks disabled and one with the module enabled, both receiving the sample request whose account has no
  bench rule
- Then no hook runs on the first, every stage runs on the second, neither writes a packet, and the second's latency is the cost
  of the untraced path plus PBS's own hook execution

**L-06 Active tracing on every auction** — FR-08 AC1, AC1a, NFR-01
- Given a partner whose limits outlast the run
- When every auction of the run is for that partner
- Then stdout holds exactly one packet per auction, no packet is dropped, and latency is compared with L-05

**L-07 Several partners at once** — FR-11 AC2, FR-16 AC1
- Given three partners whose limits outlast the run
- When auctions rotate over them
- Then stdout holds exactly one packet per auction and no packet is dropped

**L-08 Large payload** — NFR-01
- Given the traced request grown by 60 KiB of opaque site data (below PBS's request size limit)
- Then every auction is traced, no packet is dropped, and the latency cost of copying and marshalling the larger body is visible
  next to L-06

**L-09 Stalled stdout under load** — FR-08 AC1a, NFR-01
- Given a server whose stdout is a pipe nobody reads, and active tracing on every auction
- When the run fills the pipe buffer and then the output queue
- Then every auction is still answered without error at the target rate, latency is comparable to L-06, and the PBS log reports
  dropped packets

Capacity of PBS itself (rate at which it saturates) is not a bench scenario: it depends on the host, and the assessment sets no
target. The bench compares configurations at one moderate rate that every scenario sustains.
