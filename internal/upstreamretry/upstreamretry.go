// Package upstreamretry is the one reading of when an upstream HTTP answer is
// worth asking for again and how long the upstream asked to be left alone. The
// api gateway reports it on its results (#1859), a managed script's host acts
// on it, and the gateway's own page walk pauses by the same Retry-After parse
// (#1535), so the three cannot disagree about what a 429 means.
package upstreamretry

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The result keys an answer's advice is reported under. They are the contract
// between the tool that reports it and the host that acts on it.
const (
	KeyRetryable  = "upstream_retryable"
	KeyRetryAfter = "retry_after_seconds"
)

// The error code and category of a tool whose upstream timed out, dropped the
// connection or could not be reached: not the caller's fault, and the same
// call made later is expected to succeed. The error contract (pkg/middleware)
// sets them on such a result, and a managed script's host records a run ended
// by one as retryable.
const (
	CodeUnavailable     = "upstream_unavailable"
	CategoryUnavailable = "upstream"
)

// Advice is what one upstream answer says about asking again, as a tool
// result carries it: absent on an answer that is not worth repeating.
type Advice struct {
	// Retryable is set on an answer the same request is expected to get past
	// later (see Retryable).
	Retryable bool `json:"upstream_retryable,omitempty"`
	// RetryAfterSeconds is how long the upstream asked to be left alone, when
	// it said; zero when it did not.
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`
}

// Advise reads the advice of one answer to a request made with method.
func Advise(method string, status int, header http.Header, now time.Time) Advice {
	if !Retryable(method, status) {
		return Advice{}
	}
	out := Advice{Retryable: true}
	if wait, ok := After(header.Get("Retry-After"), now); ok {
		out.RetryAfterSeconds = int((wait + time.Second - 1) / time.Second)
	}
	return out
}

// Retryable reports whether an upstream that answered a method request with
// status is expected to admit the same request later. A 429 is, whatever the
// method: the upstream says it refused the request for its rate, not for its
// content, and did not act on it. A 503 is only for a GET or a HEAD, whose
// repetition cannot change anything, because an unavailable service may have
// done part of what a write asked before it answered.
func Retryable(method string, status int) bool {
	switch status {
	case http.StatusTooManyRequests:
		return true
	case http.StatusServiceUnavailable:
		m := strings.ToUpper(method)
		return m == http.MethodGet || m == http.MethodHead
	default:
		return false
	}
}

// After reads a Retry-After value in either form RFC 9110 allows: a delay in
// seconds or an HTTP date. A date in the past is a zero wait; an empty or
// unreadable value is no interval at all.
func After(value string, now time.Time) (time.Duration, bool) {
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
