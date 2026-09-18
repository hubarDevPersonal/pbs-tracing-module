// Package tracecheck verifies the NDJSON output of the test_provider.test_tracer module against the
// assessment's expectations. It backs the cmd/tracecheck CLI used by the e2e scripts.
package tracecheck

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hubarDevPersonal/pbs-tracing-module/internal/testtracer"
)

// Module is the module code every packet must carry.
const Module = "test_provider.test_tracer"

// NotFoundHookWarning is what PBS logs, per stage and request, when the module is planned but not registered.
const NotFoundHookWarning = "Not found hook while building hook execution plan: " + Module

// Options selects which assertions Verify applies. Zero values disable an assertion.
type Options struct {
	ExpectPackets int      // exact number of packets; < 0 disables the check
	PartnerID     string   // expected partner_id on every packet
	AuctionID     string   // expected BidRequest.id of incoming_request and every bidder_request
	Bidders       []string // exact set of bidders expected in bidder_requests
	StrictBids    bool     // fail when no packet carries a bidder response (live bidders may answer 204)
	RequireDebug  bool     // final_response.body.ext.debug must be present (request had ext.prebid.debug:true)
}

// Report summarizes a successful verification.
type Report struct {
	Packets         int
	BidderResponses int
}

func (r Report) String() string {
	s := fmt.Sprintf("OK: %d packets; bidder responses recorded: %d", r.Packets, r.BidderResponses)
	if r.BidderResponses == 0 {
		s += " (all live bidders answered no-bid; item 3 not exercised)"
	}
	return s
}

// Verify reads NDJSON trace packets from r and checks them against opts.
func Verify(r io.Reader, opts Options) (Report, error) {
	packets, err := readPackets(r)
	if err != nil {
		return Report{}, err
	}
	if opts.ExpectPackets >= 0 && len(packets) != opts.ExpectPackets {
		return Report{}, fmt.Errorf("expected %d trace packets, got %d", opts.ExpectPackets, len(packets))
	}

	wantBidders := toSet(opts.Bidders)
	report := Report{Packets: len(packets)}
	indices := map[string]map[int]bool{} // partner → packet_index values seen
	for i, p := range packets {
		if err := checkPacket(i+1, p, opts, wantBidders); err != nil {
			return Report{}, err
		}
		if indices[p.PartnerID] == nil {
			indices[p.PartnerID] = map[int]bool{}
		}
		if indices[p.PartnerID][p.PacketIndex] {
			return Report{}, fmt.Errorf("packet %d: duplicate packet_index %d for partner %q", i+1, p.PacketIndex, p.PartnerID)
		}
		indices[p.PartnerID][p.PacketIndex] = true
		report.BidderResponses += len(p.BidderResponses)
	}
	// packet_index is 1-based per partner and packets are emitted in completion order, so the set per
	// partner must be exactly 1..N; line order is not asserted (concurrent auctions may complete out of order).
	for partner, seen := range indices {
		for n := 1; n <= len(seen); n++ {
			if !seen[n] {
				return Report{}, fmt.Errorf("partner %q: packet_index %d missing (got %v)", partner, n, sortedKeys(seen))
			}
		}
	}
	if opts.StrictBids && len(packets) > 0 && report.BidderResponses == 0 {
		return Report{}, errors.New("strict-bids: no live bidder response was recorded in any packet")
	}
	return report, nil
}

func readPackets(r io.Reader) ([]testtracer.TracePacket, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var packets []testtracer.TracePacket
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		if !strings.HasPrefix(raw, "{") {
			return nil, fmt.Errorf("line %d: stdout must carry only JSON packets, got %q", line, truncate(raw, 80))
		}
		var p testtracer.TracePacket
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, fmt.Errorf("line %d: invalid packet JSON: %w", line, err)
		}
		packets = append(packets, p)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read trace: %w", err)
	}
	return packets, nil
}

func checkPacket(index int, p testtracer.TracePacket, opts Options, wantBidders map[string]struct{}) error {
	pfx := fmt.Sprintf("packet %d", index)
	if p.Module != Module {
		return fmt.Errorf("%s: module %q, want %q", pfx, p.Module, Module)
	}
	if opts.PartnerID != "" && p.PartnerID != opts.PartnerID {
		return fmt.Errorf("%s: partner_id %q, want %q", pfx, p.PartnerID, opts.PartnerID)
	}
	if p.PacketIndex < 1 {
		return fmt.Errorf("%s: packet_index %d must be ≥ 1", pfx, p.PacketIndex)
	}
	if p.StartedAt.IsZero() || p.CompletedAt.Before(p.StartedAt) {
		return fmt.Errorf("%s: completed_at %s is not after started_at %s", pfx, p.CompletedAt, p.StartedAt)
	}

	incomingID, err := checkIncoming(pfx, p, opts)
	if err != nil {
		return err
	}
	if err := checkBidderRequests(pfx, p, incomingID, wantBidders); err != nil {
		return err
	}
	if err := checkBidderResponses(pfx, p, wantBidders); err != nil {
		return err
	}
	return checkFinal(pfx, p, incomingID, opts)
}

