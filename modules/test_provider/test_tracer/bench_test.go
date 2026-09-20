package testtracer

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/openrtb_ext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// L-01. Benchmarks quantify the module's per-request overhead:
// a traced auction pays for one body copy plus (2 + 2×bidders) JSON marshals and one stdout write;
// an untraced auction pays a map lookup and a few context reads.

func benchModule(b testing.TB, rules []Rule) *Module {
	b.Helper()
	m, err := newModule(rules, newJSONEmitter(io.Discard), time.Now)
	if err != nil {
		b.Fatal(err)
	}
	return m
}

// benchFixture holds request-independent inputs so the benchmarks measure the module, not the harness.
type benchFixture struct {
	body    []byte
	wrapper *openrtb_ext.RequestWrapper
	bidders []string
	resps   map[string]*adapters.BidderResponse
	final   *openrtb2.BidResponse
	rec     *httptest.ResponseRecorder
}

func newBenchFixture(b testing.TB) benchFixture {
	b.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "bid_request.json"))
	if err != nil {
		b.Fatal(err)
	}
	var br openrtb2.BidRequest
	if err := json.Unmarshal(body, &br); err != nil {
		b.Fatal(err)
	}
	f := benchFixture{
		body:    body,
		wrapper: &openrtb_ext.RequestWrapper{BidRequest: &br},
		bidders: []string{"aceex", "appnexus", "amx", "adyoulike"},
		resps:   map[string]*adapters.BidderResponse{},
		final:   sampleBidResponse("r", "appnexus"),
		rec:     httptest.NewRecorder(),
	}
	for _, bd := range f.bidders {
		f.resps[bd] = sampleBidderResponse(bd, "b", 1)
	}
	return f
}

func runAuctionForBench(m *Module, accountID string, f benchFixture) {
	ctx := context.Background()
	entry, _ := m.HandleEntrypointHook(ctx, auctionCtx("", nil), entrypointPayload(f.body))
	mc := entry.ModuleContext
	if mc == nil {
		mc = hookstage.NewModuleContext()
	}
	_, _ = m.HandleProcessedAuctionHook(ctx, auctionCtx(accountID, mc), hookstage.ProcessedAuctionRequestPayload{Request: f.wrapper})
	for _, bd := range f.bidders {
		_, _ = m.HandleBidderRequestHook(ctx, auctionCtx(accountID, mc), hookstage.BidderRequestPayload{Request: f.wrapper, Bidder: bd})
		_, _ = m.HandleRawBidderResponseHook(ctx, auctionCtx(accountID, mc), hookstage.RawBidderResponsePayload{BidderResponse: f.resps[bd], Bidder: bd})
	}
	_, _ = m.HandleAuctionResponseHook(ctx, auctionCtx(accountID, mc), hookstage.AuctionResponsePayload{BidResponse: f.final})
	_, _ = m.HandleExitpointHook(ctx, auctionCtx(accountID, mc), hookstage.ExitpointPayload{Response: f.final, W: f.rec})
}

func BenchmarkTracedAuction_4Bidders(b *testing.B) {
	m := benchModule(b, []Rule{{PartnerID: sampleRequestAccountID, Duration: 24 * time.Hour, TracePacketsAmount: 1 << 30}})
	f := newBenchFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runAuctionForBench(m, sampleRequestAccountID, f)
	}
}

func BenchmarkUntracedAuction_4Bidders(b *testing.B) {
	m := benchModule(b, testRules())
	f := newBenchFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runAuctionForBench(m, "not-a-partner", f)
	}
}

func BenchmarkEmitPacket(b *testing.B) {
	em := newJSONEmitter(io.Discard)
	p := samplePacket(1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = em.Emit(p)
	}
}

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

	// one traced auction first: its packet is taken by the writer, which blocks in Write
	runAuctionForBench(m, sampleRequestAccountID, f)
	<-w.entered

	// then more traced auctions than the queue holds, and untraced ones meanwhile
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
	assert.EqualValues(t, traced-queue, em.Dropped(), "the queue holds the next packets, the rest are dropped")

	close(w.release)
	require.NoError(t, m.Shutdown())
	assert.EqualValues(t, queue+1, w.writes.Load(), "the blocked packet and the queued ones are written after stdout resumes")

	// the module stays usable: a later hook invocation is a plain no-op
	res, err := m.HandleBidderRequestHook(context.Background(), auctionCtx("not-a-partner", hookstage.NewModuleContext()), hookstage.BidderRequestPayload{})
	require.NoError(t, err)
	assert.False(t, res.Reject)
}
