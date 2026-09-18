package e2e

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unit tests of the e2e assertions; they run without Docker in the default test suite.

const (
	testAuctionID = "5d394bed0104ca857c702982fe8d95e408820eb2-3"
	testPartner   = "664-025-677-881"
)

var testBidders = []string{"aceex", "appnexus", "amx", "adyoulike"}

func testPacket(idx int, bidders []string, withResponses, withDebug bool) string {
	reqs := make([]string, 0, len(bidders))
	resps := make([]string, 0, len(bidders))
	for _, b := range bidders {
		reqs = append(reqs, `{"timestamp":"2026-09-16T10:00:01Z","bidder":"`+b+`","request":{"id":"`+testAuctionID+`"}}`)
		if withResponses {
			resps = append(resps, `{"timestamp":"2026-09-16T10:00:02Z","bidder":"`+b+`","response":{"currency":"USD","bids":[{"bid":{"id":"x"}}]}}`)
		}
	}
	ext := `{}`
	if withDebug {
		ext = `{"debug":{"httpcalls":{}}}`
	}
	return `{"module":"test_provider.test_tracer","partner_id":"` + testPartner + `","packet_index":` + strconv.Itoa(idx) +
		`,"started_at":"2026-09-16T10:00:00.5Z","completed_at":"2026-09-16T10:00:03Z",` +
		`"incoming_request":{"timestamp":"2026-09-16T10:00:00Z","body":{"id":"` + testAuctionID + `"}},` +
		`"bidder_requests":[` + strings.Join(reqs, ",") + `],"bidder_responses":[` + strings.Join(resps, ",") + `],` +
		`"final_response":{"timestamp":"2026-09-16T10:00:03Z","body":{"id":"` + testAuctionID + `","ext":` + ext + `}}}` + "\n"
}

func testExpectation(packets int) expectation {
	return expectation{Packets: packets, PartnerID: testPartner, AuctionID: testAuctionID, Bidders: testBidders}
}

func TestVerifyTrace_AcceptsValidPackets(t *testing.T) {
	// packets of one partner may complete out of order
	in := testPacket(2, testBidders, true, true) + testPacket(1, testBidders, true, true) + testPacket(3, testBidders, true, true)
	responses, err := verifyTrace([]byte(in), testExpectation(3))
	require.NoError(t, err)
	assert.Equal(t, 12, responses)
}

func TestVerifyTrace_NoBidderResponseFailsOnlyWhenStrict(t *testing.T) {
	in := []byte(testPacket(1, testBidders, false, true))
	want := testExpectation(1)

	responses, err := verifyTrace(in, want)
	require.NoError(t, err)
	assert.Zero(t, responses)

	want.StrictBids = true
	_, err = verifyTrace(in, want)
	assert.ErrorContains(t, err, "no bidder response")
}

func TestVerifyTrace_Failures(t *testing.T) {
	valid := testPacket(1, testBidders, true, true)
	testCases := []struct {
		name    string
		input   string
		want    func(*expectation)
		wantErr string
	}{
		{name: "packet count", input: valid, want: func(e *expectation) { e.Packets = 3 }, wantErr: "expected 3 trace packets, got 1"},
		{name: "log line on stdout", input: "I0916 log line\n" + valid, wantErr: "stdout line 1 is not a trace packet"},
		{name: "partner", input: strings.Replace(valid, testPartner, "other", 1), wantErr: "partner_id"},
		{name: "index gap", input: testPacket(2, testBidders, true, true), wantErr: "packet_index 1 missing"},
		{name: "duplicate index", input: valid + valid, want: func(e *expectation) { e.Packets = 2 }, wantErr: "duplicate packet_index"},
		{name: "bidder set", input: testPacket(1, []string{"appnexus"}, true, true), wantErr: "bidder_requests bidders"},
		{name: "final response without debug", input: testPacket(1, testBidders, true, false), wantErr: "ext.debug missing"},
		{name: "auction id", input: valid, want: func(e *expectation) { e.AuctionID = "other" }, wantErr: "incoming_request.body.id"},
		{name: "incoming request missing", input: strings.Replace(valid, `"incoming_request"`, `"x"`, 1), wantErr: "incoming_request missing"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			want := testExpectation(1)
			if tc.want != nil {
				tc.want(&want)
			}
			_, err := verifyTrace([]byte(tc.input), want)
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func hookTrace(module, status string) string {
	return `{"id":"x","ext":{"prebid":{"modules":{"trace":{"stages":[{"stage":"entrypoint","outcomes":[{"groups":[{"invocation_results":[` +
		`{"hook_id":{"module_code":"` + module + `","hook_impl_code":"code"},"status":"` + status + `"}]}]}]}]}}}}}`
}

func TestCheckHookOutcomes(t *testing.T) {
	assert.NoError(t, checkHookOutcomes([]byte(hookTrace(moduleCode, "success"))))
	assert.ErrorContains(t, checkHookOutcomes([]byte(hookTrace(moduleCode, "timeout"))), `status "timeout"`)
	assert.ErrorContains(t, checkHookOutcomes([]byte(hookTrace("vendor.other", "success"))), "no hook of")
	assert.ErrorContains(t, checkHookOutcomes([]byte(`{"id":"x","ext":{"prebid":{}}}`)), "trace missing")
	assert.ErrorContains(t, checkHookOutcomes([]byte(`not json`)), "not JSON")
}

func TestCheckPBSLog(t *testing.T) {
	assert.NoError(t, checkPBSLog([]byte("I0916 fine\nW0916 something else\n")))
	assert.ErrorContains(t, checkPBSLog([]byte("W0916 "+notFoundHookWarning+" test_provider_test_tracer\n")), "not compiled in")
}
