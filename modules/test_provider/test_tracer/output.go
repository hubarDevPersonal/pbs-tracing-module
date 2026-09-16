package testtracer

import (
	"encoding/json"
	"io"
	"sync"
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
func (e *jsonEmitter) Emit(packet TracePacket) error {
	// TODO(impl)
	return nil
}
