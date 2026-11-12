//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2556: ip_rate_limit limits each client address on an
// endpoint, read through server.trusted_proxies, and req["client_ip"] is that
// address. Behind a trusted proxy two clients have their own limits and a
// forged leftmost X-Forwarded-For escapes nothing; with no trusted proxy the
// header is ignored; one IPv6 /64 is one bucket; the limit holds on the
// platform's host and on the page origin alike; a refusal is counted under its
// own reason; endpoint_set warns when no proxy is trusted; and the address
// reaches no audit row and no log.
//
// Wire forms: endpoint_set takes endpoint, path and auth_mode as strings,
// methods and callers as lists of strings, and ip_rate_limit and rate_limit as
// numbers (the only form each admits). A request is an HTTP GET with an
// X-Forwarded-For header, to the platform's host or naming the page origin in
// Host.

// issue2556Config is a platform on the dev stack's database and buckets that
// trusts the loopback address as its proxy.
const issue2556Config = `
server:
  name: acceptance-2556
  transport: http
  address: "%%s"
  trusted_proxies: ["127.0.0.1/32", "::1/128"]
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2556-key"
        name: "acceptance-2556"
        roles: ["admin"]
personas:
  admin:
    display_name: Admin
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
database:
  dsn: "postgres://platform:platform_secret@localhost:%s/mcp_platform?sslmode=disable"
  max_open_conns: 5
toolkits:
  s3:
    enabled: true
    instances:
      acc-2556:
        enabled: true
        endpoint: "http://localhost:%s"
        region: "us-east-1"
        access_key_id: "dev-access-key"
        secret_access_key: "dev-secret-key"
        use_path_style: true
    default: acc-2556
portal:
  s3_connection: acc-2556
  s3_bucket: portal-assets
  public_base_url: "http://acceptance-2556.localhost"
sites:
  pages_domain: pages.localhost
thumbnails:
  enabled: false
`

// app2556 is an application with one public endpoint answering its client's
// address, limited to two requests a minute for each address.
type app2556 struct {
	admin    *client
	name     string
	endpoint string
	host     string
}

func newApp2556(t *testing.T) *app2556 {
	t.Helper()
	admin := connect(t)
	name := "acc2556-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": name, "description": "Acceptance 2556"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	ep := name + "-ip"
	saveEndpointScript(t, admin, ep, `def handle(req):
    """Answers the address the platform sees."""
    return {"json": {"client_ip": req["client_ip"]}}
`, `{"params": {}, "caller": None, "client_ip": "203.0.113.1"}`)
	addScriptPart2517(t, admin, name, ep)
	bindEndpoint(t, admin, ep, "", map[string]any{
		"endpoint": ep, "methods": []string{"GET"}, "path": "/", "auth_mode": "public", "callers": []string{},
		"rate_limit": 600, "ip_rate_limit": 2,
	})
	admin.call("manage_application", map[string]any{"command": "release", "application": name, "note": "first"})
	return &app2556{admin: admin, name: name, endpoint: ep, host: name + "." + pagesDomain2517}
}

// trusting2556 is a platform trusting the loopback address as its proxy,
// waited on until it answers.
func trusting2556(t *testing.T) *platformProcess {
	t.Helper()
	p := startPlatform(t, fmt.Sprintf(issue2556Config, devPostgresPort(), devS3Port2616()))
	for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(500 * time.Millisecond) {
		if res, err := http.Get(p.base + "/healthz"); err == nil { // #nosec G107 -- the platform this test started
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return p
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the trusting platform did not answer within 2m:\n%s", p.out.String())
		}
	}
}

// ask sends one request from forwarded (as X-Forwarded-For, when not empty)
// to base, naming host when not empty, and returns the status and the
// address the handler saw.
func ask2556(t *testing.T, base, host, path, forwarded string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, http.NoBody) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	res, err := noRedirect.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	ip, _ := out["client_ip"].(string)
	return res.StatusCode, ip, res.Header
}

