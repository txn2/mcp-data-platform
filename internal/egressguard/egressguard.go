// Package egressguard vets the outbound connections a server-side fetch makes
// on behalf of content the platform did not write: a URL an agent hands the
// util connection, a stylesheet or image a stored document names when the
// thumbnail renderer draws it. Such a fetch runs inside the network perimeter,
// so without a guard it reaches what the author of the URL cannot -- a cluster
// service, a cloud metadata endpoint, a host on the operator's overlay network.
//
// The guard resolves a hostname once, refuses every non-public answer, and
// dials the vetted address literally, so a public name cannot rebind to an
// internal address between the check and the connect. A redirect is re-dialed
// by the transport and passes the same check. An operator with trusted
// internal hosts lists their prefixes in an allow-list consulted before the
// internal-range block.
package egressguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	// connectTimeout bounds the dial step (TCP + TLS handshake) of every
	// guarded connection. The overall fetch is bounded by its caller's
	// context; a short dial bound makes an unreachable host fail fast
	// instead of consuming that whole budget.
	connectTimeout = 10 * time.Second

	// idleConnectionTimeout and maxIdleConnections size the pool for
	// occasional fan-out, not high-throughput traffic.
	idleConnectionTimeout = 90 * time.Second
	maxIdleConnections    = 10

	decimalBase = 10
	// portBits is the bitSize passed to strconv.ParseUint for a TCP port.
	portBits = 16
)

// cgnatPrefix is the carrier-grade NAT shared address space
// (RFC 6598, 100.64.0.0/10). Not covered by netip.Addr.IsPrivate but
// never a public-internet destination; overlay networks (Tailscale)
// also squat on it, so a fetch there would reach internal hosts.
//
//nolint:gochecknoglobals // immutable parsed constant
var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// embeddedIPv4Prefixes are IPv6 ranges that carry an IPv4 destination
// in their low bits. netip.Addr.Unmap only rewrites the IPv4-mapped
// form (::ffff:0:0/96), so an address in one of these prefixes escapes
// the IPv4-range classification below and would be dialed as a
// "public" v6 literal -- yet in a DNS64/NAT64 cluster (increasingly the
// default for IPv6-only Kubernetes) or behind a 6to4 relay it reaches
// the embedded IPv4, including RFC1918 and the 169.254.169.254 metadata
// endpoint (e.g. http://[64:ff9b::a9fe:a9fe]/). None is a legitimate
// public destination, so the whole prefix is refused rather than
// decoded-and-reclassified.
//
//nolint:gochecknoglobals // immutable parsed constant set
var embeddedIPv4Prefixes = []netip.Prefix{
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 well-known (RFC 6052)
	netip.MustParsePrefix("64:ff9b:1::/48"), // NAT64 local-use (RFC 8215)
	netip.MustParsePrefix("2002::/16"),      // 6to4 (RFC 3056)
	netip.MustParsePrefix("::/96"),          // IPv4-compatible, deprecated (RFC 4291)
}

// blockedHostnames and blockedHostnameSuffixes refuse well-known
// internal names before resolution. The IP filter is the real defense
// (cluster DNS names resolve to private ranges it already blocks);
// refusing the names too yields a precise error instead of a
// resolution-dependent one and covers split-horizon setups where the
// names resolve publicly.
//
//nolint:gochecknoglobals // immutable constant sets
var (
	blockedHostnames = map[string]bool{
		"localhost":                true,
		"metadata.google.internal": true,
	}
	blockedHostnameSuffixes = []string{
		".localhost",
		".svc.cluster.local",
		".cluster.local",
		".internal",
	}
)

// BlockedError is a dial the guard refused, as opposed to a network failure.
// A caller that surfaces it says the destination is not permitted rather than
// reporting a retryable upstream error, and words the remedy in its own terms:
// the setting that exempts a prefix belongs to whichever feature owns the
// guard.
type BlockedError struct {
	// Host is the hostname or literal address that was refused.
	Host string
	// Reason names the rule that refused it.
	Reason string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("destination %q refused: %s", e.Host, e.Reason)
}

// Guard vets every outbound dial a guarded client performs.
type Guard struct {
	// allowPrivate lists operator-permitted prefixes that override the
	// internal-range block (on-prem deployments fetching from trusted
	// internal hosts; tests reaching 127.0.0.1 servers).
	allowPrivate []netip.Prefix
	// lookup and dialIP are seams for tests (rebinding resolvers,
	// refused dials). Production values resolve via net.DefaultResolver
	// and dial the literal vetted address.
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	dialIP func(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error)
}

