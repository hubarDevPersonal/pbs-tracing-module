package testtracer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-02
func TestValidateRules(t *testing.T) {
	valid := Rule{PartnerID: "p1", Duration: time.Minute, TracePacketsAmount: 3}

	testCases := []struct {
		name    string
		rules   []Rule
		wantErr string // substring; empty means no error expected
	}{
		{name: "single valid rule", rules: []Rule{valid}},
		{name: "several valid rules", rules: []Rule{valid, {PartnerID: "p2", Duration: time.Second, TracePacketsAmount: 1}}},
		{name: "empty rule set is allowed", rules: nil},
		{name: "empty PartnerID", rules: []Rule{{PartnerID: "", Duration: time.Minute, TracePacketsAmount: 1}}, wantErr: "PartnerID"},
		{name: "zero Duration", rules: []Rule{{PartnerID: "p1", Duration: 0, TracePacketsAmount: 1}}, wantErr: "Duration"},
		{name: "negative Duration", rules: []Rule{{PartnerID: "p1", Duration: -time.Second, TracePacketsAmount: 1}}, wantErr: "Duration"},
		{name: "zero TracePacketsAmount", rules: []Rule{{PartnerID: "p1", Duration: time.Minute, TracePacketsAmount: 0}}, wantErr: "TracePacketsAmount"},
		{name: "negative TracePacketsAmount", rules: []Rule{{PartnerID: "p1", Duration: time.Minute, TracePacketsAmount: -1}}, wantErr: "TracePacketsAmount"},
		{name: "duplicate PartnerID", rules: []Rule{valid, {PartnerID: "p1", Duration: time.Hour, TracePacketsAmount: 9}}, wantErr: "duplicate"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRules(tc.rules)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// FR-02: the shipped rule set must pass its own validation.
func TestDefaultRulesAreValid(t *testing.T) {
	assert.NotEmpty(t, defaultRules)
	assert.NoError(t, validateRules(defaultRules))
}

// FR-02 / FR-03: the assessment's sample request must trigger tracing with the shipped rules.
func TestDefaultRulesCoverSampleRequestAccount(t *testing.T) {
	for _, r := range defaultRules {
		if r.PartnerID == sampleRequestAccountID {
			return
		}
	}
	t.Fatalf("defaultRules must contain PartnerID %q (Account.ID of 01-bid-request-example.json)", sampleRequestAccountID)
}
