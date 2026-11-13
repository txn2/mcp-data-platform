package ratelimit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckTrustedProxies(t *testing.T) {
	require.NoError(t, CheckTrustedProxies("server.trusted_proxies", nil))
	require.NoError(t, CheckTrustedProxies("server.trusted_proxies", []string{"10.0.0.0/8", "192.168.1.0/24", " ", "fd00::/32", "2001:db8::1/128"}))
	for cidr, want := range map[string]string{
		"0.0.0.0/0":     "no wider than /8",
		"10.0.0.0/7":    "no wider than /8",
		"::/0":          "/32 (IPv6)",
		"2001:db8::/31": "/32 (IPv6)",
		"10.0.0.1":      "is not a CIDR range",
		"nope":          "is not a CIDR range",
	} {
		err := CheckTrustedProxies("server.trusted_proxies", []string{cidr})
		require.ErrorContains(t, err, want, cidr)
		require.ErrorContains(t, err, "server.trusted_proxies", cidr)
	}
}

func TestProxies(t *testing.T) {
	assert.Equal(t, []string{"10.0.0.0/16"}, Proxies([]string{"10.0.0.0/16"}, []string{"172.16.0.0/12"}), "a surface's own list wins")
	assert.Equal(t, []string{"172.16.0.0/12"}, Proxies(nil, []string{}, []string{"172.16.0.0/12"}), "else server.trusted_proxies")
	assert.Nil(t, Proxies(nil, nil))
}

func TestBucket(t *testing.T) {
	assert.Equal(t, "203.0.113.7", Bucket("203.0.113.7"))
	assert.Equal(t, "203.0.113.7", Bucket("::ffff:203.0.113.7"), "an IPv4-mapped address is its IPv4 address")
	assert.Equal(t, Bucket("2001:db8:1:2:aaaa::1"), Bucket("2001:db8:1:2:bbbb:cccc:dddd:eeee"), "one /64 is one bucket")
	assert.Equal(t, "2001:db8:1:2::/64", Bucket("2001:db8:1:2:aaaa::1"))
	assert.NotEqual(t, Bucket("2001:db8:1:2::1"), Bucket("2001:db8:1:3::1"), "another /64 is another bucket")
	assert.Equal(t, "not-an-ip", Bucket("not-an-ip"))
}
