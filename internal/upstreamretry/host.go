package upstreamretry

import (
	"fmt"
	"net/http"
	"time"
)

// The host side (#1859). A managed script's host reads the advice a tool result
// carries, waits and issues the call again, a bounded number of times and never
// past the run's deadline, and then hands the script whatever the upstream
// answered last: a script has no clock and no try/except, so a throttled
// request it cannot outwait itself is a run it cannot finish.
const (
	// MaxRetries is how many times one call is issued again.
	MaxRetries = 3
	// firstUpstreamWait is the wait when the upstream names no interval; it
	// doubles with each retry.
	firstUpstreamWait = time.Second
	// maxUpstreamWait is the longest single wait the host makes. An upstream
	// asking for longer is answered by handing its refusal to the script,
	// which can record the bad day and carry on.
	maxUpstreamWait = time.Minute
)

// Seen is what one tool result says about issuing the call again.
type Seen struct {
	Retryable bool
	// Status is the upstream's HTTP status, read from upstream_status
	// (api_export) or status (api_invoke_endpoint).
	Status int
	// After is the interval the upstream asked for; zero when it named none.
	After time.Duration
}

// FromResult reads the advice a tool result carries.
func FromResult(out map[string]any) Seen {
	a := Seen{}
	a.Retryable, _ = out[KeyRetryable].(bool)
	if !a.Retryable {
		return a
	}
	if secs, ok := out[KeyRetryAfter].(float64); ok && secs > 0 {
		a.After = time.Duration(secs * float64(time.Second))
	}
	for _, key := range []string{"upstream_status", "status"} {
		if status, ok := out[key].(float64); ok && status > 0 {
			a.Status = int(status)
			break
		}
	}
	return a
}

// Wait is how long to wait before issuing the call again as retry number
// retry (zero-based), given how long the run has left; ok is false when the
// call is not to be issued again and its answer goes to the script.
func (a Seen) Wait(retry int, remaining time.Duration) (time.Duration, bool) {
	if !a.Retryable || retry >= MaxRetries {
		return 0, false
	}
	wait := a.After
	if wait <= 0 {
		wait = firstUpstreamWait << retry
	}
	if wait > maxUpstreamWait || wait >= remaining {
		return 0, false
	}
	return wait, true
}

// Answer names the upstream's answer for the run log: "429 Too Many Requests".
func (a Seen) Answer() string {
	if a.Status == 0 {
		return "a refusal to retry later"
	}
	return fmt.Sprintf("%d %s", a.Status, http.StatusText(a.Status))
}
