package testtracer

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestTracer(t *testing.T, rules []Rule, clock *fakeClock) *Tracer {
	t.Helper()
	tr, err := newTracer(rules, clock.Now)
	require.NoError(t, err)
	require.NotNil(t, tr)
	return tr
}

// M-02. FR-02
func TestNewTracer_RejectsInvalidRules(t *testing.T) {
	_, err := newTracer([]Rule{{PartnerID: "", Duration: time.Second, TracePacketsAmount: 1}}, time.Now)
	assert.Error(t, err)
}

// M-05. FR-03 / FR-13
func TestTracerBegin_UnknownPartnerIsNotTraced(t *testing.T) {
	tr := newTestTracer(t, testRules(), newFakeClock(testStart))

	trace, ok := tr.Begin("unknown-partner", "auction-1", time.Time{})

	assert.False(t, ok)
	assert.Nil(t, trace)
	_, found := tr.Status("unknown-partner")
	assert.False(t, found, "unknown partners must not allocate state")
}

// M-20. FR-03 / FR-09: the first traced request opens the window.
func TestTracerBegin_FirstPacketStartsWindow(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, testRules(), clock)

	trace, ok := tr.Begin(sampleRequestAccountID, "auction-1", time.Time{})

	require.True(t, ok)
	require.NotNil(t, trace)
	assert.Equal(t, sampleRequestAccountID, trace.PartnerID())
	assert.Equal(t, "auction-1", trace.AuctionID())
	assert.Equal(t, 1, trace.PacketIndex())

	st, found := tr.Status(sampleRequestAccountID)
	require.True(t, found)
	assert.Equal(t, 1, st.Packets)
	assert.True(t, st.FirstTracedAt.Equal(testStart))
	assert.Equal(t, StopReasonNone, st.StopReason)
}

// M-21. FR-10
func TestTracerBegin_AmountLimitStopsTracing(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Hour, TracePacketsAmount: 2}}, clock)

	first, ok1 := tr.Begin("p", "a1", time.Time{})
	second, ok2 := tr.Begin("p", "a2", time.Time{})
	third, ok3 := tr.Begin("p", "a3", time.Time{})

	require.True(t, ok1)
	require.True(t, ok2)
	assert.False(t, ok3)
	assert.Nil(t, third)
	assert.Equal(t, 1, first.PacketIndex())
	assert.Equal(t, 2, second.PacketIndex())

	st, _ := tr.Status("p")
	assert.Equal(t, 2, st.Packets, "packets must never exceed TracePacketsAmount")
	assert.Equal(t, StopReasonAmount, st.StopReason)
}

// M-20. FR-09: stop when elapsed > Duration (strictly "exceeds").
func TestTracerBegin_DurationLimitStopsTracing(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: 10 * time.Second, TracePacketsAmount: 100}}, clock)

	_, ok := tr.Begin("p", "a1", time.Time{})
	require.True(t, ok)

	clock.Advance(10 * time.Second) // elapsed == Duration
	_, ok = tr.Begin("p", "a2", time.Time{})
	assert.True(t, ok, "elapsed == Duration is still inside the window")

	clock.Advance(time.Nanosecond) // elapsed > Duration
	_, ok = tr.Begin("p", "a3", time.Time{})
	assert.False(t, ok)

	st, _ := tr.Status("p")
	assert.Equal(t, 2, st.Packets)
	assert.Equal(t, StopReasonDuration, st.StopReason)
}

// M-23. FR-11: whichever condition is met first wins.
func TestTracerBegin_WhicheverComesFirst(t *testing.T) {
	t.Run("duration before amount", func(t *testing.T) {
		clock := newFakeClock(testStart)
		tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Second, TracePacketsAmount: 10}}, clock)
		_, ok := tr.Begin("p", "a1", time.Time{})
		require.True(t, ok)
		clock.Advance(2 * time.Second)
		_, ok = tr.Begin("p", "a2", time.Time{})
		assert.False(t, ok)
		st, _ := tr.Status("p")
		assert.Equal(t, StopReasonDuration, st.StopReason)
	})

	t.Run("amount before duration", func(t *testing.T) {
		clock := newFakeClock(testStart)
		tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Hour, TracePacketsAmount: 1}}, clock)
		_, ok := tr.Begin("p", "a1", time.Time{})
		require.True(t, ok)
		_, ok = tr.Begin("p", "a2", time.Time{})
		assert.False(t, ok)
		st, _ := tr.Status("p")
		assert.Equal(t, StopReasonAmount, st.StopReason)
	})
}

