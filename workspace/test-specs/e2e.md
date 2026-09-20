# End-to-end scenarios (E)

Level: Prebid Server built at the pinned commit with the module compiled in, the provided `pbs.yaml` unchanged, run as a container;
the test sends HTTP requests and reads the container's stdout (trace) and stderr (PBS log). Bidders are live.

Requests: the **sample** request is `workspace/assessment/01-bid-request-example.json` (account `664-025-677-881`, rule amount 3; its four bidders answer
204). The **live-bid** request is the sample plus onetag's test publisher (account `33415-10498`, rule amount 1; onetag returns a test bid).

**E-01 The module is registered and all its hooks succeed** — FR-01 AC2, FR-15 AC1, AC2
- Given a freshly started server
- When any auction is sent with debug enabled
- Then the PBS log has no "Not found hook" warning for the module, and the response's hook trace lists the module's hooks, all with
  status success

**E-02 The sample partner is traced up to its amount** — FR-03 AC1, FR-10 AC1, FR-08 AC1
- Given a freshly started server
- When the sample request is sent more often than its rule's amount
- Then exactly `amount` packets appear on stdout, for partner `664-025-677-881`, with packet indices exactly 1 … amount

**E-03 Every packet carries items 1, 2 and 4** — FR-04 AC1, FR-05 AC1, FR-07 AC2, AC3
- Given the packets of E-02
- Then the incoming request has the sample's id and precedes the trace start; there is one bidder request for each of the four
  bidders, each with the sample's id and not earlier than the incoming request; the final response has the sample's id and contains
  the debug extension the client received

**E-04 An account without a rule is not traced** — FR-13 AC1
- Given the server of E-02
- When the sample request is sent with an account that has no rule
- Then the auction succeeds and no packet is added

**E-05 A live bidder response is traced (item 3) and the one-packet rule holds** — FR-06 AC1, FR-10 AC1
- Given the server after E-02
- When the live-bid request is sent twice
- Then exactly one packet is added, for partner `33415-10498`, with bidder requests for all five bidders and at least one bidder response

**E-06 stdout carries nothing but trace packets** — FR-08 AC1, AC2
- Given the whole run
- Then every line on stdout decodes as a trace packet
