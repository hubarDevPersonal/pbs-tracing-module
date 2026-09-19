package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"
)

// moduleCode is the module id every trace packet and hook outcome carries.
const moduleCode = "test_provider.test_tracer"

// notFoundHookWarning is what PBS logs per planned stage when the module is configured but not compiled in.
const notFoundHookWarning = "Not found hook while building hook execution plan: " + moduleCode

// packet is the trace JSON contract (workspace/02-specification.md §5), decoded independently of the module's
// own types so the e2e suite checks the contract, not the implementation.
type packet struct {
	Module          string    `json:"module"`
	PartnerID       string    `json:"partner_id"`
	PacketIndex     int       `json:"packet_index"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at"`
	IncomingRequest *struct {
		Timestamp time.Time       `json:"timestamp"`
		Body      json.RawMessage `json:"body"`
	} `json:"incoming_request"`
	BidderRequests []struct {
		Timestamp time.Time       `json:"timestamp"`
		Bidder    string          `json:"bidder"`
		Request   json.RawMessage `json:"request"`
	} `json:"bidder_requests"`
	BidderResponses []struct {
		Bidder   string `json:"bidder"`
		Response struct {
			Bids []json.RawMessage `json:"bids"`
		} `json:"response"`
	} `json:"bidder_responses"`
	FinalResponse *struct {
		Body json.RawMessage `json:"body"`
	} `json:"final_response"`
}

// expectation is what one phase of the e2e run must find in the trace.
type expectation struct {
	Packets    int      // exact number of packets
	PartnerID  string   // partner_id of every packet
	AuctionID  string   // BidRequest.id of the incoming request, every bidder request and the final response
	Bidders    []string // exact set of bidders in bidder_requests
	StrictBids bool     // at least one bidder response across all packets (item 3 exercised)
}

// verifyTrace checks NDJSON trace output against want and returns the number of bidder responses seen.
func verifyTrace(ndjson []byte, want expectation) (int, error) {
	packets, err := decodePackets(ndjson)
	if err != nil {
		return 0, err
	}
	if len(packets) != want.Packets {
		return 0, fmt.Errorf("expected %d trace packets, got %d", want.Packets, len(packets))
	}

	responses := 0
	indices := map[int]bool{}
	for i, p := range packets {
		if err := checkPacket(p, want); err != nil {
			return 0, fmt.Errorf("packet %d: %w", i+1, err)
		}
		if indices[p.PacketIndex] {
			return 0, fmt.Errorf("packet %d: duplicate packet_index %d", i+1, p.PacketIndex)
		}
		indices[p.PacketIndex] = true
		responses += len(p.BidderResponses)
	}
	// Packets are written in completion order, so only the set of indices is fixed: exactly 1..N.
	for n := 1; n <= len(packets); n++ {
		if !indices[n] {
			return 0, fmt.Errorf("packet_index %d missing, got %v", n, slices.Sorted(maps.Keys(indices)))
		}
	}
	if want.StrictBids && responses == 0 {
		return 0, errors.New("no bidder response recorded in any packet")
	}
	return responses, nil
}

func decodePackets(ndjson []byte) ([]packet, error) {
	var packets []packet
	sc := bufio.NewScanner(bytes.NewReader(ndjson))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for line := 1; sc.Scan(); line++ {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var p packet
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("stdout line %d is not a trace packet: %w", line, err)
		}
		packets = append(packets, p)
	}
	return packets, sc.Err()
}

