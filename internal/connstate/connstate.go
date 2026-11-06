// Package connstate holds each connection's state as the calls that used it
// last found it: healthy, unreachable, or auth_failed (#1898). It is fed by
// the places that see an upstream's answer -- the outbound HTTP chain for the
// api, graphql and mcp kinds, the Trino and S3 toolkits' telemetry --
// and read when mcp_platform_connections is sampled. Nothing here sends a
// request: a connection nobody has called has no state, and is reported as
// configured.
//
// The state is per process. Each replica reports the connections it has
// called, so the gauge is read with max by (kind, state) across replicas.
//
// A recorded state stands for the maximum age (server.connection_state_max_age,
// DefaultMaxAge) and then reads as configured again, and every move into or
// out of a failure is logged naming the connection (#2442).
package connstate

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// The states a call can leave a connection in.
const (
	Healthy     = "healthy"
	Unreachable = "unreachable"
	AuthFailed  = "auth_failed"
	// configured is what a connection with no current record reads as: the
	// from_state of its first recorded call, and of a call after its record
	// aged out.
	configured = "configured"
)

// DefaultMaxAge is how long a recorded state stands with no call to confirm
// it (#2442). A connection that failed once and was not called again used to
// report the failure until the process restarted.
const DefaultMaxAge = 15 * time.Minute

type key struct{ kind, connection string }

// entry is what the last call found: the state, the reason it read as, and
// when.
type entry struct {
	state  string
	reason string
	at     time.Time
}

var (
	mu     sync.RWMutex
	state  = map[key]entry{}
	maxAge = DefaultMaxAge
	now    = time.Now
)

// SetMaxAge sets how long a recorded state stands with no call to confirm
// it; zero or less is DefaultMaxAge. Set once at startup from
// server.connection_state_max_age.
func SetMaxAge(d time.Duration) {
	if d <= 0 {
		d = DefaultMaxAge
	}
	mu.Lock()
	defer mu.Unlock()
	maxAge = d
}

// Observe records the state a call found kind's connection in, with the
// reason its state alone gives. A call with no connection name (a
// platform-wide client) and a state that is not one of Healthy, Unreachable
// and AuthFailed -- "" for an outcome that says nothing about the upstream --
// are ignored, so the gauge's state label stays the set this package defines.
func Observe(kind, connection, s string) {
	record(kind, connection, s, stateReason(s))
}

// ObserveHTTP records the state an HTTP answer or transport error leaves
// kind's connection in (FromHTTP), with the reason HTTPReason reads from it.
func ObserveHTTP(kind, connection string, status int, err error) {
	record(kind, connection, FromHTTP(status, err), HTTPReason(status, err))
}

// ObserveStatus records s, a state the caller read from an HTTP status by its
// own rule, with the reason HTTPReason reads from that status.
func ObserveStatus(kind, connection, s string, status int) {
	record(kind, connection, s, HTTPReason(status, nil))
}

// record keeps what one call found and logs a move into or out of a failure.
func record(kind, connection, s, reason string) {
	if kind == "" || connection == "" || !known(s) {
		return
	}
	k := key{kind, connection}
	mu.Lock()
	t := now()
	prev, had := state[k]
	if had && t.Sub(prev.at) > maxAge {
		had = false
	}
	state[k] = entry{state: s, reason: reason, at: t}
	mu.Unlock()
	from := configured
	if had {
		from = prev.state
	}
	logTransition(kind, connection, from, s, reason)
}

// logTransition writes one record when a call moves a connection into a
// failure, at WARN, or out of one, at INFO (#2442), so an operator reading the
// logs learns which connection to fix. A first call that finds a connection
// healthy is not a recovery and writes nothing. The record carries the
// connection's name, its kind, both states and the reason, a word from a fixed
// vocabulary: never the upstream's address, its answer or an error's text.
func logTransition(kind, connection, from, to, reason string) {
	if from == to || (from == configured && to == Healthy) {
		return
	}
	attrs := []any{
		"connection", logsan.SanitizeForLog(connection),
		"kind", kind,
		"from_state", from,
		"to_state", to,
		"reason", reason,
	}
	if to == Healthy {
		slog.Info("connection recovered", attrs...)
		return
	}
	slog.Warn("connection state changed", attrs...)
}

