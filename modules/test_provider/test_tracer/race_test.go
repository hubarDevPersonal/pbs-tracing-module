package testtracer

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Concurrency tests follow PBS convention (docs/developers/automated-tests.md): names start with TestRace
// and are executed by validate.sh under the race detector.

// M-22. FR-10 AC2 / FR-16
func TestRaceTracerBeginNeverExceedsAmount(t *testing.T) {
	const amount = 10
	const attempts = 500
	tr, err := newTracer([]Rule{{PartnerID: "p", Duration: time.Hour, TracePacketsAmount: amount}}, time.Now)
	require.NoError(t, err)

	var started atomic.Int32
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := tr.Begin("p", "a", time.Time{}); ok {
				started.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.EqualValues(t, amount, started.Load())
	st, _ := tr.Status("p")
	assert.Equal(t, amount, st.Packets)
	assert.Equal(t, StopReasonAmount, st.StopReason)
}

// M-30. FR-16 AC1
func TestRaceAuctionTraceConcurrentAppends(t *testing.T) {
	tr, err := newTracer(testRules(), time.Now)
	require.NoError(t, err)
	trace, ok := tr.Begin(sampleRequestAccountID, "a", time.Time{})
	require.True(t, ok)

	const n = 64
	var wg sync.WaitGroup
	for range n {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = trace.AddBidderRequest(time.Now(), "b", &openrtb2.BidRequest{ID: "r"})
		}()
		go func() {
			defer wg.Done()
			_ = trace.AddBidderResponse(time.Now(), "b", sampleBidderResponse("b", "x", 1))
		}()
	}
	wg.Wait()

	p := trace.Packet(time.Now())
	assert.Len(t, p.BidderRequests, n)
	assert.Len(t, p.BidderResponses, n)
}

// M-30. FR-16 AC1: per-bidder hooks of one request run in parallel goroutines in PBS.
func TestRaceModuleConcurrentBidderHooks(t *testing.T) {
	m, out := newTestModule(t, testRules(), newFakeClock(testStart))
	body := loadSampleRequest(t)
	mc := tracedContext(t, m, sampleRequestAccountID, body)
	wrapper := requestWrapperFrom(t, body) // read-only from the goroutines below

	const bidders = 32
	var wg sync.WaitGroup
	for i := range bidders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bidder := "bidder-" + string(rune('a'+i%26))
			_, err := m.HandleBidderRequestHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
				hookstage.BidderRequestPayload{Request: wrapper, Bidder: bidder})
			assert.NoError(t, err)
			_, err = m.HandleRawBidderResponseHook(t.Context(), auctionCtx(sampleRequestAccountID, mc),
				hookstage.RawBidderResponsePayload{BidderResponse: sampleBidderResponse(bidder, "b", 1), Bidder: bidder})
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	final := sampleBidResponse("r", "appnexus")
	_, err := m.HandleAuctionResponseHook(t.Context(), auctionCtx(sampleRequestAccountID, mc), hookstage.AuctionResponsePayload{BidResponse: final})
	require.NoError(t, err)
	_, err = m.HandleExitpointHook(t.Context(), auctionCtx(sampleRequestAccountID, mc), hookstage.ExitpointPayload{Response: final, W: httptest.NewRecorder()})
	require.NoError(t, err)

	packets := out.Packets(t)
	require.Len(t, packets, 1)
	assert.Len(t, packets[0].BidderRequests, bidders)
	assert.Len(t, packets[0].BidderResponses, bidders)
}

// M-31. FR-16 AC2: concurrent requests reaching exitpoint at once never interleave output lines.
func TestRaceJSONEmitterConcurrentEmits(t *testing.T) {
	out := &syncBuffer{}
	em := newJSONEmitter(out)

	const n = 100
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			assert.NoError(t, em.Emit(samplePacket(i)))
		}(i)
	}
	wg.Wait()

	packets := out.Packets(t) // fails if any line is not standalone valid JSON
	require.Len(t, packets, n)
	seen := make(map[int]bool, n)
	for _, p := range packets {
		seen[p.PacketIndex] = true
	}
	assert.Len(t, seen, n)
}