// Criteria 1, 3, 4, 5 and 6 behind a trusted proxy.
func TestIssue2556_BehindATrustedProxyEachClientHasItsOwnLimit(t *testing.T) {
	a := newApp2556(t)
	p := trusting2556(t)
	path := "/endpoints/" + a.endpoint

	t.Run("two clients, each its own limit", func(t *testing.T) {
		for range 2 {
			if status, ip, _ := ask2556(t, p.base, "", path, "203.0.113.10"); status != http.StatusOK || ip != "203.0.113.10" {
				t.Fatalf("client 10 answered %d with client_ip %q", status, ip)
			}
		}
		status, _, header := ask2556(t, p.base, "", path, "203.0.113.10")
		if status != http.StatusTooManyRequests || header.Get("Retry-After") == "" {
			t.Fatalf("client 10's third request answered %d (Retry-After %q), want 429", status, header.Get("Retry-After"))
		}
		if status, ip, _ := ask2556(t, p.base, "", path, "203.0.113.11"); status != http.StatusOK || ip != "203.0.113.11" {
			t.Fatalf("client 11 answered %d with client_ip %q, want its own limit", status, ip)
		}
	})
	t.Run("a forged leftmost entry escapes nothing", func(t *testing.T) {
		for _, fwd := range []string{"198.51.100.1, 203.0.113.12", "198.51.100.2, 203.0.113.12"} {
			if status, ip, _ := ask2556(t, p.base, "", path, fwd); status != http.StatusOK || ip != "203.0.113.12" {
				t.Fatalf("%q answered %d with client_ip %q", fwd, status, ip)
			}
		}
		if status, _, _ := ask2556(t, p.base, "", path, "198.51.100.3, 203.0.113.12"); status != http.StatusTooManyRequests {
			t.Fatalf("a third forged leftmost entry answered %d, want 429 from the client's own bucket", status)
		}
	})
	t.Run("one IPv6 /64 is one bucket", func(t *testing.T) {
		for _, ip := range []string{"2001:db8:1:2::a", "2001:db8:1:2::b"} {
			if status, got, _ := ask2556(t, p.base, "", path, ip); status != http.StatusOK || got != ip {
				t.Fatalf("%s answered %d with client_ip %q", ip, status, got)
			}
		}
		if status, _, _ := ask2556(t, p.base, "", path, "2001:db8:1:2:ffff::c"); status != http.StatusTooManyRequests {
			t.Fatalf("a third address in the /64 answered %d, want 429", status)
		}
	})
	t.Run("the platform's host and the page origin share the limit", func(t *testing.T) {
		if status, _, _ := ask2556(t, p.base, "", path, "203.0.113.13"); status != http.StatusOK {
			t.Fatalf("the platform's host answered %d", status)
		}
		if status, ip, _ := ask2556(t, p.base, a.host, path, "203.0.113.13"); status != http.StatusOK || ip != "203.0.113.13" {
			t.Fatalf("the page origin answered %d with client_ip %q", status, ip)
		}
		if status, _, _ := ask2556(t, p.base, a.host, path, "203.0.113.13"); status != http.StatusTooManyRequests {
			t.Fatalf("the page origin's third request answered %d, want 429", status)
		}
	})

	// Criterion 8: the address reaches no audit row and no log.
	var rows []any
	for range 40 {
		rows = a.admin.list("/api/v1/admin/audit/events?tool_name=endpoint:" + a.endpoint + "&per_page=50")
		if len(rows) >= 6 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	raw, _ := json.Marshal(rows)
	if len(rows) == 0 {
		t.Fatal("the requests wrote no audit row")
	}
	for _, ip := range []string{"203.0.113.10", "203.0.113.12", "2001:db8:1:2::a", "203.0.113.13"} {
		if strings.Contains(string(raw), ip) {
			t.Errorf("an audit row holds the client address %s", ip)
		}
		if strings.Contains(p.out.String(), ip) {
			t.Errorf("the platform's log holds the client address %s", ip)
		}
	}
}

// Criteria 2 and 6 with no trusted proxy: a forged X-Forwarded-For is
// ignored and the peer's address is used, and is the bucket.
func TestIssue2556_WithNoTrustedProxyAForgedHeaderIsIgnored(t *testing.T) {
	a := newApp2556(t)
	direct := "http://localhost:" + os.Getenv("DEV_API_PORT")
	path := "/endpoints/" + a.endpoint
	for _, forged := range []string{"203.0.113.20", "203.0.113.21"} {
		status, ip, _ := ask2556(t, direct, "", path, forged)
		if status != http.StatusOK || !net.ParseIP(ip).IsLoopback() {
			t.Fatalf("a forged %s answered %d with client_ip %q, want the peer's loopback address", forged, status, ip)
		}
	}
	if status, _, _ := ask2556(t, direct, "", path, "203.0.113.22"); status != http.StatusTooManyRequests {
		t.Fatalf("a third request with another forged header answered %d, want 429 from the peer's bucket", status)
	}
}

// Criterion 7: an address refusal is counted under its own reason.
func TestIssue2556_ARefusalIsCountedUnderItsOwnReason(t *testing.T) {
	a := newApp2556(t)
	direct := "http://localhost:" + os.Getenv("DEV_API_PORT")
	for range 4 {
		ask2556(t, direct, "", "/endpoints/"+a.endpoint, "")
	}
	var reasons any
	for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		_, body := a.admin.rest(http.MethodGet, "/api/v1/admin/endpoints/"+a.endpoint+"?range=day", http.NoBody)
		reasons = body["refused_by_reason"]
		if m, _ := reasons.(map[string]any); m["ip_rate_limit"] != nil {
			if m["rate_limit"] != nil {
				t.Errorf("the refusals were also counted as rate_limit: %v", m)
			}
			return
		}
	}
	t.Fatalf("no refusal was counted as ip_rate_limit within 45s: %v", reasons)
}

// The warning (requirement 6): endpoint_set on a deployment that trusts no
// proxy says every client behind one shares an address.
func TestIssue2556_EndpointSetWarnsWhenNoProxyIsTrusted(t *testing.T) {
	a := newApp2556(t)
	res, text, err := a.admin.callRaw("manage_script", map[string]any{"command": "endpoint_set", "name": a.endpoint, "ip_rate_limit": 3})
	if err != nil || res.IsError {
		t.Fatalf("endpoint_set: %v %s", err, text)
	}
	if !strings.Contains(text, "server.trusted_proxies is empty") {
		t.Fatalf("endpoint_set answered with no warning: %s", text)
	}
}
