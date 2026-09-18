package testtracer

import (
	"fmt"
	"time"
)

// Rule is one hardcoded tracing rule (assessment: "Tracing Parameters").
//
// PartnerID maps 1:1 to the Account.ID resolved by Prebid Server for a request.
// Duration is the length of the tracing window measured from the first traced request of the partner.
// TracePacketsAmount is the maximum number of traced auctions (packets) for the partner.
type Rule struct {
	PartnerID          string
	Duration           time.Duration
	TracePacketsAmount int
}

// defaultRules is the production rule set. The first entry matches 01-bid-request-example.json,
// whose Account.ID resolves to site.publisher.ext.prebid.parentAccount ("664-025-677-881").
var defaultRules = []Rule{
	{PartnerID: "664-025-677-881", Duration: 10 * time.Minute, TracePacketsAmount: 3},
	{PartnerID: "33415-10498", Duration: 30 * time.Second, TracePacketsAmount: 1},
}

// validateRules enforces FR-02: non-empty PartnerID, positive Duration and TracePacketsAmount,
// unique PartnerID. An empty rule set is valid.
func validateRules(rules []Rule) error {
	seen := make(map[string]struct{}, len(rules))
	for i, r := range rules {
		if r.PartnerID == "" {
			return fmt.Errorf("rule #%d: PartnerID must not be empty", i)
		}
		if r.Duration <= 0 {
			return fmt.Errorf("rule #%d (%s): Duration must be positive, got %s", i, r.PartnerID, r.Duration)
		}
		if r.TracePacketsAmount <= 0 {
			return fmt.Errorf("rule #%d (%s): TracePacketsAmount must be positive, got %d", i, r.PartnerID, r.TracePacketsAmount)
		}
		if _, dup := seen[r.PartnerID]; dup {
			return fmt.Errorf("rule #%d: duplicate PartnerID %q", i, r.PartnerID)
		}
		seen[r.PartnerID] = struct{}{}
	}
	return nil
}
