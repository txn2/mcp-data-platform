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
package connstate

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
)

// The states a call can leave a connection in.
const (
	Healthy     = "healthy"
	Unreachable = "unreachable"
	AuthFailed  = "auth_failed"
)

type key struct{ kind, connection string }

var (
	mu    sync.RWMutex
	state = map[key]string{}
)

// Observe records the state a call found kind's connection in. A call with no
// connection name (a platform-wide client) and a state that is not one of
// Healthy, Unreachable and AuthFailed -- "" for an outcome that says nothing
// about the upstream -- are ignored, so the gauge's state label stays the set
// this package defines.
func Observe(kind, connection, s string) {
	if kind == "" || connection == "" || !known(s) {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	state[key{kind, connection}] = s
}

// known reports whether s is a state this package defines.
func known(s string) bool {
	return s == Healthy || s == Unreachable || s == AuthFailed
}

// State returns the state the last call found kind's connection in, and false
// when no call has used it in this process.
func State(kind, connection string) (string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	s, ok := state[key{kind, connection}]
	return s, ok
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