func checkPacket(p packet, want expectation) error {
	switch {
	case p.Module != moduleCode:
		return fmt.Errorf("module %q, want %q", p.Module, moduleCode)
	case p.PartnerID != want.PartnerID:
		return fmt.Errorf("partner_id %q, want %q", p.PartnerID, want.PartnerID)
	case p.PacketIndex < 1:
		return fmt.Errorf("packet_index %d must be ≥ 1", p.PacketIndex)
	case p.StartedAt.IsZero() || p.CompletedAt.Before(p.StartedAt):
		return fmt.Errorf("completed_at %s precedes started_at %s", p.CompletedAt, p.StartedAt)
	case p.IncomingRequest == nil:
		return errors.New("incoming_request missing")
	case p.FinalResponse == nil:
		return errors.New("final_response missing")
	}

	// item 1
	if err := checkID("incoming_request.body", p.IncomingRequest.Body, want.AuctionID); err != nil {
		return err
	}
	if p.IncomingRequest.Timestamp.After(p.StartedAt) {
		return errors.New("incoming_request.timestamp is after started_at")
	}
	// item 2
	bidders := map[string]bool{}
	for _, b := range p.BidderRequests {
		bidders[b.Bidder] = true
		if err := checkID("bidder_requests["+b.Bidder+"].request", b.Request, want.AuctionID); err != nil {
			return err
		}
		if b.Timestamp.Before(p.IncomingRequest.Timestamp) {
			return fmt.Errorf("bidder_requests[%s] timestamp precedes the incoming request", b.Bidder)
		}
	}
	if got := slices.Sorted(maps.Keys(bidders)); !slices.Equal(got, slices.Sorted(slices.Values(want.Bidders))) {
		return fmt.Errorf("bidder_requests bidders %v, want %v", got, want.Bidders)
	}
	// item 3: presence depends on live bidders (StrictBids), shape is always checked
	for _, b := range p.BidderResponses {
		if !bidders[b.Bidder] {
			return fmt.Errorf("bidder_responses has bidder %q without a bidder request", b.Bidder)
		}
		if b.Response.Bids == nil {
			return fmt.Errorf("bidder_responses[%s].response.bids must be an array", b.Bidder)
		}
	}
	// item 4: the response the client received, i.e. enriched with ext.debug (the request asks for debug)
	if err := checkID("final_response.body", p.FinalResponse.Body, want.AuctionID); err != nil {
		return err
	}
	var final struct {
		Ext struct {
			Debug json.RawMessage `json:"debug"`
		} `json:"ext"`
	}
	if err := json.Unmarshal(p.FinalResponse.Body, &final); err != nil || len(final.Ext.Debug) == 0 {
		return errors.New("final_response.body.ext.debug missing: not the response the client received")
	}
	return nil
}

func checkID(field string, raw json.RawMessage, want string) error {
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("%s is not a JSON object: %w", field, err)
	}
	if v.ID != want {
		return fmt.Errorf("%s.id %q, want %q", field, v.ID, want)
	}
	return nil
}

// checkHookOutcomes requires the auction response's hook trace (ext.prebid.modules.trace, present when the
// request enables debug) to list the module's hooks, all with status "success".
func checkHookOutcomes(response []byte) error {
	var resp struct {
		Ext struct {
			Prebid struct {
				Modules struct {
					Trace *struct {
						Stages []struct {
							Outcomes []struct {
								Groups []struct {
									InvocationResults []struct {
										HookID struct {
											ModuleCode string `json:"module_code"`
										} `json:"hook_id"`
										Status string `json:"status"`
									} `json:"invocation_results"`
								} `json:"groups"`
							} `json:"outcomes"`
						} `json:"stages"`
					} `json:"trace"`
				} `json:"modules"`
			} `json:"prebid"`
		} `json:"ext"`
	}
	if err := json.Unmarshal(response, &resp); err != nil {
		return fmt.Errorf("auction response is not JSON: %w", err)
	}
	trace := resp.Ext.Prebid.Modules.Trace
	if trace == nil {
		return errors.New("ext.prebid.modules.trace missing: hooks did not run or debug is off")
	}
	invocations := 0
	for _, stage := range trace.Stages {
		for _, outcome := range stage.Outcomes {
			for _, group := range outcome.Groups {
				for _, r := range group.InvocationResults {
					if r.HookID.ModuleCode != moduleCode {
						continue
					}
					invocations++
					if r.Status != "success" {
						return fmt.Errorf("hook of %s finished with status %q", moduleCode, r.Status)
					}
				}
			}
		}
	}
	if invocations == 0 {
		return fmt.Errorf("no hook of %s in ext.prebid.modules.trace", moduleCode)
	}
	return nil
}

// checkPBSLog fails when PBS logged that the planned module is not registered.
func checkPBSLog(log []byte) error {
	if bytes.Contains(log, []byte(notFoundHookWarning)) {
		return errors.New("PBS log: " + notFoundHookWarning + ": module not compiled in or disabled")
	}
	return nil
}
