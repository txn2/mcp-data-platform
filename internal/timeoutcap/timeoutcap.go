// Package timeoutcap reports a caller's timeout_seconds a ceiling replaced
// (#2210): a connection's call_timeout, which a caller may lower but never
// raise, or a deployment's export max_timeout. The cap is right, and it is
// reported so a caller who asked for more can see what was applied instead,
// and a call stopped by it says which limit that was.
package timeoutcap

import (
	"fmt"
	"strings"
	"time"
)

// What Cap.CappedBy names: the connection's call_timeout, or the
// deployment's export max_timeout, replacing a caller's longer
// timeout_seconds.
const (
	CappedByCallTimeout      = "connection call_timeout"
	CappedByExportMaxTimeout = "export max_timeout"
)

// CallTimeoutOwner names the connection's own per-request limit in a
// timeout sentence.
const CallTimeoutOwner = "the connection's call_timeout"

// Cap reports a caller's timeout_seconds that a ceiling replaced. The zero
// value is no cap, and renders nothing.
type Cap struct {
	// AppliedSeconds is the timeout the call ran under.
	AppliedSeconds int `json:"timeout_applied_seconds,omitempty"`
	// CappedBy names the ceiling that replaced the caller's value.
	CappedBy string `json:"timeout_capped_by,omitempty"`
	// RequestedSeconds is the caller's own value.
	RequestedSeconds int `json:"timeout_requested_seconds,omitempty"`
}

// Capped reports whether a ceiling replaced the caller's value.
func (c Cap) Capped() bool { return c.CappedBy != "" }

// Apply is the timeout one call runs under: the caller's requested seconds
// when they are positive and no longer than ceiling, else the ceiling; by
// names the ceiling in the report.
func Apply(ceiling time.Duration, requested int, by string) (time.Duration, Cap) {
	if requested <= 0 {
		return ceiling, Cap{}
	}
	asked := time.Duration(requested) * time.Second
	if asked <= ceiling {
		return asked, Cap{}
	}
	return ceiling, Cap{AppliedSeconds: wholeSeconds(ceiling), CappedBy: by, RequestedSeconds: requested}
}

// TimedOut is the sentence a call that failed on its deadline is reported
// with: how long it ran under, which limit that was, and, when a caller's
// timeout_seconds was capped, that it was. limit is the timeout applied and
// owner names it ("the connection's call_timeout").
func (c Cap) TimedOut(limit time.Duration, owner string) string {
	capped := ""
	if c.Capped() {
		capped = fmt.Sprintf("; timeout_seconds %d was capped", c.RequestedSeconds)
	}
	return fmt.Sprintf("timed out after %ds (%s%s)", wholeSeconds(limit), owner, capped)
}

// IsTimeout reports whether an error message carries a timeout signature:
// the stable phrases Go's net/http, context and net error types emit.
// A plain "timeout" substring is not matched, since the word can appear in
// an upstream's own text.
func IsTimeout(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "client.timeout exceeded") ||
		strings.Contains(lower, "i/o timeout")
}

// wholeSeconds rounds d up to a whole second, so a sub-second ceiling is
// never reported as zero.
func wholeSeconds(d time.Duration) int {
	return int((d + time.Second - 1) / time.Second)
}

// Owner names the limit a timed-out request ran under: the connection's
// call_timeout when the connection's client stopped it (msg carries its
// "Client.Timeout exceeded") or the caller's value was capped to it, else the
// caller's own timeout_seconds when one was given.
func Owner(requested int, c Cap, msg string) string {
	if requested > 0 && !c.Capped() && !strings.Contains(strings.ToLower(msg), "client.timeout exceeded") {
		return "the call's timeout_seconds"
	}
	return CallTimeoutOwner
}

// Explain is msg with the sentence a timed-out request is reported with in
// front of it, and msg unchanged when it is not a timeout.
func Explain(msg string, timeout time.Duration, requested int, c Cap) string {
	if !IsTimeout(msg) {
		return msg
	}
	return c.TimedOut(timeout, Owner(requested, c, msg)) + ": " + msg
}
