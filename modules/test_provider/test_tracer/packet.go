package testtracer

import (
	"encoding/json"
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