// Forget drops what calls found kind's connection in (#2444): the connection
// was deleted, or its configuration changed, so the next call on the
// connection now under that name says what it is. A connection re-created
// under a deleted one's name, or saved with a new credential, otherwise read
// as the old one's last state until a call used it.
func Forget(kind, connection string) {
	mu.Lock()
	defer mu.Unlock()
	delete(state, key{kind, connection})
}

// known reports whether s is a state this package defines.
func known(s string) bool {
	return s == Healthy || s == Unreachable || s == AuthFailed
}

// State returns the state the last call found kind's connection in, and false
// when no call has used it in this process or the last one is older than the
// maximum age.
func State(kind, connection string) (string, bool) {
	o, ok := Observed(kind, connection)
	return o.State, ok
}

// Observation is a connection's current record.
type Observation struct {
	State  string
	Reason string
	At     time.Time
}

// Observed returns kind's connection's current record, and false when it has
// none: no call used it in this process, or the last one aged out.
func Observed(kind, connection string) (Observation, bool) {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := state[key{kind, connection}]
	if !ok || now().Sub(e.at) > maxAge {
		return Observation{}, false
	}
	return Observation{State: e.state, Reason: e.reason, At: e.at}, true
}

// The reasons a state is recorded with. A reason is chosen from this list by
// this package, so a log record or label carrying one carries nothing an
// upstream wrote.
const (
	ReasonAnswered          = "answered"
	ReasonCredentialRefused = "credential_refused" // #nosec G101 -- a reason word a log record carries, not a credential
	ReasonNetworkError      = "network_error"
	ReasonTimeout           = "timeout"
	ReasonTransportError    = "transport_error"
)

// minStatus and maxStatus bound the HTTP statuses a reason names by class.
const (
	minStatus = 100
	maxStatus = 599
)

// stateReason is the reason a state gives when the caller has nothing more.
func stateReason(s string) string {
	switch s {
	case AuthFailed:
		return ReasonCredentialRefused
	case Unreachable:
		return ReasonNetworkError
	}
	return ReasonAnswered
}

// HTTPReason is the reason an HTTP answer gives: http_401, http_407, http_502,
// http_503 and http_504 for the statuses FromHTTP reads as a failure, the
// status class (http_2xx, http_4xx, ...) for any other, timeout for an error
// that is one, and transport_error for any other error. Only the error's type
// is read, never its text.
func HTTPReason(status int, err error) string {
	if err != nil {
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
			return ReasonTimeout
		}
		return ReasonTransportError
	}
	switch status {
	case http.StatusUnauthorized, http.StatusProxyAuthRequired,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "http_" + strconv.Itoa(status)
	}
	if status < minStatus || status > maxStatus {
		return ReasonAnswered
	}
	return "http_" + strconv.Itoa(status/100) + "xx"
}

// FromHTTP is the state an HTTP answer leaves a connection in: 401 and 407 are
// a refused credential; a transport error, a timeout, or a 502, 503 or 504 is
// an upstream that could not be reached or could not answer; any other status
// is an upstream that answered, which is healthy whatever it said about the
// request itself. A caller that canceled the request says nothing about the
// upstream, and records nothing.
func FromHTTP(status int, err error) string {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ""
		}
		return Unreachable
	}
	switch status {
	case http.StatusUnauthorized, http.StatusProxyAuthRequired:
		return AuthFailed
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return Unreachable
	case 0:
		return ""
	default:
		return Healthy
	}
}

// FromResultText is the state a failed tool result leaves a connection in when
// the result is all a caller has: its text names a refused credential (one of
// credential, the client's own stable error texts or codes), a lost network
// (Go's net error texts), or an upstream that answered and refused the
// request, which is healthy.
func FromResultText(text string, credential ...string) string {
	lower := strings.ToLower(text)
	for _, c := range credential {
		if strings.Contains(lower, strings.ToLower(c)) {
			return AuthFailed
		}
	}
	for _, n := range networkTexts {
		if strings.Contains(lower, n) {
			return Unreachable
		}
	}
	return Healthy
}

// networkTexts are the texts Go's net package and its HTTP client put in an
// error for an upstream that could not be reached.
//
//nolint:gochecknoglobals // a read-only lookup list.
var networkTexts = []string{
	"connection refused",
	"no such host",
	"i/o timeout",
	"connection reset by peer",
	"network is unreachable",
	"context deadline exceeded",
}