// M-23. FR-11 AC1
func TestTracerBegin_StoppedPartnerDoesNotRearm(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Second, TracePacketsAmount: 1}}, clock)
	_, ok := tr.Begin("p", "a1", time.Time{})
	require.True(t, ok)

	for _, step := range []time.Duration{0, time.Second, time.Hour, 24 * time.Hour} {
		clock.Advance(step)
		_, ok = tr.Begin("p", "again", time.Time{})
		assert.False(t, ok, "stopped partner re-armed after %s", step)
	}
	st, _ := tr.Status("p")
	assert.Equal(t, 1, st.Packets)
}

// M-23. FR-11 AC2
func TestTracerBegin_PartnersAreIndependent(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{
		{PartnerID: "a", Duration: time.Hour, TracePacketsAmount: 1},
		{PartnerID: "b", Duration: time.Hour, TracePacketsAmount: 2},
	}, clock)

	_, okA1 := tr.Begin("a", "1", time.Time{})
	_, okA2 := tr.Begin("a", "2", time.Time{})
	_, okB1 := tr.Begin("b", "1", time.Time{})
	_, okB2 := tr.Begin("b", "2", time.Time{})
	_, okB3 := tr.Begin("b", "3", time.Time{})

	assert.True(t, okA1)
	assert.False(t, okA2)
	assert.True(t, okB1)
	assert.True(t, okB2)
	assert.False(t, okB3)
}

// M-20. FR-09 AC1: the window opens at the incoming timestamp of the first traced request, not at the
// later trigger time. First request in at t=0, triggered at t=500 ms; Duration 1 s; a request triggered
// at t=1100 ms is refused even though only 600 ms passed since the first trigger.
func TestTracerBegin_WindowOpensAtIncomingTimestamp(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Second, TracePacketsAmount: 10}}, clock)

	incomingAt := clock.Now()
	clock.Advance(500 * time.Millisecond)
	_, ok := tr.Begin("p", "a1", incomingAt)
	require.True(t, ok)
	st, _ := tr.Status("p")
	assert.True(t, st.FirstTracedAt.Equal(incomingAt), "window starts at the incoming timestamp")

	clock.Advance(600 * time.Millisecond) // t = 1100 ms since the first incoming request
	_, ok = tr.Begin("p", "a2", clock.Now())
	assert.False(t, ok)
	st, _ = tr.Status("p")
	assert.Equal(t, StopReasonDuration, st.StopReason)
}

// M-34. NFR-01: once every partner is stopped the tracer reports exhaustion, so the entrypoint copy
// can be skipped; an empty rule set is exhausted from the start.
func TestTracer_ExhaustedWhenEveryPartnerIsStopped(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{
		{PartnerID: "p1", Duration: time.Hour, TracePacketsAmount: 1},
		{PartnerID: "p2", Duration: time.Second, TracePacketsAmount: 5},
	}, clock)
	assert.False(t, tr.Exhausted(clock.Now()))

	a, ok := tr.Begin("p1", "a", clock.Now()) // amount reached, slot still pending
	require.True(t, ok)
	assert.False(t, tr.Exhausted(clock.Now()), "p2 is still active")

	b, ok := tr.Begin("p2", "b", clock.Now())
	require.True(t, ok)
	clock.Advance(2 * time.Second)
	_, ok = tr.Begin("p2", "c", clock.Now()) // duration exceeded
	require.False(t, ok)
	assert.False(t, tr.Exhausted(clock.Now()), "p1's pending slot could still be given back")

	tr.Complete(a)
	tr.Complete(b)
	assert.True(t, tr.Exhausted(clock.Now()))

	empty := newTestTracer(t, nil, clock)
	assert.True(t, empty.Exhausted(clock.Now()))
}

