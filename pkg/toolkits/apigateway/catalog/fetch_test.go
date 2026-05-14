package catalog

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubResolver lets tests pin a hostname to a chosen set of IPs
// without touching the real DNS.
type stubResolver struct {
	ips map[string][]net.IP
	err error
}

func (s *stubResolver) LookupIP(_ context.Context, _, host string) ([]net.IP, error) {
	if s.err != nil {
		return nil, s.err
	}
	if ips, ok := s.ips[host]; ok {
		return ips, nil
	}
	return nil, errors.New("no such host")
}

func TestFetchFromURL_RejectsNonHTTPSWhenInsecureNotAllowed(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "http://example.com/spec.json",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
	if !strings.Contains(err.Error(), "scheme must be https") {
		t.Fatalf("err message=%q", err.Error())
	}
}

func TestFetchFromURL_RejectsBogusScheme(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "file:///etc/passwd",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsEmptyHost(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://", FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsUnparseable(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://%zz", FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsLoopbackLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://127.0.0.1/spec.json",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("err message=%q", err.Error())
	}
}

func TestFetchFromURL_RejectsIPv6LoopbackLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://[::1]/spec.json",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsLinkLocalLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://169.254.169.254/spec.json",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsPrivateLiteral(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"https://10.0.0.1/spec", "https://172.16.0.1/spec", "https://192.168.1.1/spec"} {
		_, err := FetchFromURL(context.Background(), addr, FetchOptions{})
		if !errors.Is(err, ErrSSRFBlocked) {
			t.Fatalf("%s err=%v want ErrSSRFBlocked", addr, err)
		}
	}
}

func TestFetchFromURL_RejectsCGNATLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://100.64.1.1/spec",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsMulticastLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://224.0.0.1/spec",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsUnspecifiedLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://0.0.0.0/spec",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsPrivateViaDNS(t *testing.T) {
	t.Parallel()
	stub := &stubResolver{ips: map[string][]net.IP{
		"trap.example.com": {net.ParseIP("10.20.30.40")},
	}}
	_, err := FetchFromURL(context.Background(),
		"https://trap.example.com/spec.json",
		FetchOptions{Resolver: stub})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsResolverError(t *testing.T) {
	t.Parallel()
	stub := &stubResolver{err: errors.New("nx")}
	_, err := FetchFromURL(context.Background(), "https://x.example.com/y",
		FetchOptions{Resolver: stub})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsEmptyResolverResult(t *testing.T) {
	t.Parallel()
	stub := &stubResolver{ips: map[string][]net.IP{"empty.example.com": {}}}
	_, err := FetchFromURL(context.Background(),
		"https://empty.example.com/y", FetchOptions{Resolver: stub})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte("openapi: 3.0.0\ninfo:\n  title: t\n  version: '1'\npaths: {}\n"))
	}))
	defer srv.Close()
	res, err := FetchFromURL(context.Background(), srv.URL,
		FetchOptions{
			AllowInsecureScheme:  true,
			AllowPrivateNetworks: true,
			HTTPClient:           srv.Client(),
		})
	if err != nil {
		t.Fatalf("FetchFromURL: %v", err)
	}
	if !strings.Contains(res.Content, "openapi:") {
		t.Fatalf("unexpected content: %q", res.Content)
	}
	if res.ETag != `"abc123"` {
		t.Fatalf("etag=%q", res.ETag)
	}
	if res.FetchedAt.IsZero() {
		t.Fatal("FetchedAt zero")
	}
}

func TestFetchFromURL_RejectsNon2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := FetchFromURL(context.Background(), srv.URL,
		FetchOptions{
			AllowInsecureScheme:  true,
			AllowPrivateNetworks: true,
			HTTPClient:           srv.Client(),
		})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err=%v want ErrUpstream", err)
	}
}

func TestFetchFromURL_TooLarge(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()
	_, err := FetchFromURL(context.Background(), srv.URL,
		FetchOptions{
			AllowInsecureScheme:  true,
			AllowPrivateNetworks: true,
			MaxBytes:             512,
			HTTPClient:           srv.Client(),
		})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err=%v want ErrTooLarge", err)
	}
}

