package testtracer

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
)

// AuctionTrace collects the four kinds of trace data for one auction. It is shared between the
// concurrent per-bidder hooks of the same request and is therefore mutex-guarded.
// All inputs are snapshotted (marshaled or copied) at call time.
type AuctionTrace struct {
	mu              sync.Mutex
	partnerID       string
	rule            Rule
	packetIndex     int
	auctionID       string
	startedAt       time.Time
	slot            *reservation // the partner's slot this trace holds; also records that the packet was emitted
	incoming        *RequestPacket
	bidderRequests  []BidderRequestPacket
	bidderResponses []BidderResponsePacket
	final           *ResponsePacket
	auctionResp     *openrtb2.BidResponse // seen at auction_response, marshaled only as a fallback
	auctionRespAt   time.Time
}

// PartnerID returns the partner the trace belongs to.
func (a *AuctionTrace) PartnerID() string { return a.partnerID }

// AuctionID returns the BidRequest.id the trace was started with.
func (a *AuctionTrace) AuctionID() string { return a.auctionID }

// PacketIndex returns the 1-based index of this packet within the partner's window.
func (a *AuctionTrace) PacketIndex() int { return a.packetIndex }

// StartedAt returns the trigger time (processed_auction_request) recorded by Tracer.Begin.
func (a *AuctionTrace) StartedAt() time.Time { return a.startedAt }

// SetIncomingRequest stores the raw incoming body and its timestamp (FR-04). The trace takes
// ownership of body: callers pass the copy taken at entrypoint or a freshly marshaled buffer.
// A body that is not valid JSON is embedded as a JSON string so the packet stays well-formed.
func (a *AuctionTrace) SetIncomingRequest(at time.Time, body []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.incoming = &RequestPacket{Timestamp: at.UTC(), Body: rawJSON(body)}
}

func (a *AuctionTrace) hasIncomingRequest() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.incoming != nil
}

