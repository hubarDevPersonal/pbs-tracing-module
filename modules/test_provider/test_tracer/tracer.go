package testtracer

import (
	"slices"
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

// slotLease bounds how long a reserved packet slot may stay unconfirmed. A slot is reserved when a
// trace starts and confirmed when its packet is written at exitpoint. PBS runs no hook when it fails
// an auction with 4xx/5xx after the trigger, so such a trace never confirms; once its lease is over
// the slot can be given back, and the partner's amount counts collected packets again (FR-10, D13).
// The lease does not have to outlast every auction: PBS allows tmax up to auction_timeouts_ms.max, 10
// minutes in the provided pbs.yaml. A slot given back is revoked, and the auction that held it can no
// longer write its packet, so an auction still running past its lease costs its own packet, never the limit.
const slotLease = 5 * time.Minute

// Tracer owns the process-wide, per-partner tracing state and applies the trigger and stop
// conditions (FR-03, FR-09..FR-11). It is safe for concurrent use.
type Tracer struct {
	mu       sync.Mutex
	rules    map[string]Rule
	partners map[string]*partnerState
	now      func() time.Time

	// Both fields answer Exhausted without the lock, on the entrypoint of every request, so the
	// body copy, the module's main cost on untraced traffic, can be skipped once nothing can be
	// traced any more.
	exhausted atomic.Bool  // every partner is stopped for good and no slot can be given back
	deadline  atomic.Int64 // unix nanos after which every partner's window is closed; 0 = unknown
}

type partnerState struct {
	firstTracedAt time.Time
	packets       int            // slots taken: written packets plus unconfirmed reservations
	stopReason    StopReason     // StopReasonAmount is revoked when a lease expires
	pending       []*reservation // reserved and not yet confirmed
}

// reservation is one packet slot handed out to a trace. The tracer keeps reservations, never the traces
// themselves: a trace lives only in its request's module context, so its payload is released when the
// request ends, whether the packet was written or PBS abandoned the auction after the trigger (NFR-02).
type reservation struct {
	startedAt time.Time
	state     atomic.Int32 // slotPending, then slotEmitted or slotRevoked, once
}

// A slot leaves slotPending exactly once, by compare-and-swap: to slotEmitted when exitpoint claims it
// for the packet, or to slotRevoked when an expired lease gives it to another auction. The two swaps
// exclude each other, so every slot is written by at most one auction (FR-10 AC2, AC4).
const (
	slotPending int32 = iota
	slotEmitted
	slotRevoked
)

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

// Exhausted reports whether no request can be traced any more: every partner is stopped for good,
// or every partner's window has closed by now.
func (t *Tracer) Exhausted(now time.Time) bool {
	if t.exhausted.Load() {
		return true
	}
	d := t.deadline.Load()
	return d != 0 && now.UnixNano() > d
}

// Begin decides whether a new auction for partnerID must be traced. On success it reserves a
// packet slot (D11) and returns the per-request collector; the caller confirms the slot with
// Complete once the packet is written. ok == false means "do not trace".
//
// incomingAt is the timestamp of the incoming BidRequest (its entrypoint time). The partner's
// window opens at the incomingAt of the first auction that reserves a slot, and a later auction is
// refused when its own incomingAt is more than Duration after that (FR-09): the window is measured
// between request arrivals, as the trace reports them. A zero incomingAt means the entrypoint
// capture is unavailable and the current time stands in.
//
// The check-and-reserve sequence runs under one lock so that concurrent requests can never hold
// more than TracePacketsAmount slots (FR-10 AC2). Concurrent first auctions of a partner open the
// window in lock order, so it starts at the incoming timestamp of whichever reserves first.
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
	if st.stopReason == StopReasonDuration {
		return nil, false
	}
	if st.packets == 0 {
		st.firstTracedAt = incomingAt
	} else if incomingAt.Sub(st.firstTracedAt) > rule.Duration { // FR-09: strictly "exceeds"
		t.stop(st, StopReasonDuration)
		return nil, false
	}
	if st.packets >= rule.TracePacketsAmount {
		t.reclaim(st, rule, now)
		if st.packets >= rule.TracePacketsAmount {
			t.stop(st, StopReasonAmount)
			return nil, false
		}
	}
	st.packets++
	slot := &reservation{startedAt: now}
	trace := &AuctionTrace{
		partnerID:       partnerID,
		rule:            rule,
		packetIndex:     st.packets,
		auctionID:       auctionID,
		startedAt:       now.UTC(),
		slot:            slot,
		bidderRequests:  make([]BidderRequestPacket, 0, 4),
		bidderResponses: make([]BidderResponsePacket, 0, 4),
	}
	st.pending = append(st.pending, slot)
	if st.packets >= rule.TracePacketsAmount { // FR-10: the last slot has been taken
		t.stop(st, StopReasonAmount)
	}
	t.refresh()
	return trace, true
}

// Complete confirms the slot of a trace whose packet has been written (or attempted).
func (t *Tracer) Complete(trace *AuctionTrace) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.partners[trace.partnerID]
	if st == nil {
		return
	}
	if i := slices.Index(st.pending, trace.slot); i >= 0 {
		st.pending = slices.Delete(st.pending, i, i+1) // clears the vacated element of the backing array
	}
	t.refresh()
}

// reclaim gives back the slots whose lease is over and whose packet was not claimed. Caller holds t.mu.
func (t *Tracer) reclaim(st *partnerState, rule Rule, now time.Time) {
	before := len(st.pending)
	st.pending = slices.DeleteFunc(st.pending, func(r *reservation) bool { // clears the tail of the backing array
		// Revoke, not just forget: the auction that held the slot may still reach exitpoint.
		return now.Sub(r.startedAt) > slotLease && r.state.CompareAndSwap(slotPending, slotRevoked)
	})
	st.packets -= before - len(st.pending)
	if st.stopReason == StopReasonAmount && st.packets < rule.TracePacketsAmount {
		st.stopReason = StopReasonNone
	}
}

// stop moves a partner to a stopped state. Caller holds t.mu. A partner stopped by duration never
// traces again, so no slot of it will ever be given back and its reservations are dropped.
func (t *Tracer) stop(st *partnerState, reason StopReason) {
	st.stopReason = reason
	if reason == StopReasonDuration {
		st.pending = nil
	}
	t.refresh()
}

// refresh recomputes the lock-free answers of Exhausted. Caller holds t.mu.
//
// exhausted: every partner is stopped and none of the amount-stopped ones has a reservation that a
// lease could give back. deadline: once every partner has started, the latest window end among the
// partners that are not stopped by duration; after it no reservation can be made or given back.
func (t *Tracer) refresh() {
	if len(t.partners) < len(t.rules) {
		t.deadline.Store(0)
		t.exhausted.Store(false)
		return
	}
	var deadline time.Time
	exhausted := true
	for id, st := range t.partners {
		switch st.stopReason {
		case StopReasonDuration:
			continue
		case StopReasonAmount:
			if len(st.pending) > 0 {
				exhausted = false
			}
		default:
			exhausted = false
		}
		if end := st.firstTracedAt.Add(t.rules[id].Duration); end.After(deadline) {
			deadline = end
		}
	}
	t.exhausted.Store(exhausted)
	if deadline.IsZero() {
		t.deadline.Store(0)
	} else {
		t.deadline.Store(deadline.UnixNano())
	}
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
