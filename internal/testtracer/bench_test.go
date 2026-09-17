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
)

// Benchmarks quantify the module's per-request overhead (docs/06-performance.md):
// a traced auction pays for one body copy plus (2 + 2×bidders) JSON marshals and one stdout write;
// an untraced auction pays a map lookup and a few context reads.

func benchModule(b *testing.B, rules []Rule) *Module {
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

func newBenchFixture(b *testing.B) benchFixture {
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
