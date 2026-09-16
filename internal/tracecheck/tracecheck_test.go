package tracecheck

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	auctionID = "5d394bed0104ca857c702982fe8d95e408820eb2-3"
	partner   = "664-025-677-881"
)

func packet(idx int, bidders []string, withResponses, withDebug bool) string {
	reqs := make([]string, 0, len(bidders))
	resps := make([]string, 0, len(bidders))
	for _, b := range bidders {
		reqs = append(reqs, `{"timestamp":"2026-09-16T10:00:01Z","bidder":"`+b+`","request":{"id":"`+auctionID+`"}}`)
		if withResponses {
			resps = append(resps, `{"timestamp":"2026-09-16T10:00:02Z","bidder":"`+b+`","response":{"currency":"USD","bids":[{"bid":{"id":"x","impid":"1","price":1}}]}}`)
		}
	}
	ext := `{}`
	if withDebug {
		ext = `{"debug":{"httpcalls":{}}}`
	}
	return `{"module":"test_provider.test_tracer","partner_id":"` + partner + `","rule":{"partner_id":"` + partner + `","duration":"10m0s","trace_packets_amount":3},` +
		`"packet_index":` + strconv.Itoa(idx) + `,"auction_id":"` + auctionID + `","started_at":"2026-09-16T10:00:00.5Z","completed_at":"2026-09-16T10:00:03Z",` +
		`"incoming_request":{"timestamp":"2026-09-16T10:00:00Z","body":{"id":"` + auctionID + `"}},` +
		`"bidder_requests":[` + strings.Join(reqs, ",") + `],"bidder_responses":[` + strings.Join(resps, ",") + `],` +
		`"final_response":{"timestamp":"2026-09-16T10:00:03Z","body":{"id":"` + auctionID + `","ext":` + ext + `}}}`
}

var sampleBidders = []string{"aceex", "appnexus", "amx", "adyoulike"}

func defaultOpts() Options {
	return Options{ExpectPackets: 3, PartnerID: partner, AuctionID: auctionID, Bidders: sampleBidders, RequireDebug: true}
}

func TestVerify_HappyPath(t *testing.T) {
	in := packet(1, sampleBidders, true, true) + "\n" + packet(2, sampleBidders, true, true) + "\n" + packet(3, sampleBidders, true, true) + "\n"
	rep, err := Verify(strings.NewReader(in), defaultOpts())
	require.NoError(t, err)
	assert.Equal(t, 3, rep.Packets)
	assert.Equal(t, 12, rep.BidderResponses)
	assert.Contains(t, rep.String(), "OK: 3 packets")
}

func TestVerify_LiveNoBidIsAcceptedUnlessStrict(t *testing.T) {
	in := packet(1, sampleBidders, false, true) + "\n"
	opts := defaultOpts()
	opts.ExpectPackets = 1

	rep, err := Verify(strings.NewReader(in), opts)
	require.NoError(t, err)
	assert.Equal(t, 0, rep.BidderResponses)
	assert.Contains(t, rep.String(), "item 3 not exercised")

	opts.StrictBids = true
	_, err = Verify(strings.NewReader(in), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "strict-bids")
}

func TestVerify_Failures(t *testing.T) {
	testCases := []struct {
		name    string
		input   string
		mutate  func(*Options)
		wantErr string
	}{
		{name: "wrong packet count", input: packet(1, sampleBidders, true, true), wantErr: "expected 3 trace packets, got 1"},
		{name: "non-JSON line on stdout", input: "I0916 log line\n" + packet(1, sampleBidders, true, true), wantErr: "only JSON packets"},
		{name: "wrong partner", input: strings.Replace(packet(1, sampleBidders, true, true), partner, "other", 1), mutate: func(o *Options) { o.ExpectPackets = 1 }, wantErr: "partner_id"},
		{name: "packet index gap", input: packet(2, sampleBidders, true, true), mutate: func(o *Options) { o.ExpectPackets = 1 }, wantErr: "packet_index 2, want 1"},
		{name: "missing bidder", input: packet(1, []string{"appnexus"}, true, true), mutate: func(o *Options) { o.ExpectPackets = 1 }, wantErr: "bidder_requests bidders"},
		{name: "no debug ext in final response", input: packet(1, sampleBidders, true, false), mutate: func(o *Options) { o.ExpectPackets = 1 }, wantErr: "ext.debug missing"},
		{name: "wrong auction id", input: packet(1, sampleBidders, true, true), mutate: func(o *Options) { o.ExpectPackets = 1; o.AuctionID = "nope" }, wantErr: "incoming_request.body.id"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := defaultOpts()
			if tc.mutate != nil {
				tc.mutate(&opts)
			}
			_, err := Verify(strings.NewReader(tc.input+"\n"), opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestVerify_DisabledChecks(t *testing.T) {
	in := packet(1, []string{"onlyone"}, false, false) + "\n"
	_, err := Verify(strings.NewReader(in), Options{ExpectPackets: -1})
	assert.NoError(t, err)
}

func TestCheckResponses(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	good := write(
		"good.json",
		`{"id":"x","ext":{"prebid":{"modules":{"trace":{"stages":[{"stage":"entrypoint","outcomes":[{"groups":[{"invocation_results":[{"hook_id":{"module_code":"test_provider.test_tracer"},"status":"success"}]}]}]}]}}}}}`,
	)
	noModules := write("nomods.json", `{"id":"x","ext":{"prebid":{}}}`)
	failure := write(
		"fail.json",
		`{"id":"x","ext":{"prebid":{"modules":{"trace":{"stages":[{"outcomes":[{"groups":[{"invocation_results":[{"hook_id":{"module_code":"test_provider.test_tracer"},"status":"failure"}]}]}]}]}}}}}`,
	)
	other := write(
		"other.json",
		`{"id":"x","ext":{"prebid":{"modules":{"trace":{"stages":[{"outcomes":[{"groups":[{"invocation_results":[{"hook_id":{"module_code":"vendor.other"},"status":"success"}]}]}]}]}}}}}`,
	)

	assert.NoError(t, CheckResponses([]string{good}))
	assert.ErrorContains(t, CheckResponses([]string{noModules}), "ext.prebid.modules missing")
	assert.ErrorContains(t, CheckResponses([]string{failure}), `"status":"failure"`)
	assert.ErrorContains(t, CheckResponses([]string{other}), "not present")
	assert.ErrorContains(t, CheckResponses(nil), "no response files")
}

func TestCheckLog(t *testing.T) {
	assert.NoError(t, CheckLog(strings.NewReader("I0916 fine\nW0916 something else\n")))
	assert.ErrorContains(t, CheckLog(strings.NewReader("W0916 "+NotFoundHookWarning+" test_provider_test_tracer\n")), "module not compiled in")
}
