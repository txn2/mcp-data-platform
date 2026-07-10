package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Resolver extracts the originating client IP from an HTTP request with
// trusted-proxy awareness. It exists because naively trusting the leftmost
// X-Forwarded-For entry is both spoofable (a caller sets any leftmost value
// to mint unlimited rate-limit buckets) and fragile behind a load balancer
// that does not forward the header (every client collapses onto the proxy
// IP). Neither failure mode is acceptable for a rate limiter guarding an
// unauthenticated, CPU-amplifying endpoint.
//
// Trust model: X-Forwarded-For is consulted ONLY when the direct peer
// (RemoteAddr) is itself a configured trusted proxy. In that case the client
// IP is the rightmost XFF entry that is not itself a trusted proxy — the last
// hop a trusted proxy actually observed, which a spoofing client cannot
// forge past the trust boundary. When no trusted proxies are configured, or
// the peer is not trusted, the header is ignored entirely and RemoteAddr is
// used. This makes the safe default (trust nothing) the zero value.
type Resolver struct {
	trusted []*net.IPNet
}

// NewResolver builds a Resolver from a list of trusted proxy CIDRs (e.g.
// "10.0.0.0/8", "127.0.0.1/32"). An empty list yields a resolver that always
// returns the direct peer address and never consults X-Forwarded-For. A
// malformed CIDR is a configuration error and is returned.
func NewResolver(cidrs []string) (*Resolver, error) {
	trusted := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, network, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", c, err)
		}
		trusted = append(trusted, network)
	}
	return &Resolver{trusted: trusted}, nil
}

// ClientIP returns the rate-limit key for the request: the originating client
// IP per the trust model documented on Resolver. The returned value is a bare
// IP string when parseable, otherwise the raw RemoteAddr as a last resort so
// the caller always gets a non-empty key.
func (r *Resolver) ClientIP(req *http.Request) string {
	peer := hostOnly(req.RemoteAddr)

	// Only consult forwarding headers when the direct peer is a trusted proxy.
	if !r.isTrusted(peer) {
		return peer
	}

	xff := req.Header.Get("X-Forwarded-For")
	if xff == "" {
		return peer
	}

	// Walk right-to-left: the rightmost entry is the address the nearest
	// (trusted) proxy saw. Skip entries that are themselves trusted proxies
	// to find the last untrusted hop — the real client.
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := hostOnly(strings.TrimSpace(parts[i]))
		if ip == "" {
			continue
		}
		if !r.isTrusted(ip) {
			return ip
		}
	}

	// Every forwarded hop was a trusted proxy (or unparseable); fall back to
	// the direct peer.
	return peer
}

// isTrusted reports whether ip parses and falls within a configured trusted
// proxy network.
func (r *Resolver) isTrusted(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, network := range r.trusted {
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// hostOnly strips an optional port from an address, returning the bare host.
// It accepts "host:port", "[ipv6]:port", a bare IP, or a bare host and never
// returns an error: an unparseable value is returned trimmed so the caller
// still receives a usable, non-empty key.
func hostOnly(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	// No port present (bare IP or host); strip IPv6 brackets if any.
	return strings.Trim(addr, "[]")
}