// M-36. FR-10, D13: a slot reserved by an auction that never reached exitpoint is given back once
// its lease is over, so the amount counts collected packets; a written packet keeps its slot.
func TestTracerBegin_ReclaimsSlotsOfAuctionsThatNeverCompleted(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: 24 * time.Hour, TracePacketsAmount: 1}}, clock)

	lost, ok := tr.Begin("p", "lost", clock.Now()) // PBS fails this auction: exitpoint never runs
	require.True(t, ok)
	_, ok = tr.Begin("p", "too-early", clock.Now())
	assert.False(t, ok, "the slot is still leased")
	st, _ := tr.Status("p")
	assert.Equal(t, StopReasonAmount, st.StopReason)

	clock.Advance(slotLease + time.Second)
	written, ok := tr.Begin("p", "written", clock.Now())
	require.True(t, ok, "the lease is over, the slot is given back")
	assert.Equal(t, 1, written.PacketIndex())
	st, _ = tr.Status("p")
	assert.Equal(t, 1, st.Packets)
	assert.False(t, lost.isEmitted())

	require.True(t, written.tryMarkEmitted())
	tr.Complete(written)
	clock.Advance(slotLease + time.Second)
	_, ok = tr.Begin("p", "after-written", clock.Now())
	assert.False(t, ok, "a written packet keeps its slot for good")
}

// M-37. NFR-01: once every partner has started, the tracer knows the time after which no window is
// open and reports exhaustion from then on, without waiting for a request of each partner.
func TestTracer_ExhaustedAfterEveryWindowClosed(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{
		{PartnerID: "p1", Duration: time.Minute, TracePacketsAmount: 10},
		{PartnerID: "p2", Duration: time.Hour, TracePacketsAmount: 10},
	}, clock)

	_, ok := tr.Begin("p1", "a", clock.Now())
	require.True(t, ok)
	assert.False(t, tr.Exhausted(clock.Now().Add(2*time.Hour)), "p2 has not started: its window is unknown")

	_, ok = tr.Begin("p2", "b", clock.Now())
	require.True(t, ok)
	assert.False(t, tr.Exhausted(clock.Now().Add(time.Hour)), "p2's window is still open at its end")
	assert.True(t, tr.Exhausted(clock.Now().Add(time.Hour+time.Nanosecond)))
}

// M-20. FR-09: the window is measured between request arrivals. A request that arrived inside the
// window is traced even if its trigger decision comes after the window's end.
func TestTracerBegin_WindowIsMeasuredBetweenArrivals(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Second, TracePacketsAmount: 10}}, clock)
	_, ok := tr.Begin("p", "a1", clock.Now())
	require.True(t, ok)

	arrived := clock.Now().Add(900 * time.Millisecond)
	clock.Advance(1100 * time.Millisecond) // triggered after the window's end
	_, ok = tr.Begin("p", "a2", arrived)
	assert.True(t, ok)
	_, ok = tr.Begin("p", "a3", clock.Now())
	assert.False(t, ok)
}

// M-38. NFR-02, FR-10 AC4: the tracer keeps slot reservations, not traces. A completed auction and one
// abandoned after the trigger are both collectable once their requests let go of them; the abandoned one
// still holds its slot until the lease ends, and a partner stopped for good keeps no reservations.
func TestTracer_KeepsNoTraceReachable(t *testing.T) {
	clock := newFakeClock(testStart)
	tr := newTestTracer(t, []Rule{{PartnerID: "p", Duration: time.Minute, TracePacketsAmount: 3}}, clock)

	// Each trace lives only inside its closure, as in production it lives only in its request's module context.
	abandoned := func() weak.Pointer[AuctionTrace] {
		trace, ok := tr.Begin("p", "abandoned", clock.Now()) // PBS fails the auction: exitpoint never runs
		require.True(t, ok)
		trace.SetIncomingRequest(clock.Now(), []byte(`{"id":"abandoned"}`))
		return weak.Make(trace)
	}()
	completed := func() weak.Pointer[AuctionTrace] {
		trace, ok := tr.Begin("p", "completed", clock.Now())
		require.True(t, ok)
		trace.SetIncomingRequest(clock.Now(), []byte(`{"id":"completed"}`))
		require.True(t, trace.tryMarkEmitted())
		tr.Complete(trace)
		return weak.Make(trace)
	}()

	runtime.GC()
	assert.Nil(t, completed.Value(), "a completed trace is still reachable from the tracer")
	assert.Nil(t, abandoned.Value(), "an abandoned trace is still reachable from the tracer")
	st, _ := tr.Status("p")
	assert.Equal(t, 2, st.Packets, "the abandoned auction keeps its slot until the lease ends")

	clock.Advance(2 * time.Minute)
	_, ok := tr.Begin("p", "late", clock.Now()) // the window has closed: the partner stops for good
	require.False(t, ok)
	assert.Empty(t, tr.partners["p"].pending, "a partner stopped for good keeps no reservations")
}
