//go:build integration

package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpserver/pdfhttp"
	"github.com/txn2/mcp-data-platform/internal/routescan"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// routesOutsideTheListener are the source files whose registrations are on a
// mux the platform's listener never serves, so the gate does not send them
// to it: the Prometheus scrape listener, and the util connection's in-process
// handler, which no socket reaches.
var routesOutsideTheListener = map[string]string{
	"pkg/observability/listener.go":            "the /metrics listener, its own http.Server",
	"internal/platform/utilhandler/handler.go": "the util connection's in-process handler; served to the api gateway's transport, never to a listener",
}

// routesUnmountedHere are the route families the scan finds that this test's
// platform does not mount, each as a pattern over the registered pattern and
// the condition that leaves it unmounted. Every family must excuse at least
// one scanned route and match no reported one, so an entry cannot outlive the
// condition it names or hide a route that is served.
var routesUnmountedHere = []struct {
	match  *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`^/portal/auth/`), "mountBrowserAuth: no browser-session OIDC flow is configured"},
	{regexp.MustCompile(`^(GET|POST|PUT|DELETE) /api/v1/admin/api-catalogs`), "catalogapi.Register: no API catalog store (no apigateway toolkit)"},
	{regexp.MustCompile(`^(GET|POST|DELETE) /api/v1/admin/auth/keys`), "registerAuthKeyRoutes: no API key manager (auth.api_keys is off)"},
	{regexp.MustCompile(`^(GET|POST|PUT|DELETE) /api/v1/admin/webhooks/`), "mountWebhookAdminAPI: no object store for a webhook source"},
	{regexp.MustCompile(`^(GET|POST|DELETE) /api/v1/portal/api-keys`), "wirePortalUserKeys: no API key store with principals"},
	{regexp.MustCompile(`^(GET|POST|PUT|DELETE) /api/v1/portal/datahub/`), "dataHubRegistrar: no DataHub toolkit"},
	{regexp.MustCompile(`^GET /api/v1/portal/scripts/\{id\}/thumbnail$`), "flowhttp.RegisterPortal: tiles need the renderer"},
	{regexp.MustCompile(`^GET /api/v1/portal/(assets/\{id\}/content-url|content/\{token\})$`), "contentURLsReady: no content URL signing key"},
	{regexp.MustCompile(`^(GET|POST) /portal/notifications/unsubscribe$`), "mountNotificationUnsubscribe: no browser-session signing key"},
	{regexp.MustCompile(`^POST /api/v1/admin/(api-gateway|gateway)/connections/\{name\}/oauth-start$`), "connoauthapi legacy per-kind OAuth: the unified connection OAuth routes are registered instead"},
	{regexp.MustCompile(`/calls/\{id\}/(promote|reject)$`), "callapi.Register: no call promoter"},
	{regexp.MustCompile(`^(GET|POST|DELETE) /api/v1/(tables|table-connections|(portal/assets|resources)/\{id\}/tables)`), "mountTableAPI: the table registrar needs a Trino connection and an object store; the test platform has neither"},
}

