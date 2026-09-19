package testtracer

import (
	"testing"
	"time"

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
	assert.False(t, tr.Exhausted())

	_, ok := tr.Begin("p1", "a", clock.Now()) // amount reached
	require.True(t, ok)
	assert.False(t, tr.Exhausted(), "p2 is still active")

	_, ok = tr.Begin("p2", "b", clock.Now())
	require.True(t, ok)
	clock.Advance(2 * time.Second)
	_, ok = tr.Begin("p2", "c", clock.Now()) // duration exceeded
	require.False(t, ok)
	assert.True(t, tr.Exhausted())

	empty := newTestTracer(t, nil, clock)
	assert.True(t, empty.Exhausted())
}
