package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResolverInvalidCIDR(t *testing.T) {
	_, err := NewResolver([]string{"not-a-cidr"})
	require.Error(t, err)

	r, err := NewResolver([]string{"10.0.0.0/8", "  ", ""})
	require.NoError(t, err)
	require.NotNil(t, r)
}

func TestResolverNoTrustedProxies(t *testing.T) {
	// With no trusted proxies, XFF is ignored entirely and the direct peer
	// is always returned — the spoof-safe default.
	r, err := NewResolver(nil)
	require.NoError(t, err)

	tests := []struct {
		name     string
		xff      string
		remote   string
		expected string
	}{
		{"ignores xff", "1.2.3.4", "5.6.7.8:1234", "5.6.7.8"},
		{"bare ip remote", "", "5.6.7.8", "5.6.7.8"},
		{"spoofed xff ignored", "9.9.9.9, 8.8.8.8", "5.6.7.8:1234", "5.6.7.8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newReq(tt.remote, tt.xff)
			assert.Equal(t, tt.expected, r.ClientIP(req))
		})
	}
}

func TestResolverTrustedProxy(t *testing.T) {
	r, err := NewResolver([]string{"10.0.0.0/8", "127.0.0.1/32"})
	require.NoError(t, err)

	tests := []struct {
		name     string
		xff      string
		remote   string
		expected string
	}{
		{
			name:     "trusted peer, single client hop",
			xff:      "203.0.113.5",
			remote:   "10.1.2.3:443",
			expected: "203.0.113.5",
		},
		{
			name:     "trusted peer, client then trusted proxy chain (rightmost untrusted wins)",
			xff:      "203.0.113.5, 10.9.9.9",
			remote:   "10.1.2.3:443",
			expected: "203.0.113.5",
		},
		{
			name:     "spoofed leftmost entry is skipped, real client is rightmost untrusted",
			xff:      "1.1.1.1, 203.0.113.5, 10.9.9.9",
			remote:   "10.1.2.3:443",
			expected: "203.0.113.5",
		},
		{
			name:     "untrusted peer ignores xff even if present",
			xff:      "203.0.113.5",
			remote:   "198.51.100.7:443",
			expected: "198.51.100.7",
		},
		{
			name:     "trusted peer, empty xff falls back to peer",
			xff:      "",
			remote:   "10.1.2.3:443",
			expected: "10.1.2.3",
		},
		{
			name:     "trusted peer, all hops trusted falls back to peer",
			xff:      "10.9.9.9, 127.0.0.1",
			remote:   "10.1.2.3:443",
			expected: "10.1.2.3",
		},
		{
			name:     "ipv6 loopback trusted via /32 not matched (distinct family)",
			xff:      "203.0.113.5",
			remote:   "127.0.0.1:443",
			expected: "203.0.113.5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newReq(tt.remote, tt.xff)
			assert.Equal(t, tt.expected, r.ClientIP(req))
		})
	}
}

func TestHostOnly(t *testing.T) {
	tests := []struct {
		in, out string
	}{
		{"5.6.7.8:1234", "5.6.7.8"},
		{"5.6.7.8", "5.6.7.8"},
		{"[::1]:8080", "::1"},
		{"  1.2.3.4  ", "1.2.3.4"},
		{"", ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.out, hostOnly(tt.in), "hostOnly(%q)", tt.in)
	}
}

func newReq(remote, xff string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = remote
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	return req
}