// New builds a guard, parsing the operator's allow-list of private prefixes.
// An unparseable entry is a hard error: silently dropping it would either
// block a destination the operator explicitly permitted or, if the intent was
// a broad prefix, go unnoticed until a legitimate fetch fails.
func New(allowPrivateCIDRs []string) (*Guard, error) {
	prefixes := make([]netip.Prefix, 0, len(allowPrivateCIDRs))
	for _, c := range allowPrivateCIDRs {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			return nil, fmt.Errorf("egressguard: invalid private prefix %q: %w", c, err)
		}
		prefixes = append(prefixes, p.Masked())
	}
	d := &net.Dialer{Timeout: connectTimeout}
	return &Guard{
		allowPrivate: prefixes,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host) //nolint:wrapcheck // transparent resolver seam
		},
		dialIP: func(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error) {
			return d.DialContext(ctx, network, addr.String()) //nolint:wrapcheck // transparent dialer seam
		},
	}, nil
}

// Transport returns an HTTP transport whose every dial passes the guard.
//
// It deliberately ignores proxy environment variables (Proxy is nil): an
// egress proxy sits inside the network perimeter, and routing a guarded fetch
// through it would let the proxy reach a destination the guard just refused.
func (g *Guard) Transport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           g.DialContext,
		TLSHandshakeTimeout:   connectTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       idleConnectionTimeout,
		MaxIdleConns:          maxIdleConnections,
	}
}

// hostnameBlocked refuses well-known internal names case-insensitively,
// tolerating a trailing FQDN dot.
func hostnameBlocked(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if blockedHostnames[h] {
		return true
	}
	for _, s := range blockedHostnameSuffixes {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

// ipBlockedReason classifies an address as non-public. Empty string
// means the address is a legitimate public-internet destination.
// v4-mapped v6 addresses are unmapped first so ::ffff:10.0.0.1 cannot
// smuggle past the v4 checks.
func ipBlockedReason(a netip.Addr) string {
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		return "loopback address"
	case a.IsPrivate():
		return "private address range"
	case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast():
		return "link-local address range (includes cloud metadata endpoints)"
	case a.IsMulticast():
		return "multicast address"
	case a.IsUnspecified():
		return "unspecified address"
	case cgnatPrefix.Contains(a):
		return "carrier-grade NAT address range"
	case containsAny(embeddedIPv4Prefixes, a):
		return "IPv4-in-IPv6 embedded address range (NAT64/6to4) not permitted"
	default:
		return ""
	}
}

// containsAny reports whether a falls in any of the prefixes.
func containsAny(prefixes []netip.Prefix, a netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// permitted applies the operator allow-list before the internal-range
// block: an explicitly listed prefix is reachable even when the
// classifier would refuse it.
func (g *Guard) permitted(a netip.Addr) (ok bool, reason string) {
	a = a.Unmap()
	for _, p := range g.allowPrivate {
		if p.Contains(a) {
			return true, ""
		}
	}
	if r := ipBlockedReason(a); r != "" {
		return false, r
	}
	return true, ""
}

// DialContext resolves the hostname once, filters the answers, and dials only
// the vetted literal addresses -- the pin that defeats DNS rebinding. A host
// whose every address is refused surfaces a *BlockedError; mixed answers dial
// the permitted subset only.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("egressguard: dial address %q: %w", addr, err)
	}
	port, err := strconv.ParseUint(portStr, decimalBase, portBits)
	if err != nil {
		return nil, fmt.Errorf("egressguard: dial port %q: %w", portStr, err)
	}
	if hostnameBlocked(host) {
		return nil, &BlockedError{Host: host, Reason: "internal hostname"}
	}
	addrs, err := g.lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("egressguard: resolving %q: %w", host, err)
	}
	var lastErr error
	for _, a := range addrs {
		ok, reason := g.permitted(a)
		if !ok {
			lastErr = &BlockedError{Host: host, Reason: reason}
			continue
		}
		conn, derr := g.dialIP(ctx, network, netip.AddrPortFrom(a.Unmap(), uint16(port)))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("egressguard: host %q resolved to no addresses", host)
	}
	return nil, lastErr
}
