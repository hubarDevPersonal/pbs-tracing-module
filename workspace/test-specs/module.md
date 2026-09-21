# Module scenarios (M)

Level: the module in process. Unless a scenario says otherwise: the clock is controlled by the test, output goes to an in-memory
writer, the endpoint is `/openrtb2/auction`, and "an auction" means the module's hooks invoked in PBS order: `entrypoint`,
`processed_auction_request`, `bidder_request` and `raw_bidder_response` per bidder, `all_processed_bid_responses`, `auction_response`,
`exitpoint`. "Traced partner" means an account id listed in the rules and not stopped.

## Registration and rules

**M-01 The module serves every planned stage** — FR-01 AC1, AC3
- Given the module's builder and no module configuration
- When PBS builds the module
- Then construction succeeds and the module handles all seven stages of the provided plan

**M-02 Invalid rules stop PBS at startup** — FR-02 AC1
- Given a rule set with an empty partner id, a non-positive duration, a non-positive amount, or a partner id listed twice
- When the module is constructed
- Then construction fails and the error names the offending rule

**M-03 An empty rule set is valid and traces nothing** — FR-02 AC2
- Given no rules
- When the module is constructed and an auction runs for any account
- Then construction succeeds and nothing is written

**M-04 The shipped rules are valid and match the sample request** — FR-02, FR-03 AC1
- Given the hardcoded rules
- Then they pass validation, and one of them lists the account the sample request resolves to (`664-025-677-881`)

## Trigger

**M-05 A traced partner starts a trace, any other account does not** — FR-03 AC1, FR-13 AC1
- Given a rule for partner P
- When an auction runs for P, and another for an account without a rule
- Then the first auction is traced and the second is not

**M-06 Only requests that reach processed_auction_request are traced** — FR-03 AC2, AC3
- Given a traced partner
- When later stages run for a request that never passed `processed_auction_request`
- Then nothing is recorded and nothing is written

## Collected data

**M-07 Item 1 is the body PBS received, with its entrypoint time** — FR-04 AC1
- Given a traced auction whose entrypoint body is B at time T
- Then the packet's incoming request is B with timestamp T

**M-08 Item 1 is a copy** — FR-04 AC2
- Given a traced auction
- When the entrypoint body is modified after the hook returned
- Then the packet still holds the original bytes

**M-09 Item 1 falls back to the processed request** — FR-04 AC3
- Given a traced auction without an entrypoint capture, or with a body that is not JSON
- Then the incoming request is the processed request at its stage time, or the raw body as a JSON string; if even that cannot be
  encoded the trace continues without item 1

**M-10 Item 2 is a snapshot per bidder at hook time** — FR-05 AC1
- Given a traced auction with bidders A and B
- When the request objects are modified after their `bidder_request` hooks
- Then the packet holds one entry per bidder with the bidder's name, the per-bidder request as the hook exposed it, and the hook time

**M-11 Item 2 keeps invocation order** — FR-05 AC2
- Given bidder hooks invoked in order A, B, C
- Then the entries appear in that order

**M-12 Untraced requests record nothing** — FR-05 AC3, FR-13 AC1
- Given an auction that is not traced
- When its bidder hooks run
- Then no entry is recorded and the hook result carries no change

**M-13 Item 3 is a snapshot of each bidder response** — FR-06 AC1
- Given a traced auction and a bidder response with bids, optional bid metadata, video data and FLEDGE configs
- Then the packet holds the bidder's name, the hook time and every field of the response in the contract's shape

**M-14 A bidder without a response still yields a packet** — FR-06 AC2
- Given a traced auction in which one bidder never reaches `raw_bidder_response`
- Then the packet is written without an entry for that bidder

**M-15 Item 4 is the response the client receives** — FR-07 AC1, AC2, AC3
- Given a traced auction
- When `auction_response` sees response R1 and `exitpoint` sees response R2
- Then the packet holds R2 with the exitpoint time, including its `ext`; when the `exitpoint` payload is not a bid response, R1 is kept

## Output

**M-16 One JSON line per traced auction, in the contract's shape** — FR-08 AC1, AC2
- Given a traced auction
- When `exitpoint` runs
- Then exactly one line is written in a single write: a JSON object with the contract's keys, embedded requests and responses as
  JSON values, bidder arrays `[]` when empty

**M-35 Output is asynchronous, bounded and drained on shutdown** — FR-08 AC1a, AC1b
- Given the production output path with a queue of N packets and a stdout that does not drain
- When more than N packets are emitted
- Then every emit returns at once; the packets beyond the queue are dropped and counted; once stdout drains again the queued
  packets are written in order; shutdown waits for the queue to empty, at most 5 s, and later emits are refused

**M-17 A packet is written once** — FR-08 AC3
- Given a traced auction whose packet was written
- When `exitpoint` runs again for it
- Then nothing more is written