// checkIncoming validates item 1 and returns the auction id every other section must reference.
func checkIncoming(pfx string, p testtracer.TracePacket, opts Options) (string, error) {
	if p.IncomingRequest == nil {
		return "", fmt.Errorf("%s: incoming_request missing", pfx)
	}
	id, err := jsonID(p.IncomingRequest.Body)
	if err != nil {
		return "", fmt.Errorf("%s: incoming_request.body: %w", pfx, err)
	}
	if opts.AuctionID != "" && id != opts.AuctionID {
		return "", fmt.Errorf("%s: incoming_request.body.id %q, want %q", pfx, id, opts.AuctionID)
	}
	if p.IncomingRequest.Timestamp.After(p.StartedAt) {
		return "", fmt.Errorf("%s: incoming_request.timestamp is after started_at", pfx)
	}
	return id, nil
}

// checkBidderRequests validates item 2.
func checkBidderRequests(pfx string, p testtracer.TracePacket, incomingID string, wantBidders map[string]struct{}) error {
	got := make(map[string]struct{}, len(p.BidderRequests))
	for _, b := range p.BidderRequests {
		got[b.Bidder] = struct{}{}
		id, err := jsonID(b.Request)
		if err != nil {
			return fmt.Errorf("%s: bidder_request[%s].request: %w", pfx, b.Bidder, err)
		}
		if id != incomingID {
			return fmt.Errorf("%s: bidder_request[%s].request.id %q != incoming id %q", pfx, b.Bidder, id, incomingID)
		}
		if b.Timestamp.Before(p.IncomingRequest.Timestamp) {
			return fmt.Errorf("%s: bidder_request[%s] timestamp precedes the incoming request", pfx, b.Bidder)
		}
	}
	if len(wantBidders) > 0 && !sameSet(got, wantBidders) {
		return fmt.Errorf("%s: bidder_requests bidders %v, want %v", pfx, keys(got), keys(wantBidders))
	}
	return nil
}

// checkBidderResponses validates the shape of item 3; presence is governed by Options.StrictBids.
func checkBidderResponses(pfx string, p testtracer.TracePacket, wantBidders map[string]struct{}) error {
	for _, b := range p.BidderResponses {
		if len(wantBidders) > 0 {
			if _, ok := wantBidders[b.Bidder]; !ok {
				return fmt.Errorf("%s: bidder_responses has unexpected bidder %q", pfx, b.Bidder)
			}
		}
		if b.Response.Bids == nil {
			return fmt.Errorf("%s: bidder_responses[%s].response.bids must be an array", pfx, b.Bidder)
		}
	}
	return nil
}

// checkFinal validates item 4.
func checkFinal(pfx string, p testtracer.TracePacket, incomingID string, opts Options) error {
	if p.FinalResponse == nil {
		return fmt.Errorf("%s: final_response missing", pfx)
	}
	finalID, err := jsonID(p.FinalResponse.Body)
	if err != nil {
		return fmt.Errorf("%s: final_response.body: %w", pfx, err)
	}
	if finalID != incomingID {
		return fmt.Errorf("%s: final_response.body.id %q != incoming id %q", pfx, finalID, incomingID)
	}
	if opts.RequireDebug {
		var body struct {
			Ext struct {
				Debug json.RawMessage `json:"debug"`
			} `json:"ext"`
		}
		if err := json.Unmarshal(p.FinalResponse.Body, &body); err != nil || len(body.Ext.Debug) == 0 {
			return fmt.Errorf("%s: final_response.body.ext.debug missing — not the enriched response the client received", pfx)
		}
	}
	return nil
}

// CheckResponses verifies that every HTTP response file (JSON) shows the module in ext.prebid.modules
// with only successful hook outcomes. Requires ext.prebid.debug:true in the request.
func CheckResponses(paths []string) error {
	if len(paths) == 0 {
		return errors.New("no response files to check")
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path) //nolint:gosec // paths come from the operator's own e2e work directory
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var resp struct {
			Ext struct {
				Prebid struct {
					Modules json.RawMessage `json:"modules"`
				} `json:"prebid"`
			} `json:"ext"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("%s: invalid JSON: %w", filepath.Base(path), err)
		}
		mods := string(resp.Ext.Prebid.Modules)
		if mods == "" || mods == "null" {
			return fmt.Errorf("%s: ext.prebid.modules missing — hooks did not run", filepath.Base(path))
		}
		if !strings.Contains(mods, Module) {
			return fmt.Errorf("%s: %s not present in ext.prebid.modules", filepath.Base(path), Module)
		}
		compact := strings.ReplaceAll(mods, " ", "")
		for _, bad := range []string{`"status":"failure"`, `"status":"timeout"`, `"status":"execution_failure"`} {
			if strings.Contains(compact, bad) {
				return fmt.Errorf("%s: hook outcome %s", filepath.Base(path), bad)
			}
		}
		if !strings.Contains(compact, `"status":"success"`) {
			return fmt.Errorf("%s: no successful hook outcome recorded", filepath.Base(path))
		}
	}
	return nil
}

// CheckLog fails when the PBS log shows the module was planned but not found (registration broken).
func CheckLog(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		if strings.Contains(sc.Text(), NotFoundHookWarning) {
			return errors.New("PBS log: " + NotFoundHookWarning + " — module not compiled in or disabled")
		}
	}
	return sc.Err()
}

func jsonID(raw json.RawMessage) (string, error) {
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("not a JSON object: %w", err)
	}
	if v.ID == "" {
		return "", errors.New("missing id")
	}
	return v.ID, nil
}

func toSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			set[it] = struct{}{}
		}
	}
	return set
}

func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
