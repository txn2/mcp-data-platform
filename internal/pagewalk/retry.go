package pagewalk

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryAfterPause reads the pause an upstream asks for on a 429 or 503.
// ok is false for any other status, and for one of those two that names
// no interval: the gateway paces by the upstream's own instruction, and
// with none the page fails like any other failed page rather than being
// retried on a guess.
func retryAfterPause(resp *http.Response, now time.Time) (wait time.Duration, ok bool) {
	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable {
		return 0, false
	}
	return parseRetryAfter(resp.Header.Get("Retry-After"), now)
}

// parseRetryAfter reads a Retry-After value in either form RFC 9110
// allows: a delay in seconds or an HTTP date. A date in the past is a
// zero wait.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(value); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second, true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return max(at.Sub(now), 0), true
}

// waitRetryAfter pauses the walk for the upstream's interval, bounded by
// the call's timeout: a pause the deadline cannot contain fails now,
// naming the interval, instead of sleeping into a timeout that would
// name nothing.
func waitRetryAfter(ctx context.Context, wait time.Duration) error {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
		return fmt.Errorf("apigateway: upstream asked to retry after %s, past the call's remaining timeout", wait)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // the deadline is the caller's; its own error names it
	case <-timer.C:
		return nil
	}
}
