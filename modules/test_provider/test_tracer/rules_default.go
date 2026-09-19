//go:build !loadbench

package testtracer

import "time"

// defaultRules is the production rule set. The first entry matches 01-bid-request-example.json,
// whose Account.ID resolves to site.publisher.ext.prebid.parentAccount ("664-025-677-881").
var defaultRules = []Rule{
	{PartnerID: "664-025-677-881", Duration: 10 * time.Minute, TracePacketsAmount: 3},
	{PartnerID: "33415-10498", Duration: 30 * time.Second, TracePacketsAmount: 1},
}
