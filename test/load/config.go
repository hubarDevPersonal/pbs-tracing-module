// Package load drives Prebid Server's auction endpoint at a constant request rate and summarizes the
// outcome. It backs the load tests in this directory (build tag "load"); it is test tooling, not part of
// the module.
package load

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// MaxRPS bounds the target rate. Above it the schedule interval drops below one millisecond, which is
// not a meaningful arrival rate for an HTTP auction.
const MaxRPS = 1000.0

// Config describes one load run. Every field is required; Validate reports the first invalid one.
type Config struct {
	URL              string        // auction endpoint
	Body             []byte        // request body sent with every request
	RPS              float64       // target arrival rate
	Duration         time.Duration // length of the scheduling window
	Concurrency      int           // maximum in-flight requests
	Timeout          time.Duration // per-request timeout
	MaxResponseBytes int64         // a larger response is counted as an error
}

// Validate checks that the configuration describes a runnable load.
func (c Config) Validate() error {
	switch {
	case c.URL == "":
		return errors.New("load: URL is required")
	case len(c.Body) == 0:
		return errors.New("load: Body is required")
	case c.RPS <= 0 || c.RPS > MaxRPS || math.IsNaN(c.RPS):
		return fmt.Errorf("load: RPS must be in (0, %v], got %v", MaxRPS, c.RPS)
	case c.Duration <= 0:
		return fmt.Errorf("load: Duration must be positive, got %s", c.Duration)
	case c.Concurrency <= 0:
		return fmt.Errorf("load: Concurrency must be positive, got %d", c.Concurrency)
	case c.Timeout <= 0:
		return fmt.Errorf("load: Timeout must be positive, got %s", c.Timeout)
	case c.MaxResponseBytes <= 0:
		return fmt.Errorf("load: MaxResponseBytes must be positive, got %d", c.MaxResponseBytes)
	}
	return nil
}

func (c Config) interval() time.Duration {
	return time.Duration(float64(time.Second) / c.RPS)
}
