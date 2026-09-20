//go:build load

package load

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stubBidder answers every bidder call of the sample request with one banner bid per impression,
// after a fixed delay that stands in for the network. It listens on all interfaces so the PBS
// container reaches it as host.docker.internal (deploy/pbs.load.yaml). The path names the bidder:
// appnexus needs its ad-type marker in the bid ext, the other three parse a plain OpenRTB response.
type stubBidder struct {
	server  *http.Server
	latency time.Duration
}

func startStubBidder(t *testing.T, addr string, latency time.Duration) {
	t.Helper()
	s := &stubBidder{latency: latency}
	s.server = &http.Server{Addr: addr, Handler: http.HandlerFunc(s.handle), ReadHeaderTimeout: 5 * time.Second}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	require.NoError(t, err, "stub bidder listen")
	go func() { _ = s.server.Serve(ln) }()
	t.Cleanup(func() { _ = s.server.Close() })
}

func (s *stubBidder) handle(w http.ResponseWriter, r *http.Request) {
	body := r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") { // amx sends gzip
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = gz.Close() }()
		body = gz
	}
	var req struct {
		ID  string `json:"id"`
		Imp []struct {
			ID string `json:"id"`
		} `json:"imp"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	bidder := strings.TrimPrefix(r.URL.Path, "/")

	type bid struct {
		ID    string          `json:"id"`
		ImpID string          `json:"impid"`
		Price float64         `json:"price"`
		AdM   string          `json:"adm"`
		CrID  string          `json:"crid"`
		W     int             `json:"w"`
		H     int             `json:"h"`
		Ext   json.RawMessage `json:"ext,omitempty"`
	}
	bids := make([]bid, 0, len(req.Imp))
	for _, imp := range req.Imp {
		b := bid{ID: bidder + "-" + imp.ID, ImpID: imp.ID, Price: 1.25, AdM: "<div>stub</div>", CrID: "stub", W: 300, H: 250}
		if bidder == "appnexus" {
			b.Ext = json.RawMessage(`{"appnexus":{"bid_ad_type":0}}`)
		}
		bids = append(bids, b)
	}
	resp := map[string]any{
		"id":      req.ID,
		"cur":     "USD",
		"seatbid": []map[string]any{{"seat": bidder, "bid": bids}},
	}

	time.Sleep(s.latency)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
