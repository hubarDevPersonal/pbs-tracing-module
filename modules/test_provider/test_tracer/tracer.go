package testtracer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
)

// StopReason explains why tracing for a partner has stopped.
type StopReason string

const (
	StopReasonNone     StopReason = ""
	StopReasonDuration StopReason = "duration_exceeded"
	StopReasonAmount   StopReason = "amount_reached"
)

// Tracer owns the process-wide, per-partner tracing state and applies the trigger and stop
// conditions (FR-03, FR-09..FR-11). It is safe for concurrent use.
type Tracer struct {
	mu       sync.Mutex
	rules    map[string]Rule
	partners map[string]*partnerState
	stopped  int // partners in a terminal state; when it equals len(rules) nothing can be traced any more
	now      func() time.Time

	// exhausted is set once every partner is stopped. It is read lock-free on the entrypoint of every
	// request so that the body copy, the module's main cost on untraced traffic, can be skipped.
	exhausted atomic.Bool
}

type partnerState struct {
	firstTracedAt time.Time
	packets       int
	stopReason    StopReason
}

// PartnerStatus is a read-only view of a partner's state for tests and diagnostics.
type PartnerStatus struct {
	Packets       int
	FirstTracedAt time.Time
	StopReason    StopReason
}

// newTracer validates the rules and returns a Tracer using the given clock.
func newTracer(rules []Rule, now func() time.Time) (*Tracer, error) {
	if err := validateRules(rules); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	t := &Tracer{
		rules:    make(map[string]Rule, len(rules)),
		partners: make(map[string]*partnerState, len(rules)),
		now:      now,
	}
	for _, r := range rules {
		t.rules[r.PartnerID] = r
	}
	t.exhausted.Store(len(rules) == 0)
	return t, nil
}

// Exhausted reports whether every partner is stopped, i.e. no future request can be traced.
func (t *Tracer) Exhausted() bool { return t.exhausted.Load() }

// stop moves a partner to a terminal state and flips exhausted when it was the last active one.
// Caller holds t.mu.
func (t *Tracer) stop(st *partnerState, reason StopReason) {
	st.stopReason = reason
	t.stopped++
	if t.stopped == len(t.rules) {
		t.exhausted.Store(true)
	}
}

// Begin decides whether a new auction for partnerID must be traced. On success it reserves a
// packet slot (D11) and returns the per-request collector. ok == false means "do not trace".
//
// incomingAt is the timestamp of the incoming BidRequest (its entrypoint time). The partner's
// window opens at the incomingAt of the first auction that reserves a slot (FR-09), and the
// duration check compares the current time against it. A zero incomingAt means the entrypoint
// capture is unavailable and the current time is used instead.
//
// The check-and-reserve sequence runs under one lock so that concurrent requests can never start
// more than TracePacketsAmount traces (FR-10 AC2). Concurrent first auctions of a partner open
// the window in lock order, so it starts at the incoming timestamp of whichever reserves first.
func (t *Tracer) Begin(partnerID, auctionID string, incomingAt time.Time) (*AuctionTrace, bool) {
	rule, ok := t.rules[partnerID]
	if !ok {
		return nil, false
	}
	now := t.now()
	if incomingAt.IsZero() {
		incomingAt = now
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.partners[partnerID]
	if st == nil {
		st = &partnerState{}
		t.partners[partnerID] = st
	}
	if st.stopReason != StopReasonNone {
		return nil, false
	}
	if st.packets == 0 {
		st.firstTracedAt = incomingAt
	} else if now.Sub(st.firstTracedAt) > rule.Duration { // FR-09: strictly "exceeds"
		t.stop(st, StopReasonDuration)
		return nil, false
	}
	if st.packets >= rule.TracePacketsAmount { // defensive; unreachable because of the mark below
		t.stop(st, StopReasonAmount)
		return nil, false
	}
	st.packets++
	if st.packets >= rule.TracePacketsAmount { // FR-10: the last slot has been taken
		t.stop(st, StopReasonAmount)
	}

	return &AuctionTrace{
		partnerID:       partnerID,
		rule:            rule,
		packetIndex:     st.packets,
		auctionID:       auctionID,
		startedAt:       now.UTC(),
		bidderRequests:  make([]BidderRequestPacket, 0, 4),
		bidderResponses: make([]BidderResponsePacket, 0, 4),
	}, true
}

// Status returns the partner state; found == false when the partner has never matched a rule.
func (t *Tracer) Status(partnerID string) (status PartnerStatus, found bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.partners[partnerID]
	if !ok {
		return PartnerStatus{}, false
	}
	return PartnerStatus{Packets: st.packets, FirstTracedAt: st.firstTracedAt, StopReason: st.stopReason}, true
}

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
	incoming        *RequestPacket
	bidderRequests  []BidderRequestPacket
	bidderResponses []BidderResponsePacket
	final           *ResponsePacket
	emitted         bool
}

// PartnerID returns the partner the trace belongs to.
func (a *AuctionTrace) PartnerID() string { return a.partnerID }

// AuctionID returns the BidRequest.id the trace was started with.
func (a *AuctionTrace) AuctionID() string { return a.auctionID }

// PacketIndex returns the 1-based index of this packet within the partner's window.
func (a *AuctionTrace) PacketIndex() int { return a.packetIndex }

// StartedAt returns the trigger time (processed_auction_request) recorded by Tracer.Begin.
func (a *AuctionTrace) StartedAt() time.Time { return a.startedAt }

// SetIncomingRequest stores a copy of the raw incoming body and its timestamp (FR-04).
// A body that is not valid JSON is embedded as a JSON string so the packet stays well-formed.
func (a *AuctionTrace) SetIncomingRequest(at time.Time, body []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.incoming = &RequestPacket{Timestamp: at.UTC(), Body: rawJSON(bytes.Clone(body))}
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

// tryMarkEmitted flips the emitted flag; it returns true only for the first caller (FR-08 AC3).
func (a *AuctionTrace) tryMarkEmitted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.emitted {
		return false
	}
	a.emitted = true
	return true
}

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
