package ratelimit

import (
	"fmt"
	"net"
	"strings"
)

// trustedPrefixShare bounds how wide a trusted proxy range may be: its prefix
// is at least a quarter of the address, /8 of IPv4 and /32 of IPv6. A wider
// range trusts the forwarding headers of a large part of the internet, which
// would let any client name its own address and escape its limit (#2556).
const trustedPrefixShare = 4

// CheckTrustedProxies refuses a trusted proxy list that does not parse, or
// that holds a range wider than /8 (IPv4) or /32 (IPv6). field names the
// setting in the refusal.
func CheckTrustedProxies(field string, cidrs []string) error {
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, network, err := net.ParseCIDR(c)
		if err != nil {
			return fmt.Errorf("the entry %q in %s is not a CIDR range such as 10.0.0.0/16", c, field)
		}
		if ones, bits := network.Mask.Size(); ones < bits/trustedPrefixShare {
			return fmt.Errorf("%s: %q trusts the forwarding headers of addresses that are not your proxies; "+
				"name the ingress's own range, no wider than /8 (IPv4) or /32 (IPv6)", field, c)
		}
	}
	return nil
}

// Proxies is the first of lists that names any proxy: a surface's own
// trusted proxies, and server.trusted_proxies when it names none (#2556).
func Proxies(lists ...[]string) []string {
	for _, l := range lists {
		if len(l) > 0 {
			return l
		}
	}
	return nil
}

// Bucket is the key a client address is limited under: an IPv4 address as
// it is, and an IPv6 address by its /64, since one host routinely holds a
// whole /64 and could otherwise change address for every request (#2556). A
// value that is not an address is its own key.
func Bucket(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
