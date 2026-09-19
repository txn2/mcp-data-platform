package egressguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestIPBlockedReason(t *testing.T) {
	tests := []struct {
		addr    string
		blocked bool
	}{
		{"127.0.0.1", true},           // loopback v4
		{"::1", true},                 // loopback v6
		{"10.1.2.3", true},            // RFC1918
		{"172.16.0.9", true},          // RFC1918
		{"192.168.1.1", true},         // RFC1918
		{"169.254.169.254", true},     // link-local / cloud metadata
		{"fe80::1", true},             // link-local v6
		{"fd12:3456::1", true},        // ULA (netip.IsPrivate)
		{"224.0.0.1", true},           // multicast
		{"0.0.0.0", true},             // unspecified
		{"100.64.0.7", true},          // CGNAT
		{"::ffff:10.0.0.1", true},     // v4-mapped private
		{"::ffff:169.254.1.1", true},  // v4-mapped link-local
		{"64:ff9b::a9fe:a9fe", true},  // NAT64 well-known embedding 169.254.169.254
		{"64:ff9b::7f00:1", true},     // NAT64 well-known embedding 127.0.0.1
		{"64:ff9b::0a00:1", true},     // NAT64 well-known embedding 10.0.0.1
		{"64:ff9b:1::a00:1", true},    // NAT64 local-use (RFC 8215)
		{"2002:a00:1::", true},        // 6to4 embedding 10.0.0.1
		{"::0a00:1", true},            // IPv4-compatible embedding 10.0.0.1
		{"93.184.216.34", false},      // public v4
		{"2606:2800:220:1::1", false}, // public v6
		{"8.8.8.8", false},            // public v4
	}
	for _, tt := range tests {
		got := ipBlockedReason(netip.MustParseAddr(tt.addr))
		if (got != "") != tt.blocked {
			t.Errorf("ipBlockedReason(%s) = %q; want blocked=%v", tt.addr, got, tt.blocked)
		}
	}
}

func TestHostnameBlocked(t *testing.T) {
	tests := []struct {
		host    string
		blocked bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{"localhost.", true},
		{"foo.localhost", true},
		{"api.default.svc.cluster.local", true},
		{"db.cluster.local", true},
		{"metadata.google.internal", true},
		{"vault.corp.internal", true},
		{"example.com", false},
		{"nsa-pusa01.app.example.net", false},
		{"my-bucket.s3.amazonaws.com", false},
	}
	for _, tt := range tests {
		if got := hostnameBlocked(tt.host); got != tt.blocked {
			t.Errorf("hostnameBlocked(%q) = %v; want %v", tt.host, got, tt.blocked)
		}
	}
}

