package testtracer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/openrtb_ext"
	"github.com/stretchr/testify/require"
)

// sampleRequestAccountID is the Account.ID Prebid Server resolves for testdata/bid_request.json
// (site.publisher.ext.prebid.parentAccount wins over site.publisher.id "33415-10498").
const sampleRequestAccountID = "664-025-677-881"

const hookImplCode = "test_provider_test_tracer"

var testStart = time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)

// fakeClock is an injectable, manually advanced clock (NFR-05).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{t: start} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// syncBuffer is a goroutine-safe io.Writer used as the module's stdout in tests.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Lines returns the non-empty lines written so far.
func (b *syncBuffer) Lines() []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(b.String()))
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		if l := sc.Text(); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// Packets parses every emitted line back into a TracePacket.
func (b *syncBuffer) Packets(t *testing.T) []TracePacket {
	t.Helper()
	lines := b.Lines()
	packets := make([]TracePacket, 0, len(lines))
	for i, l := range lines {
		var p TracePacket
		require.NoErrorf(t, json.Unmarshal([]byte(l), &p), "line %d is not valid JSON: %s", i, l)
		packets = append(packets, p)
	}
	return packets
}

// blockingWriter blocks every Write until release is closed, like a stdout pipe nobody reads.
type blockingWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	writes  atomic.Int64
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	w.writes.Add(1)
	return len(p), nil
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func testRules() []Rule {
	return []Rule{
		{PartnerID: sampleRequestAccountID, Duration: 10 * time.Minute, TracePacketsAmount: 3},
		{PartnerID: "partner-two", Duration: 30 * time.Second, TracePacketsAmount: 1},
	}
}

func newTestModule(t *testing.T, rules []Rule, clock *fakeClock) (*Module, *syncBuffer) {
	t.Helper()
	out := &syncBuffer{}
	m, err := newModule(rules, newJSONEmitter(out), clock.Now)
	require.NoError(t, err)
	require.NotNil(t, m)
	return m, out
}

func loadSampleRequest(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "bid_request.json"))
	require.NoError(t, err)
	return body
}

func requestWrapperFrom(t *testing.T, body []byte) *openrtb_ext.RequestWrapper {
	t.Helper()
	var br openrtb2.BidRequest
	require.NoError(t, json.Unmarshal(body, &br))
	return &openrtb_ext.RequestWrapper{BidRequest: &br}
}

func invocationCtx(endpoint, accountID string, mc *hookstage.ModuleContext) hookstage.ModuleInvocationContext {
	return hookstage.ModuleInvocationContext{
		Endpoint:      endpoint,
		AccountID:     accountID,
		ModuleContext: mc,
		HookImplCode:  hookImplCode,
	}
}

func auctionCtx(accountID string, mc *hookstage.ModuleContext) hookstage.ModuleInvocationContext {
	return invocationCtx(auctionEndpoint, accountID, mc)
}

func entrypointPayload(body []byte) hookstage.EntrypointPayload {
	return hookstage.EntrypointPayload{
		Request: httptest.NewRequestWithContext(context.Background(), http.MethodPost, auctionEndpoint, bytes.NewReader(body)),
		Body:    body,
	}
}

func sampleBidderResponse(bidder, bidID string, price float64) *adapters.BidderResponse {
	return &adapters.BidderResponse{
		Currency: "USD",
		Bids: []*adapters.TypedBid{{
			Bid:     &openrtb2.Bid{ID: bidID, ImpID: "974090632", Price: price, AdM: "<div>ad</div>", CrID: "crid-" + bidID},
			BidType: openrtb_ext.BidTypeBanner,
			Seat:    openrtb_ext.BidderName(bidder),
		}},
	}
}

func sampleBidResponse(id string, seats ...string) *openrtb2.BidResponse {
	resp := &openrtb2.BidResponse{ID: id, Cur: "USD"}
	for _, s := range seats {
		resp.SeatBid = append(resp.SeatBid, openrtb2.SeatBid{Seat: s, Bid: []openrtb2.Bid{{ID: "bid-" + s, ImpID: "974090632", Price: 1.25}}})
	}
	return resp
}

// tracedContext runs entrypoint + processed_auction_request for accountID and returns the module context.
func tracedContext(t *testing.T, m *Module, accountID string, body []byte) *hookstage.ModuleContext {
	t.Helper()
	entry, err := m.HandleEntrypointHook(t.Context(), auctionCtx("", nil), entrypointPayload(body))
	require.NoError(t, err)
	mc := entry.ModuleContext
	processed, err := m.HandleProcessedAuctionHook(t.Context(), auctionCtx(accountID, mc),
		hookstage.ProcessedAuctionRequestPayload{Request: requestWrapperFrom(t, body)})
	require.NoError(t, err)
	if processed.ModuleContext != nil {
		mc = processed.ModuleContext
	}
	return mc
}

func traceIn(mc *hookstage.ModuleContext) *AuctionTrace {
	if mc == nil {
		return nil
	}
	v, ok := mc.Get(ctxKeyTrace)
	if !ok || v == nil {
		return nil
	}
	tr, _ := v.(*AuctionTrace)
	return tr
}

// driveHooks drives all seven stages for one request without failing the test itself, so it can be
// used from goroutines (testify's require must only be called from the test goroutine).
func driveHooks(ctx context.Context, m *Module, accountID string, body []byte, wrapper *openrtb_ext.RequestWrapper, bidders []string, final *openrtb2.BidResponse) (*hookstage.ModuleContext, []error) {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	entry, err := m.HandleEntrypointHook(ctx, auctionCtx("", nil), entrypointPayload(body))
	add(err)
	mc := entry.ModuleContext
	processed, err := m.HandleProcessedAuctionHook(ctx, auctionCtx(accountID, mc), hookstage.ProcessedAuctionRequestPayload{Request: wrapper})
	add(err)
	if processed.ModuleContext != nil {
		mc = processed.ModuleContext
	}
	for _, b := range bidders {
		_, err := m.HandleBidderRequestHook(ctx, auctionCtx(accountID, mc), hookstage.BidderRequestPayload{Request: wrapper, Bidder: b})
		add(err)
		_, err = m.HandleRawBidderResponseHook(ctx, auctionCtx(accountID, mc),
			hookstage.RawBidderResponsePayload{BidderResponse: sampleBidderResponse(b, "bid-"+b, 1.5), Bidder: b})
		add(err)
	}
	_, err = m.HandleAllProcessedBidResponsesHook(ctx, auctionCtx(accountID, mc), hookstage.AllProcessedBidResponsesPayload{})
	add(err)
	_, err = m.HandleAuctionResponseHook(ctx, auctionCtx(accountID, mc), hookstage.AuctionResponsePayload{BidResponse: final})
	add(err)
	_, err = m.HandleExitpointHook(ctx, auctionCtx(accountID, mc), hookstage.ExitpointPayload{Response: final, W: httptest.NewRecorder()})
	add(err)
	return mc, errs
}

// runFullAuction drives all seven stages for one request and returns the module context.
func runFullAuction(t *testing.T, m *Module, accountID string, body []byte, bidders []string, final *openrtb2.BidResponse) *hookstage.ModuleContext {
	t.Helper()
	mc, errs := driveHooks(t.Context(), m, accountID, body, requestWrapperFrom(t, body), bidders, final)
	require.Empty(t, errs)
	return mc
}
