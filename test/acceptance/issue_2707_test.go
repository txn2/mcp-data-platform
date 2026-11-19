//go:build integration

package acceptance

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Issue #2707: a replica starts taking script runs before it has read its
// GraphQL connections' schemas, and a run it picks up in that window failed on
// its first platform.call to such a connection ("has no schema yet"), a
// condition that clears by itself. A call on a connection whose first read is
// still in flight now waits for it, bounded, and is served by the schema it
// installs.
//
// The criterion starts the platform twice on a database of its own, so it is
// the only worker: first with the run worker off, to save a script calling
// graphql_query and queue a run of it; then with the worker on and the
// GraphQL endpoint's schema read held open by a proxy in front of the dev
// stack's GraphQL fixture. The second start's worker claims the queued run
// while the read is held; the run must start before the read is released and
// still succeed.
//
// Wire forms: run_script's `name` is typed string and `wait_seconds` integer,
// each sent as its one form; manage_script's `command` and `run_id` are
// strings. graphql_query's arguments are the script's own.

const issue2707Config = `
server:
  name: acceptance-2707
  transport: http
  address: "%%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2707-key"
        name: "acceptance"
        email: "acceptance-2707@example.com"
        roles: ["admin"]
personas:
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
database:
  dsn: "%s"
scripts:
  worker:
    enabled: %s
notifications:
  enabled: false
thumbnails:
  enabled: false
toolkits:
  graphql:
    enabled: true
    instances:
      gql2707:
        endpoint_url: "%s"
        description: "Acceptance #2707: the GraphQL fixture behind a proxy that can hold the schema read."
`

// schemaHold2707 is a proxy to the GraphQL fixture that, while armed, holds
// each introspection request until it is released, and records when.
type schemaHold2707 struct {
	mu       sync.Mutex
	armed    bool
	held     chan struct{}
	release  chan struct{}
	released time.Time
	once     sync.Once
}

func newSchemaHold2707(t *testing.T) (*schemaHold2707, *httptest.Server) {
	t.Helper()
	h := &schemaHold2707{held: make(chan struct{}), release: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(srv.Close)
	return h, srv
}

func (h *schemaHold2707) arm() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.armed = true
}

func (h *schemaHold2707) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	hold := h.armed && bytes.Contains(body, []byte("__schema"))
	h.mu.Unlock()
	if hold {
		h.once.Do(func() { close(h.held) })
		<-h.release
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, issue1277Endpoint(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after copy
	for k, v := range res.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}

// releaseAfter frees the held read d after it arrived, and returns when.
func (h *schemaHold2707) releaseAfter(t *testing.T, d time.Duration) <-chan time.Time {
	t.Helper()
	done := make(chan time.Time, 1)
	go func() {
		select {
		case <-h.held:
		case <-time.After(3 * time.Minute):
		}
		time.Sleep(d)
		h.mu.Lock()
		h.released = time.Now()
		h.mu.Unlock()
		close(h.release)
		done <- h.released
	}()
	return done
}

func TestIssue2707_ARunOnAJustStartedReplicaWaitsForTheGraphQLSchema(t *testing.T) {
	requireGraphQLUpstream(t)
	db := fmt.Sprintf("acceptance_2707_%d", time.Now().UnixNano())
	devPSQL(t, "mcp_platform", "CREATE DATABASE "+db)
	t.Cleanup(func() { devPSQL(t, "mcp_platform", "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)") })
	dsn := "postgres://platform:platform_secret@localhost:" + devPostgresPort() + "/" + db + "?sslmode=disable"
	hold, proxy := newSchemaHold2707(t)

	// First start, worker off: save the script and queue its run.
	first := startPlatform(t, fmt.Sprintf(issue2707Config, dsn, "false", proxy.URL+"/graphql"))
	first.serving(t, 3*time.Minute)
	c := connectAt(t, first.base, "acceptance-2707-key")
	c.call("platform_info", nil)
	c.saveScript(map[string]any{"command": "create", "name": "acc-2707-health", "source": "def main():\n" +
		"    \"\"\"Reads the fixture's version through the GraphQL connection.\"\"\"\n" +
		"    out = platform.call(\"graphql_query\", {\"connection\": \"gql2707\", " +
		"\"query\": \"query Health { appConfig { appVersion } }\"})\n" +
		"    return {\"status\": out[\"status\"]}\n"}, nil)
	queued := c.call("run_script", map[string]any{"name": "acc-2707-health", "wait_seconds": -1})
	runID, _ := queued["run_id"].(string)
	if runID == "" {
		t.Fatalf("run_script queued no run: %v", queued)
	}
	first.stop(t)

	// Between the starts, nothing of the schema is stored, so the second
	// start reads it from the endpoint, and the proxy holds that read.
	devPSQL(t, db, "DELETE FROM graphql_connection_schemas")
	hold.arm()
	released := hold.releaseAfter(t, 8*time.Second)

	second := startPlatform(t, fmt.Sprintf(issue2707Config, dsn, "true", proxy.URL+"/graphql"))
	second.serving(t, 3*time.Minute)
	freed := <-released
	c = connectAt(t, second.base, "acceptance-2707-key")
	c.call("platform_info", nil)
	run := awaitRun1843(t, c, runID)
	if run["status"] != "succeeded" {
		t.Fatalf("the run failed on the replica still reading its schema: %v", run)
	}
	started := when1843(t, run, "started_at")
	if !started.Before(freed) {
		t.Fatalf("the run started at %s, after the schema read was released at %s: the criterion did not exercise the window",
			started.Format(time.RFC3339Nano), freed.Format(time.RFC3339Nano))
	}
	if out := fmt.Sprint(run); strings.Contains(out, "has no schema yet") {
		t.Fatalf("the run reports the schema not being read: %s", out)
	}
}
