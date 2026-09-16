package testtracer

import (
	"sync"
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
	now      func() time.Time
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
	// TODO(impl)
	return &Tracer{now: now}, nil
}

// Begin decides whether a new auction for partnerID must be traced. On success it reserves a
// packet slot (D11) and returns the per-request collector. ok == false means "do not trace".
func (t *Tracer) Begin(partnerID, auctionID string) (*AuctionTrace, bool) {
	// TODO(impl)
	return nil, false
}

// Status returns the partner state; found == false when the partner has never matched a rule.
func (t *Tracer) Status(partnerID string) (status PartnerStatus, found bool) {
	// TODO(impl)
	return PartnerStatus{}, false
}

// AuctionTrace collects the four kinds of trace data for one auction. It is shared between the
// concurrent per-bidder hooks of the same request and is therefore mutex-guarded.
// All inputs are snapshotted (marshalled or copied) at call time.
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

// SetIncomingRequest stores a copy of the raw incoming body and its timestamp (FR-04).
func (a *AuctionTrace) SetIncomingRequest(at time.Time, body []byte) {
	// TODO(impl)
}

// AddBidderRequest appends a snapshot of the request sent to bidder (FR-05).
func (a *AuctionTrace) AddBidderRequest(at time.Time, bidder string, req *openrtb2.BidRequest) error {
	// TODO(impl)
	return nil
}

// AddBidderResponse appends a snapshot of the response received from bidder (FR-06).
func (a *AuctionTrace) AddBidderResponse(at time.Time, bidder string, resp *adapters.BidderResponse) error {
	// TODO(impl)
	return nil
}

// SetFinalResponse stores a snapshot of the response returned to the client (FR-07).
func (a *AuctionTrace) SetFinalResponse(at time.Time, resp *openrtb2.BidResponse) error {
	// TODO(impl)
	return nil
}

// Packet builds the output object. It never returns nil slices (FR-08 / spec §5).
func (a *AuctionTrace) Packet(completedAt time.Time) TracePacket {
	// TODO(impl)
	return TracePacket{}
}
