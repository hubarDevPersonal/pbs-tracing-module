package testtracer

import (
	"context"
	"sync"
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

// blockingWriter blocks every Write until release is closed, like a stdout pipe nobody reads.
type blockingWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

// L-03. NFR-01: the stdout write at exitpoint is the module's only blocking call. A stalled stdout may
// hold the traced auction that writes, but auctions that are not traced never touch the emitter and
// must complete regardless.
func TestOverhead_BlockedStdoutStallsOnlyTracedAuctions(t *testing.T) {
	w := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	m, err := newModule(testRules(), newJSONEmitter(w), time.Now)
	require.NoError(t, err)
	f := newBenchFixture(t)

	traced := make(chan struct{})
	go func() {
		defer close(traced)
		runAuctionForBench(m, sampleRequestAccountID, f)
	}()
	select {
	case <-w.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the traced auction never reached the stdout write")
	}

	untraced := make(chan struct{})
	go func() {
		defer close(untraced)
		for range 100 {
			runAuctionForBench(m, "not-a-partner", f)
		}
	}()
	select {
	case <-untraced:
	case <-time.After(5 * time.Second):
		t.Fatal("untraced auctions were blocked by the stalled stdout of a traced auction")
	}

	select {
	case <-traced:
		t.Fatal("the traced auction finished although its write is still blocked")
	default:
	}
	close(w.release)
	<-traced

	// the module stays usable: a later hook invocation is a plain no-op
	res, err := m.HandleBidderRequestHook(context.Background(), auctionCtx("not-a-partner", hookstage.NewModuleContext()), hookstage.BidderRequestPayload{})
	require.NoError(t, err)
	assert.False(t, res.Reject)
}
