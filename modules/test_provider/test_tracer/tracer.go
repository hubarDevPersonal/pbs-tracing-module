package testtracer

import (
	"sync"
	"sync/atomic"
	"time"
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