func TestNew_InvalidCIDR(t *testing.T) {
	if _, err := New([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
	if _, err := New([]string{"127.0.0.1"}); err == nil {
		t.Fatal("expected error for bare IP (prefix required)")
	}
}

func TestGuard_AllowPrivateOverride(t *testing.T) {
	g, err := New([]string{" 127.0.0.0/8 ", "10.5.0.0/16"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, allowed := range []string{"127.0.0.1", "10.5.9.9"} {
		if ok, reason := g.permitted(netip.MustParseAddr(allowed)); !ok {
			t.Errorf("permitted(%s) = false (%s); want allowed via CIDR", allowed, reason)
		}
	}
	// A private address outside the allowed prefixes stays blocked.
	if ok, _ := g.permitted(netip.MustParseAddr("10.6.0.1")); ok {
		t.Error("permitted(10.6.0.1) = true; want blocked (outside allow list)")
	}
}

// stubGuard builds a guard whose resolver and dialer are test doubles.
// dialed records every address handed to the dialer. The guard carries
// no allow-list -- these tests exercise the classifier and dial path,
// not the operator exemption (covered by TestGuard_AllowPrivateOverride).
func stubGuard(t *testing.T, addrs []netip.Addr, lookupErr, dialErr error) (*Guard, *[]netip.AddrPort) {
	t.Helper()
	g, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	g.lookup = func(_ context.Context, _ string) ([]netip.Addr, error) {
		return addrs, lookupErr
	}
	dialed := &[]netip.AddrPort{}
	g.dialIP = func(_ context.Context, _ string, addr netip.AddrPort) (net.Conn, error) {
		*dialed = append(*dialed, addr)
		if dialErr != nil {
			return nil, dialErr
		}
		c, s := net.Pipe()
		s.Close() //nolint:errcheck,gosec // test pipe
		return c, nil
	}
	return g, dialed
}

func TestGuard_DialContext_BlockedHostname(t *testing.T) {
	g, dialed := stubGuard(t, nil, nil, nil)
	_, err := g.DialContext(context.Background(), "tcp", "internal.svc.cluster.local:443")
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v; want *BlockedError", err)
	}
	if len(*dialed) != 0 {
		t.Errorf("dialed %v; blocked hostname must never dial", *dialed)
	}
}

// TestGuard_DialContext_RebindingResolver is the DNS-rebinding case: a
// public-looking hostname whose resolver answer is a private address. The
// guard must refuse at dial time -- the only sound place, because any earlier
// check races a second resolution.
func TestGuard_DialContext_RebindingResolver(t *testing.T) {
	g, dialed := stubGuard(t, []netip.Addr{netip.MustParseAddr("10.0.0.5")}, nil, nil)
	_, err := g.DialContext(context.Background(), "tcp", "public-looking.example.com:443")
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v; want *BlockedError", err)
	}
	if len(*dialed) != 0 {
		t.Errorf("dialed %v; private resolution must never dial", *dialed)
	}
}

// TestGuard_DialContext_MixedAnswersDialsVettedOnly pins the pin: with mixed
// public+private answers, only the vetted public address is dialed, and it is
// dialed literally (not re-resolved).
func TestGuard_DialContext_MixedAnswersDialsVettedOnly(t *testing.T) {
	pub := netip.MustParseAddr("93.184.216.34")
	g, dialed := stubGuard(t, []netip.Addr{netip.MustParseAddr("169.254.169.254"), pub}, nil, nil)
	conn, err := g.DialContext(context.Background(), "tcp", "mixed.example.com:80")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	conn.Close() //nolint:errcheck,gosec // test conn
	if len(*dialed) != 1 || (*dialed)[0] != netip.AddrPortFrom(pub, 80) {
		t.Errorf("dialed %v; want exactly [%s:80]", *dialed, pub)
	}
}

func TestGuard_DialContext_Errors(t *testing.T) {
	errResolve := errors.New("resolve boom")
	errDial := errors.New("dial boom")
	pub := []netip.Addr{netip.MustParseAddr("93.184.216.34")}
	tests := []struct {
		name      string
		addr      string
		addrs     []netip.Addr
		lookupErr error
		dialErr   error
		wantErr   error
	}{
		{name: "resolver error", addr: "x.example.com:80", lookupErr: errResolve, wantErr: errResolve},
		{name: "dial error propagates", addr: "x.example.com:80", addrs: pub, dialErr: errDial, wantErr: errDial},
		{name: "no addresses", addr: "x.example.com:80"},
		{name: "missing port", addr: "x.example.com"},
		{name: "bad port", addr: "x.example.com:notaport"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _ := stubGuard(t, tt.addrs, tt.lookupErr, tt.dialErr)
			_, err := g.DialContext(context.Background(), "tcp", tt.addr)
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v; want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBlockedError_Text(t *testing.T) {
	e := &BlockedError{Host: "10.0.0.1", Reason: "private address range"}
	msg := e.Error()
	for _, want := range []string{"10.0.0.1", "private address range"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error text %q missing %q", msg, want)
		}
	}
}

// TestTransport_RoutesThroughGuardAndIgnoresProxy pins the two properties a
// guarded client depends on: the transport's dials pass the guard, and no
// proxy is consulted -- an egress proxy inside the perimeter would reach a
// destination the guard refused.
func TestTransport_RoutesThroughGuardAndIgnoresProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:3128")

	g, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr := g.Transport()
	if tr.Proxy != nil {
		t.Fatal("Transport().Proxy is set; a guarded transport must not consult a proxy")
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost:1/", http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v; want *BlockedError from the guard", err)
	}
}
