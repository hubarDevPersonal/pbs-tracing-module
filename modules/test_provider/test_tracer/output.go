package testtracer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
)

// TracePacket is the JSON contract printed to stdout, one object per line (spec §5).
type TracePacket struct {
	Module          string                 `json:"module"`
	PartnerID       string                 `json:"partner_id"`
	Rule            RuleView               `json:"rule"`
	PacketIndex     int                    `json:"packet_index"`
	AuctionID       string                 `json:"auction_id"`
	StartedAt       time.Time              `json:"started_at"`
	CompletedAt     time.Time              `json:"completed_at"`
	IncomingRequest *RequestPacket         `json:"incoming_request"`
	BidderRequests  []BidderRequestPacket  `json:"bidder_requests"`
	BidderResponses []BidderResponsePacket `json:"bidder_responses"`
	FinalResponse   *ResponsePacket        `json:"final_response"`
}

// RuleView is the printable form of a Rule.
type RuleView struct {
	PartnerID          string `json:"partner_id"`
	Duration           string `json:"duration"`
	TracePacketsAmount int    `json:"trace_packets_amount"`
}

// RequestPacket is item 1: the incoming BidRequest and its timestamp.
type RequestPacket struct {
	Timestamp time.Time       `json:"timestamp"`
	Body      json.RawMessage `json:"body"`
}

// BidderRequestPacket is item 2: the outgoing BidRequest to one bidder.
type BidderRequestPacket struct {
	Timestamp time.Time       `json:"timestamp"`
	Bidder    string          `json:"bidder"`
	Request   json.RawMessage `json:"request"`
}

// BidderResponsePacket is item 3: the BidResponse received from one bidder.
type BidderResponsePacket struct {
	Timestamp time.Time          `json:"timestamp"`
	Bidder    string             `json:"bidder"`
	Response  BidderResponseView `json:"response"`
}

// BidderResponseView is an explicit DTO for adapters.BidderResponse, which has no JSON tags (D10).
type BidderResponseView struct {
	Currency             string          `json:"currency"`
	Bids                 []TypedBidView  `json:"bids"`
	FledgeAuctionConfigs json.RawMessage `json:"fledge_auction_configs,omitempty"`
}

// TypedBidView is an explicit DTO for adapters.TypedBid.
type TypedBidView struct {
	Bid          *openrtb2.Bid   `json:"bid"`
	BidType      string          `json:"bid_type"`
	BidMeta      json.RawMessage `json:"bid_meta,omitempty"`
	BidVideo     json.RawMessage `json:"bid_video,omitempty"`
	DealPriority int             `json:"deal_priority"`
	Seat         string          `json:"seat"`
}

// ResponsePacket is item 4: the final auction response and its timestamp.
type ResponsePacket struct {
	Timestamp time.Time       `json:"timestamp"`
	Body      json.RawMessage `json:"body"`
}

// Emitter writes trace packets. The production implementation writes NDJSON to stdout.
type Emitter interface {
	Emit(packet TracePacket) error
}

// ErrEmitterClosed is returned by Emit after Close.
var ErrEmitterClosed = errors.New("emitter is closed")

// ErrQueueFull is returned by asyncEmitter.Emit when the packet had to be dropped.
var ErrQueueFull = errors.New("trace queue is full, packet dropped")

// defaultQueueSize bounds the packets waiting for stdout. Each packet holds the full auction (tens of
// KB with debug responses), so the queue is kept small; the rules already cap how many exist.
const defaultQueueSize = 64

// asyncEmitter decouples the hooks from stdout. Emit enqueues and returns at once; one goroutine drains
// the queue into next. A stalled stdout therefore never delays an auction response (NFR-01): the
// queue fills up and further packets are dropped, counted and logged, instead of blocking exitpoint.
// Close, called from the module's Shutdown, waits for the queue to drain.
type asyncEmitter struct {
	next    Emitter
	queue   chan TracePacket
	done    chan struct{}
	dropped atomic.Int64

	// mu orders Emit against Close: Emit holds it shared while it sends, Close holds it exclusively
	// while it closes the channel, so a hook still running during shutdown can never send on a
	// closed channel. Uncontended in normal operation.
	mu     sync.RWMutex
	closed bool
}

func newAsyncEmitter(next Emitter, queueSize int) *asyncEmitter {
	e := &asyncEmitter{next: next, queue: make(chan TracePacket, queueSize), done: make(chan struct{})}
	go e.drain()
	return e
}

func (e *asyncEmitter) drain() {
	defer close(e.done)
	for p := range e.queue {
		if err := e.next.Emit(p); err != nil {
			warnf("auction %s: %v", p.AuctionID, err)
		}
	}
}

// Emit enqueues the packet without blocking. It returns ErrQueueFull when the queue is full and the
// packet was dropped, or ErrEmitterClosed after Close.
func (e *asyncEmitter) Emit(packet TracePacket) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return ErrEmitterClosed
	}
	select {
	case e.queue <- packet:
		return nil
	default:
		return fmt.Errorf("%w (%d dropped so far)", ErrQueueFull, e.dropped.Add(1))
	}
}

// Dropped returns the number of packets dropped because the queue was full.
func (e *asyncEmitter) Dropped() int64 { return e.dropped.Load() }

// Close stops accepting packets and waits until the queued ones are written. Idempotent.
func (e *asyncEmitter) Close() error {
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		close(e.queue)
	}
	e.mu.Unlock()
	<-e.done
	return nil
}

// jsonEmitter writes one JSON object per line to w with a single Write call per packet, so
// concurrent emits from different requests never interleave (FR-08, FR-16).
type jsonEmitter struct {
	mu sync.Mutex
	w  io.Writer
}

func newJSONEmitter(w io.Writer) *jsonEmitter {
	return &jsonEmitter{w: w}
}

// Emit marshals packet, appends '\n' and writes it atomically with respect to other Emit calls.
// All timestamps are normalised to UTC (FR-08 AC5).
func (e *jsonEmitter) Emit(packet TracePacket) error {
	raw, err := json.Marshal(packet.utc())
	if err != nil {
		return fmt.Errorf("%s: marshal trace packet: %w", ModuleCode, err)
	}
	raw = append(raw, '\n')

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.w.Write(raw); err != nil {
		return fmt.Errorf("%s: write trace packet: %w", ModuleCode, err)
	}
	return nil
}

// utc returns a copy of the packet with every timestamp converted to UTC.
func (p TracePacket) utc() TracePacket {
	out := p
	out.StartedAt = p.StartedAt.UTC()
	out.CompletedAt = p.CompletedAt.UTC()
	if p.IncomingRequest != nil {
		c := *p.IncomingRequest
		c.Timestamp = c.Timestamp.UTC()
		out.IncomingRequest = &c
	}
	if p.FinalResponse != nil {
		c := *p.FinalResponse
		c.Timestamp = c.Timestamp.UTC()
		out.FinalResponse = &c
	}
	out.BidderRequests = make([]BidderRequestPacket, 0, len(p.BidderRequests))
	for _, b := range p.BidderRequests {
		b.Timestamp = b.Timestamp.UTC()
		out.BidderRequests = append(out.BidderRequests, b)
	}
	out.BidderResponses = make([]BidderResponsePacket, 0, len(p.BidderResponses))
	for _, b := range p.BidderResponses {
		b.Timestamp = b.Timestamp.UTC()
		out.BidderResponses = append(out.BidderResponses, b)
	}
	return out
}