// AddBidderRequest appends a snapshot of the request sent to bidder (FR-05).
func (a *AuctionTrace) AddBidderRequest(at time.Time, bidder string, req *openrtb2.BidRequest) error {
	if req == nil {
		return errors.New("bidder request is nil")
	}
	raw, err := json.Marshal(req) // outside the lock (design §5)
	if err != nil {
		return fmt.Errorf("marshal bidder request for %q: %w", bidder, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bidderRequests = append(a.bidderRequests, BidderRequestPacket{Timestamp: at.UTC(), Bidder: bidder, Request: raw})
	return nil
}

// AddBidderResponse appends a snapshot of the response received from bidder (FR-06).
func (a *AuctionTrace) AddBidderResponse(at time.Time, bidder string, resp *adapters.BidderResponse) error {
	if resp == nil {
		return errors.New("bidder response is nil")
	}
	view, err := snapshotBidderResponse(resp)
	if err != nil {
		return fmt.Errorf("snapshot bidder response for %q: %w", bidder, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bidderResponses = append(a.bidderResponses, BidderResponsePacket{Timestamp: at.UTC(), Bidder: bidder, Response: view})
	return nil
}

// SetFinalResponse stores a snapshot of the response returned to the client (FR-07).
func (a *AuctionTrace) SetFinalResponse(at time.Time, resp *openrtb2.BidResponse) error {
	if resp == nil {
		return errors.New("final response is nil")
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal final response: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.final = &ResponsePacket{Timestamp: at.UTC(), Body: raw}
	return nil
}

// RememberAuctionResponse keeps the response seen at auction_response and its time (FR-07 AC1);
// it is marshaled at exitpoint only if the exitpoint payload is not a bid response, so the
// common path marshals the final response once.
func (a *AuctionTrace) RememberAuctionResponse(at time.Time, resp *openrtb2.BidResponse) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.auctionResp, a.auctionRespAt = resp, at
}

// AuctionResponse returns what RememberAuctionResponse kept; resp is nil if nothing was.
func (a *AuctionTrace) AuctionResponse() (resp *openrtb2.BidResponse, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.auctionResp, a.auctionRespAt
}

func (a *AuctionTrace) isEmitted() bool { return a.slot.emitted.Load() }

// tryMarkEmitted flips the emitted flag; it returns true only for the first caller (FR-08 AC3).
func (a *AuctionTrace) tryMarkEmitted() bool { return a.slot.emitted.CompareAndSwap(false, true) }

// Packet builds the output object. It never returns nil slices (FR-08 / spec §5).
func (a *AuctionTrace) Packet(completedAt time.Time) TracePacket {
	a.mu.Lock()
	defer a.mu.Unlock()

	reqs := append(make([]BidderRequestPacket, 0, len(a.bidderRequests)), a.bidderRequests...)
	resps := append(make([]BidderResponsePacket, 0, len(a.bidderResponses)), a.bidderResponses...)

	return TracePacket{
		Module:    ModuleCode,
		PartnerID: a.partnerID,
		Rule: RuleView{
			PartnerID:          a.rule.PartnerID,
			Duration:           a.rule.Duration.String(),
			TracePacketsAmount: a.rule.TracePacketsAmount,
		},
		PacketIndex:     a.packetIndex,
		AuctionID:       a.auctionID,
		StartedAt:       a.startedAt.UTC(),
		CompletedAt:     completedAt.UTC(),
		IncomingRequest: a.incoming,
		BidderRequests:  reqs,
		BidderResponses: resps,
		FinalResponse:   a.final,
	}
}

// snapshotBidderResponse converts adapters.BidderResponse into the explicit DTO (D10) and deep-copies
// it through a marshal/unmarshal round trip so later mutations by the exchange are not observed.
func snapshotBidderResponse(resp *adapters.BidderResponse) (BidderResponseView, error) {
	view := BidderResponseView{Currency: resp.Currency, Bids: make([]TypedBidView, 0, len(resp.Bids))}
	for _, tb := range resp.Bids {
		if tb == nil {
			continue
		}
		v := TypedBidView{
			Bid:          tb.Bid,
			BidType:      string(tb.BidType),
			DealPriority: tb.DealPriority,
			Seat:         string(tb.Seat),
		}
		if tb.BidMeta != nil {
			raw, err := json.Marshal(tb.BidMeta)
			if err != nil {
				return BidderResponseView{}, fmt.Errorf("marshal bid meta: %w", err)
			}
			v.BidMeta = raw
		}
		if tb.BidVideo != nil {
			raw, err := json.Marshal(tb.BidVideo)
			if err != nil {
				return BidderResponseView{}, fmt.Errorf("marshal bid video: %w", err)
			}
			v.BidVideo = raw
		}
		view.Bids = append(view.Bids, v)
	}
	if len(resp.FledgeAuctionConfigs) > 0 {
		raw, err := json.Marshal(resp.FledgeAuctionConfigs)
		if err != nil {
			return BidderResponseView{}, fmt.Errorf("marshal fledge auction configs: %w", err)
		}
		view.FledgeAuctionConfigs = raw
	}

	// deep copy: detach from the pointers owned by the exchange
	raw, err := json.Marshal(view)
	if err != nil {
		return BidderResponseView{}, err
	}
	var detached BidderResponseView
	if err := json.Unmarshal(raw, &detached); err != nil {
		return BidderResponseView{}, err
	}
	if detached.Bids == nil {
		detached.Bids = []TypedBidView{}
	}
	return detached, nil
}

// rawJSON returns body as an embedded JSON value; non-JSON bodies are wrapped into a JSON string.
func rawJSON(body []byte) json.RawMessage {
	if len(body) == 0 {
		return json.RawMessage("null")
	}
	if json.Valid(body) {
		return json.RawMessage(body)
	}
	quoted, err := json.Marshal(string(body))
	if err != nil {
		return json.RawMessage("null")
	}
	return quoted
}