**M-18 Nothing is written for untraced auctions** — FR-08 AC4, FR-13 AC1
- Given an auction that is not traced
- Then nothing is written

**M-19 Timestamps are RFC 3339 with nanoseconds, in UTC** — FR-08 AC5
- Given a clock in a non-UTC zone
- Then every timestamp in the packet is UTC with nanosecond precision

## Stop conditions

**M-20 Time limit, boundary included, window between arrivals** — FR-09 AC1, AC2
- Given a rule with duration D and a first traced auction whose incoming request arrived at T and was triggered later
- Then the window opens at T, not at the trigger time; an auction that arrived at T + D is traced even if triggered later, one that
  arrived at T + D + 1 ns is not, and the partner is stopped for duration

**M-21 Amount limit** — FR-10 AC1, AC3
- Given a rule with amount 2
- When three auctions run for the partner
- Then the first two are traced, the third is not, and the partner is stopped for amount

**M-22 The amount holds under concurrency** — FR-10 AC2, FR-16 AC1
- Given a rule with amount N
- When many more than N auctions start concurrently
- Then exactly N are traced

**M-23 Whichever limit comes first; no re-arm; partners independent** — FR-11 AC1, AC2
- Given two partners
- When one reaches either limit
- Then it is never traced again, whatever the clock does (except through M-36 while its window is open), and the other partner is
  unaffected

**M-24 Traces in flight complete** — FR-12 AC1
- Given a trace started before its partner stopped
- When the partner stops while the auction is still running
- Then the packet is still written at `exitpoint`

**M-25 Beyond the limit, auctions pass through the real executor unobserved** — FR-10 AC1, FR-13 AC1
- Given PBS's hook executor with the provided plan and a partner whose amount is used up
- When another auction runs for it
- Then nothing is written and every hook outcome is a success

**M-36 A slot of an auction that never completed is given back after its lease** — FR-10 AC4, FR-12
- Given a partner with amount 1 whose only trace started but never reached `exitpoint`
- When a matching request arrives within 5 minutes, then another after 5 minutes
- Then the first is refused and the second is traced with packet index 1; a trace whose packet was written keeps its slot for good

## Scope and safety

**M-26 Only the auction endpoint is observed** — FR-14 AC1
- Given a traced partner
- When the hooks are invoked for another endpoint, also in the middle of a traced auction
- Then nothing is recorded, the active trace is untouched, and nothing is written

**M-27 The module never rejects or mutates** — FR-15 AC1
- Given any auction, traced or not
- Then every hook result has no rejection, no no-bid reason and an empty change set

**M-28 Internal failures are logged, not returned** — FR-15 AC2
- Given a payload that cannot be encoded, or an output that fails to write
- Then the hook returns success, the failure is logged, and the auction is unaffected

**M-29 Missing or unexpected inputs are safe** — FR-15 AC3
- Given a missing module context, missing payload fields, or payload types other than expected
- Then no hook panics and no hook fails; a panic raised inside a hook is recovered, logged and turned into a successful empty result

## Memory

**M-33 The entrypoint copy of an untraced request is released at the trigger decision** — NFR-02
- Given a request whose account has no rule, or whose partner is stopped
- When `processed_auction_request` runs
- Then the copy taken at `entrypoint` is no longer held by the request's module context

**M-34 No body is copied once every partner is stopped** — NFR-01
- Given every partner stopped by amount with every slot confirmed, or by duration, or an empty rule set
- When `entrypoint` runs for any request
- Then no context is created and the body is not copied

**M-37 No body is copied once every window has closed** — NFR-01
- Given every partner has started and the latest window end among them has passed
- When `entrypoint` runs for any request
- Then no body is copied, even though no partner has been refused yet

**M-38 No traced auction stays in memory after its request** — NFR-02, FR-10 AC4
- Given a traced partner with one auction that completed and one abandoned after the trigger (its `exitpoint` never runs)
- When both requests have let go of their auctions, and later the partner's window closes
- Then the data collected for neither auction is still held by the module; the abandoned auction's slot still counts until its
  lease ends, and a partner stopped for good holds no reservations

## Concurrency

**M-30 Concurrent hooks and requests are race-free** — FR-16 AC1
- Given parallel bidder hooks of one auction, and many auctions for several partners at once
- When run under the race detector
- Then no race is reported and every packet holds all its entries

**M-31 Concurrent writes never interleave** — FR-16 AC2
- Given many traced auctions reaching `exitpoint` at once
- Then every output line is one complete packet

## Integration

**M-32 The sample request through PBS's hook executor** — FR-01 AC1, FR-03 AC1, FR-04 … FR-08, FR-15 AC1
- Given PBS's hook executor and plan builder with the stage list of the provided `pbs.yaml`, and the sample request
- When the stages run in PBS order for the sample account with four bidders
- Then exactly one packet is written with all four items, and every hook outcome is a success without errors
