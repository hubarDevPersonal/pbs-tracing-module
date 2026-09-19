//go:build loadbench

package testtracer

import "time"

// defaultRules under the loadbench tag: partners whose limits outlast a whole load run, so the
// bench in test/load measures active tracing on every auction instead of on the first few.
// The production rule set is in rules_default.go; `make load-bench` builds the image with this tag.
var defaultRules = []Rule{
	{PartnerID: "load-partner-1", Duration: 24 * time.Hour, TracePacketsAmount: 1 << 30},
	{PartnerID: "load-partner-2", Duration: 24 * time.Hour, TracePacketsAmount: 1 << 30},
	{PartnerID: "load-partner-3", Duration: 24 * time.Hour, TracePacketsAmount: 1 << 30},
}