func TestFetchFromURL_DialError(t *testing.T) {
	t.Parallel()
	// Pin a public-looking hostname to an unroutable public IP so
	// preflight passes but the dial fails. The dialer error path is
	// what we want exercised here.
	stub := &stubResolver{ips: map[string][]net.IP{
		"public.example.com": {net.ParseIP("203.0.113.1")}, // TEST-NET-3
	}}
	_, err := FetchFromURL(context.Background(),
		"https://public.example.com:1/spec",
		FetchOptions{
			Resolver:       stub,
			ConnectTimeout: 50 * time.Millisecond,
			TotalTimeout:   200 * time.Millisecond,
		})
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestApplyFetchDefaults(t *testing.T) {
	t.Parallel()
	out := applyFetchDefaults(FetchOptions{})
	if out.MaxBytes != defaultFetchMaxBytes ||
		out.ConnectTimeout != defaultFetchConnectTimeout ||
		out.TotalTimeout != defaultFetchTotalTimeout {
		t.Fatalf("defaults not applied: %+v", out)
	}
}

func TestCheckDialAddress_AcceptsPublic(t *testing.T) {
	t.Parallel()
	if err := checkDialAddress("tcp4", "8.8.8.8:443"); err != nil {
		t.Fatalf("public dial address blocked: %v", err)
	}
}

func TestCheckDialAddress_RejectsLoopback(t *testing.T) {
	t.Parallel()
	err := checkDialAddress("tcp4", "127.0.0.1:443")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestCheckDialAddress_RejectsPrivate(t *testing.T) {
	t.Parallel()
	err := checkDialAddress("tcp4", "10.0.0.1:443")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestCheckDialAddress_RejectsCGNAT(t *testing.T) {
	t.Parallel()
	err := checkDialAddress("tcp4", "100.64.0.1:443")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestCheckDialAddress_BadHostPort(t *testing.T) {
	t.Parallel()
	err := checkDialAddress("tcp4", "garbage")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestCheckDialAddress_NonIPHost(t *testing.T) {
	t.Parallel()
	// Dialer only ever sees IPs, but the safety branch must report
	// rather than silently allow.
	err := checkDialAddress("tcp4", "example.com:443")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestNewFetchClient_BuildsBoth(t *testing.T) {
	t.Parallel()
	c := newFetchClient(FetchOptions{
		ConnectTimeout: time.Second,
		TotalTimeout:   2 * time.Second,
	})
	if c.Timeout != 2*time.Second {
		t.Fatalf("Timeout=%v", c.Timeout)
	}
	if c.CheckRedirect == nil {
		t.Fatal("CheckRedirect nil")
	}
	if err := c.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect returned %v want ErrUseLastResponse", err)
	}
	cAllow := newFetchClient(FetchOptions{
		ConnectTimeout:       time.Second,
		TotalTimeout:         2 * time.Second,
		AllowPrivateNetworks: true,
	})
	if cAllow.Transport == nil {
		t.Fatal("nil transport when private allowed")
	}
}

func TestBlockedIPReason_Public(t *testing.T) {
	t.Parallel()
	if r := blockedIPReason(net.ParseIP("8.8.8.8")); r != "" {
		t.Fatalf("public IP blocked: %q", r)
	}
	if r := blockedIPReason(net.ParseIP("2001:4860:4860::8888")); r != "" {
		t.Fatalf("public IPv6 blocked: %q", r)
	}
}

func TestBlockedIPReason_Ranges(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"127.0.0.1":   "loopback",
		"::1":         "loopback",
		"169.254.0.1": "link-local",
		"fe80::1":     "link-local",
		"224.0.0.1":   "link-local", // 224.0.0.0/24 is link-local-scope multicast
		"225.0.0.1":   "multicast",  // outside 224.0.0.0/24
		"0.0.0.0":     "unspecified",
		"10.0.0.1":    "private",
		"192.168.0.1": "private",
		"172.16.0.1":  "private",
		"100.64.1.1":  "carrier-grade-nat",
	}
	for ip, want := range cases {
		got := blockedIPReason(net.ParseIP(ip))
		if got != want {
			t.Fatalf("blockedIPReason(%s)=%q want %q", ip, got, want)
		}
	}
}