// TestEveryRouteReportsItsTemplate_RealDB is the gate for #1889: every
// pattern the source registers, on the top mux and on the nested admin,
// portal, resources, gateway and proxy muxes, is sent one request through the
// assembled listener, and the request observer must record a sample under
// that exact template. A route that reports only its mount prefix (a nested
// mux the auth layer clones the request before) or the raw path fails here.
func TestEveryRouteReportsItsTemplate_RealDB(t *testing.T) {
	_, dsn := testdb.NewWithDSN(t)
	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server:   platform.ServerConfig{Name: "test", SlowRequestThreshold: time.Hour},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
		Database: platform.DatabaseConfig{DSN: dsn},
		Portal:   platform.PortalConfig{Enabled: ptr(true)},
		Resources: platform.ResourcesConfig{
			Managed: platform.ManagedResourcesCfg{Enabled: ptr(true)},
		},
		OAuth: platform.OAuthConfig{
			Enabled:    true,
			Issuer:     "https://mcp.example.com",
			SigningKey: "dGVzdC1zaWduaW5nLWtleS0xMjM0NTY3ODkwYWJjZGVm",
		},
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.NotNil(t, p.Metrics(), "the gate reads the metrics recorder; OTEL_METRICS_ENABLED must not be false here")

	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil), p, addr)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Error("Serve did not stop")
		}
	})
	base := "http://" + addr
	awaitListener(t, base+"/healthz")

	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, rt := range scannedRoutes(t) {
		method, path := splitPattern(rt)
		req, err := http.NewRequestWithContext(ctx, method, base+requestPath(path), http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		_ = res.Body.Close()
	}

	rec := httptest.NewRecorder()
	p.Metrics().Handler().ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/metrics", http.NoBody))
	body := rec.Body.String()

	reportedSet := reportedRoutes(body)
	excused := make([]int, len(routesUnmountedHere))
	var missing []string
	for _, rt := range scannedRoutes(t) {
		reported := strings.Contains(body, `route="`+rt+`"`) || mountServedBeneath(rt, reportedSet)
		family := unmountedFamily(rt)
		switch {
		case family >= 0 && reported:
			t.Errorf("routesUnmountedHere[%d] (%s) matches %q, which the listener reported; narrow or remove the entry",
				family, routesUnmountedHere[family].reason, rt)
		case family >= 0:
			excused[family]++
		case !reported:
			missing = append(missing, rt)
		}
	}
	for i, n := range excused {
		if n == 0 {
			t.Errorf("routesUnmountedHere[%d] (%s) excuses no scanned route; remove the entry", i, routesUnmountedHere[i].reason)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d route(s) answered a request without reporting their own template (#1889); a nested mux behind "+
			"an auth layer needs httpobs.Routed around it, a pattern mounted elsewhere goes in routesOutsideTheListener, "+
			"a pattern this platform does not mount goes in routesUnmountedHere with its reason:\n  %s\n\nroutes reported: %s",
			len(missing), strings.Join(missing, "\n  "), strings.Join(reportedRoutes(body), ", "))
	}
}

// scannedRoutes is the route table the source registers on the listener's
// muxes, the PDF route table included (it is registered from a slice the
// scan reads as a cross-package value), minus the files whose muxes are not
// the listener's.
func scannedRoutes(t *testing.T) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	res, err := routescan.Scan(filepath.Join(repoRoot, "pkg"), filepath.Join(repoRoot, "internal"))
	require.NoError(t, err)
	require.Empty(t, res.Unresolved, "a registration the scan cannot fold is a route this gate cannot check")
	seen := map[string]bool{}
	var out []string
	for _, rt := range res.Routes {
		rel, err := filepath.Rel(repoRoot, strings.SplitN(rt.Pos, ":", 2)[0])
		require.NoError(t, err)
		if _, outside := routesOutsideTheListener[filepath.ToSlash(rel)]; outside || seen[rt.Pattern] {
			continue
		}
		seen[rt.Pattern] = true
		out = append(out, rt.Pattern)
	}
	for _, rt := range pdfhttp.Routes {
		if !seen[rt.Pattern] {
			seen[rt.Pattern] = true
			out = append(out, rt.Pattern)
		}
	}
	require.Greater(t, len(out), 300, "the scan found too few routes to be reading the tree")
	sort.Strings(out)
	return out
}

// splitPattern is a pattern's method (GET for one registered with none) and path.
func splitPattern(pattern string) (method, path string) {
	if m, p, ok := strings.Cut(pattern, " "); ok {
		return m, p
	}
	return http.MethodGet, pattern
}

var wildcardSegment = regexp.MustCompile(`\{([^}]*)\}`)

// requestPath is a concrete path a pattern matches: each {name} becomes a
// value, {name...} a two-segment remainder and {$} nothing.
func requestPath(pattern string) string {
	return wildcardSegment.ReplaceAllStringFunc(pattern, func(seg string) string {
		switch {
		case seg == "{$}":
			return ""
		case strings.HasSuffix(seg, "...}"):
			return "a/b"
		}
		return "x1"
	})
}

// unmountedFamily is the index of the routesUnmountedHere entry matching the
// pattern, or -1.
func unmountedFamily(pattern string) int {
	for i, f := range routesUnmountedHere {
		if f.match.MatchString(pattern) {
			return i
		}
	}
	return -1
}

// mountServedBeneath reports whether pattern is a subtree mount (no method)
// with a method route reported under it. Such a mount only delegates: every
// request it receives is reported under the nested route it reached, or
// under the mount itself when the nested mux had none, and either is the
// template the request matched.
func mountServedBeneath(pattern string, reported []string) bool {
	if strings.Contains(pattern, " ") {
		return false
	}
	prefix := strings.TrimSuffix(pattern, "/") + "/"
	for _, r := range reported {
		if _, path, ok := strings.Cut(r, " "); ok && strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func reportedRoutes(body string) []string {
	re := regexp.MustCompile(`route="([^"]*)"`)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// freeAddr is a loopback address nothing listens on. Serve binds its own
// listener, so the port is chosen here and handed to it.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func awaitListener(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = res.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("listener never answered at %s: %v", url, err)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			time.Sleep(20 * time.Millisecond)
		}
	}
}