// M-30. FR-16: many concurrent requests for several partners through the full hook path.
func TestRaceModuleConcurrentRequests(t *testing.T) {
	rules := []Rule{
		{PartnerID: "p1", Duration: time.Hour, TracePacketsAmount: 5},
		{PartnerID: "p2", Duration: time.Hour, TracePacketsAmount: 7},
	}
	m, out := newTestModule(t, rules, newFakeClock(testStart))
	body := loadSampleRequest(t)
	wrapper := requestWrapperFrom(t, body) // read-only from the goroutines below

	var wg sync.WaitGroup
	for i := range 60 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			partner := []string{"p1", "p2", "none"}[i%3]
			_, errs := driveHooks(t.Context(), m, partner, body, wrapper, []string{"appnexus", "amx"}, sampleBidResponse("r", "appnexus"))
			assert.Empty(t, errs)
		}(i)
	}
	wg.Wait()

	packets := out.Packets(t)
	counts := map[string]int{}
	for _, p := range packets {
		counts[p.PartnerID]++
	}
	assert.Equal(t, 5, counts["p1"])
	assert.Equal(t, 7, counts["p2"])
	assert.Zero(t, counts["none"])
	assert.Len(t, packets, 12)
}

// M-35. FR-08 AC1b: hooks still emitting while the process shuts down never panic; each emit either
// lands in the queue before Close or is refused after it.
func TestRaceAsyncEmitterEmitDuringClose(t *testing.T) {
	out := &syncBuffer{}
	em := newAsyncEmitter(newJSONEmitter(out), 8)

	var wg sync.WaitGroup
	var refused atomic.Int32
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := em.Emit(samplePacket(i)); errors.Is(err, ErrEmitterClosed) {
				refused.Add(1)
			}
		}()
	}
	require.NoError(t, em.Close())
	wg.Wait()

	assert.Equal(t, 64, len(out.Packets(t))+int(refused.Load())+int(em.Dropped()), "every emit was written, refused or dropped")
}

// M-39. FR-10 AC2, AC4: old auctions reach exitpoint while new ones claim their expired slots. Whichever
// order the goroutines take, each slot is written by exactly one auction.
func TestRaceExpiredLeaseAndExitpointNeverBothWrite(t *testing.T) {
	const amount = 3
	for range 100 {
		clock := newFakeClock(testStart)
		m, out := newTestModule(t, []Rule{{PartnerID: sampleRequestAccountID, Duration: time.Hour, TracePacketsAmount: amount}}, clock)
		body := loadSampleRequest(t)
		exit := func(mc *hookstage.ModuleContext) {
			_, err := m.HandleExitpointHook(context.Background(), auctionCtx(sampleRequestAccountID, mc),
				hookstage.ExitpointPayload{Response: sampleBidResponse("r"), W: httptest.NewRecorder()})
			assert.NoError(t, err)
		}

		slow := make([]*hookstage.ModuleContext, 0, amount)
		for range amount {
			slow = append(slow, tracedContext(t, m, sampleRequestAccountID, body))
		}
		clock.Advance(slotLease + time.Second)
		fresh := make([]*hookstage.ModuleContext, 0, amount)
		payloads := make([]hookstage.ProcessedAuctionRequestPayload, 0, amount)
		for range amount {
			entry, err := m.HandleEntrypointHook(t.Context(), auctionCtx("", nil), entrypointPayload(body))
			require.NoError(t, err)
			fresh = append(fresh, entry.ModuleContext)
			payloads = append(payloads, hookstage.ProcessedAuctionRequestPayload{Request: requestWrapperFrom(t, body)})
		}

		var wg sync.WaitGroup
		for i := range amount {
			wg.Add(2)
			go func() {
				defer wg.Done()
				exit(slow[i])
			}()
			go func() {
				defer wg.Done()
				_, err := m.HandleProcessedAuctionHook(context.Background(), auctionCtx(sampleRequestAccountID, fresh[i]), payloads[i])
				assert.NoError(t, err)
			}()
		}
		wg.Wait()
		for _, mc := range fresh {
			exit(mc)
		}

		require.LessOrEqual(t, len(out.Packets(t)), amount, "a slot was written twice")
	}
}
