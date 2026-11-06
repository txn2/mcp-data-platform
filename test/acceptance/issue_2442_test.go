//go:build integration

package acceptance

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Acceptance for #2442: a call a connection's upstream refuses writes a WARN
// record naming the connection, with to_state auth_failed; its recovery
// writes an INFO record; the scrape names the connection in
// mcp_platform_connection_state with when its state was observed; and no
// record or label carries the credential.
//
// The upstream is a server in this process that refuses the bearer token
// until it is told to accept it, which is a credential revoked and restored
// upstream; the platform reaches it over loopback through the client a call
// uses.
//
// Wire forms: the admin connection route takes a JSON object of config
// strings; api_invoke_endpoint takes connection, method, path and purpose as
// strings.

const token2442 = "acceptance-2442-credential"

func TestIssue2442_AConnectionsStateChangesAreLoggedAndNamed(t *testing.T) {
	r := replicas(t)[0]
	admin := connectAt(t, r.base, devAPIKey())
	var accept atomic.Bool
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if accept.Load() && req.Header.Get("Authorization") == "Bearer "+token2442 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})}}
	srv.Start()
	t.Cleanup(srv.Close)

	name := register2443(t, admin, srv.URL, token2442, "")
	call := func() {
		if _, _, err := admin.callRaw("api_invoke_endpoint", map[string]any{
			"connection": name, "method": "GET", "path": "/v1/me", "purpose": "Acceptance 2442: a call through the connection.",
		}); err != nil {
			t.Fatalf("api_invoke_endpoint: %v", err)
		}
	}
	call()
	refused := waitLog2442(t, name, "connection state changed")
	if refused["level"] != "WARN" || refused["to_state"] != "auth_failed" || refused["kind"] != "api" || refused["reason"] != "http_401" {
		t.Fatalf("the refused call's record: %v", refused)
	}
	// The state is the replica's that made the call, and a scrape answers
	// from a sample taken at most once per server.state_probe_interval (1m),
	// refreshed by the scrape that finds it stale: every replica is read
	// until one names the connection.
	named := false
	var body string
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		body = scrapeRaw(t)
		if metricSeries(body, "mcp_platform_connection_state", map[string]string{"connection": name, "kind": "api", "state": "auth_failed"}) >= 1 {
			named = true
			break
		}
	}
	if !named {
		t.Fatalf("no replica's scrape names the failing connection")
	}
	if metricSeries(body, "mcp_platform_connection_state_observed_timestamp_seconds", map[string]string{"connection": name, "kind": "api"}) <= 0 {
		t.Errorf("the scrape carries no observation time for the connection")
	}

	accept.Store(true)
	call()
	recovered := waitLog2442(t, name, "connection recovered")
	if recovered["level"] != "INFO" || recovered["from_state"] != "auth_failed" || recovered["to_state"] != "healthy" {
		t.Fatalf("the recovery's record: %v", recovered)
	}
	for _, rec := range []map[string]any{refused, recovered} {
		raw, _ := json.Marshal(rec)
		if strings.Contains(string(raw), token2442) || strings.Contains(string(raw), srv.URL) {
			t.Errorf("a state record carries the credential or the upstream's address: %s", raw)
		}
	}
	if strings.Contains(scrapeRaw(t), token2442) {
		t.Errorf("a label carries the credential")
	}
}

// waitLog2442 waits for the replicas' log record msg naming connection.
func waitLog2442(t *testing.T, connection, msg string) map[string]any {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		for _, path := range stderrLogFiles(t) {
			f, err := os.Open(path) // #nosec G304 -- the dev stack's own log
			if err != nil {
				continue
			}
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
			for sc.Scan() {
				line := sc.Bytes()
				if len(line) == 0 || line[0] != '{' {
					continue
				}
				var rec map[string]any
				if json.Unmarshal(line, &rec) == nil && rec["msg"] == msg && rec["connection"] == connection {
					_ = f.Close()
					return rec
				}
			}
			_ = f.Close()
		}
	}
	t.Fatalf("no %q record names %s in the replicas' logs", msg, connection)
	return nil
}
