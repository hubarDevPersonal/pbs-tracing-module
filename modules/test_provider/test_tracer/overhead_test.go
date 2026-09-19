package testtracer

import (
	"context"
	"testing"
	"time"

	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Load criteria that hold on every machine and therefore run in the default suite
// (docs/test-specs/load.md). Timing is measured by the benchmarks and the load test instead.

// untracedAllocBudget bounds the allocations of a whole untraced auction with four bidders (all seven
// hooks). Most of it is the entrypoint body copy, which happens before the account is known.
const untracedAllocBudget = 20

// L-02. NFR-01 / FR-05 AC3: the steady-state cost of the module is the untraced path; it must stay flat.
func TestOverhead_UntracedAuctionAllocationBudget(t *testing.T) {
	m := benchModule(t, testRules())
	f := newBenchFixture(t)

	allocs := testing.AllocsPerRun(200, func() { runAuctionForBench(m, "not-a-partner", f) })
	assert.LessOrEqual(t, allocs, float64(untracedAllocBudget), "allocations per untraced auction")
}

// L-03. NFR-01 / FR-08 AC1a: a stdout that does not drain delays no auction. Traced auctions enqueue and
// return; beyond the queue their packets are dropped and counted; untraced auctions never touch the output.
func TestOverhead_BlockedStdoutStallsNoAuction(t *testing.T) {
	w := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	const queue = 4
	em := newAsyncEmitter(newJSONEmitter(w), queue)
	rules := []Rule{{PartnerID: sampleRequestAccountID, Duration: time.Hour, TracePacketsAmount: 1 << 20}}
	m, err := newModule(rules, em, time.Now)
	require.NoError(t, err)
	f := newBenchFixture(t)

	const traced = queue + 10
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range traced {
			runAuctionForBench(m, sampleRequestAccountID, f)
		}
		for range 100 {
			runAuctionForBench(m, "not-a-partner", f)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("auctions were held by the stalled stdout")
	}
	<-w.entered
	assert.EqualValues(t, traced-queue-1, em.Dropped(), "one packet is in the blocked write, queue holds the next ones, the rest are dropped")

	close(w.release)
	require.NoError(t, m.Shutdown())
	assert.EqualValues(t, queue+1, w.writes.Load(), "the blocked packet and the queued ones are written after stdout resumes")

	// the module stays usable: a later hook invocation is a plain no-op
	res, err := m.HandleBidderRequestHook(context.Background(), auctionCtx("not-a-partner", hookstage.NewModuleContext()), hookstage.BidderRequestPayload{})
	require.NoError(t, err)
	assert.False(t, res.Reject)
}
