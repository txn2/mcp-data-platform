//go:build integration

package acceptance

// Issue #1708: the local stack ran one platform process, so a defect that
// exists only between two replicas over one database had no lane that could
// see it before a release reached a deployment running two pods. `make dev`
// now runs two processes behind a round-robin proxy with no affinity, the
// proxy replaces origin 502 and 504 bodies the way the CDN in front of a
// deployment does, and every response names the process that answered it.
//
// Every criterion runs through that proxy, the address `make acceptance`
// connects to, or against each replica behind it on its own.
//
// Wire forms: this ticket adds no tool parameter. The REST gateway invoke body
// (`POST /api/v1/gateway/{connection}/invoke`) takes one shape,
// `{"method": ..., "path": ...}`, and is sent that way; the admin connection
// write takes one shape, `{"config": {...}, "description": ...}`.

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	issue1708FixtureURL = "http://localhost:9282"
	issue1708FixtureKey = "apitest-dev-key-2024"
	// issue1708Propagation bounds how long a connection written on one
	// replica may take to be served by another.
	issue1708Propagation = 30 * time.Second
)

// issue1708Response is what reached the caller: status, content type, body,
// and the process the platform says answered.
type issue1708Response struct {
	status      int
	contentType string
	body        string
	instance    string
}

// issue1708Request is one request: where it goes, as whom, and its JSON body.
// An empty apiKey sends no credential; a nil body sends none.
type issue1708Request struct {
	base, method, path, apiKey string
	body                       []byte
}

// issue1708Do issues one request and returns what reached the caller.
func issue1708Do(t *testing.T, r issue1708Request) issue1708Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), r.method, r.base+r.path, bytes.NewReader(r.body)) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("%s %s: %v", r.method, r.path, err)
	}
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("%s %s%s: %v", r.method, r.base, r.path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the body: %v", r.method, r.path, err)
	}
	return issue1708Response{
		status:      res.StatusCode,
		contentType: res.Header.Get("Content-Type"),
		body:        string(raw),
		instance:    res.Header.Get(instanceHeader),
	}
}

// issue1708Connect registers a catalog-less API connection on the replica c
// is open on and returns its name. It is removed when the test ends.
func issue1708Connect(t *testing.T, c *client, label, baseURL, callTimeout string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1708-%s-%d", label, time.Now().UnixNano())
	status, body := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, jsonBody(t, map[string]any{
		"config": map[string]any{
			"base_url": baseURL, "auth_mode": "api_key", "credential": issue1708FixtureKey,
			"api_key_placement": "header", "api_key_header": "X-API-Key", "connection_name": name,
			"connect_timeout": "2s", "call_timeout": callTimeout, "trust_level": "untrusted",
		},
		"description": "Acceptance 1708: " + label,
	}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("registering the %s connection on %s answered HTTP %d: %v", label, c.base, status, body)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})
	return name
}

// issue1708Invoke calls one operation of a connection through the REST
// gateway on base.
func issue1708Invoke(t *testing.T, base, name, path string) issue1708Response {
	t.Helper()
	return issue1708Do(t, issue1708Request{
		base: base, method: http.MethodPost, path: "/api/v1/gateway/" + name + "/invoke", apiKey: devAPIKey(),
		body: fmt.Appendf(nil, `{"method":"GET","path":%q}`, path),
	})
}

// issue1708AwaitStatus invokes the connection on one replica until it answers
// want, which is how long that replica took to serve a connection another
// replica wrote.
func issue1708AwaitStatus(t *testing.T, c *client, name, path string, want int) issue1708Response {
	t.Helper()
	deadline := time.Now().Add(issue1708Propagation)
	for {
		got := issue1708Invoke(t, c.base, name, path)
		if got.status == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still answers HTTP %d for connection %s %s %s after %s; want %d", c.base, got.status, name, path, got.body, issue1708Propagation, want)
		}
		time.Sleep(time.Second)
	}
}

// issue1708AssertCDNLine: two consecutive requests through the proxy, which
// round robin sends to both replicas, each reach the caller as the CDN's line
// for status and nothing of the origin's body.
func issue1708AssertCDNLine(t *testing.T, name, path string, status int, origin string) {
	t.Helper()
	for i := range 2 {
		got := issue1708Invoke(t, baseURL(), name, path)
		want := fmt.Sprintf("error code: %d", status)
		if got.status != status || !strings.HasPrefix(got.contentType, "text/plain") || got.body != want {
			t.Errorf("request %d through the proxy reached the caller as HTTP %d %q %q; want HTTP %d text/plain %q", i+1, got.status, got.contentType, got.body, status, want)
		}
		if strings.Contains(got.body, origin) {
			t.Errorf("request %d through the proxy carried the origin's body: %q", i+1, got.body)
		}
	}
}

// TestIssue1708_ConsecutiveRequestsThroughTheProxyLandOnDifferentReplicas is
// the lane itself: a request and the next one are answered by different
// processes, and every response names the process, whether the platform
// served it or refused it.
func TestIssue1708_ConsecutiveRequestsThroughTheProxyLandOnDifferentReplicas(t *testing.T) {
	requests := []struct {
		label, path, apiKey string
		status              int
	}{
		{"liveness", "/healthz", "", http.StatusOK},
		{"an authenticated admin route", "/api/v1/admin/connection-instances", devAPIKey(), http.StatusOK},
		{"an admin route with no credential", "/api/v1/admin/connection-instances", "", http.StatusUnauthorized},
		{"liveness again", "/healthz", "", http.StatusOK},
		{"an authenticated admin route again", "/api/v1/admin/connection-instances", devAPIKey(), http.StatusOK},
		{"an admin route with no credential again", "/api/v1/admin/connection-instances", "", http.StatusUnauthorized},
	}
	var previous string
	seen := map[string]bool{}
	for i, r := range requests {
		got := issue1708Do(t, issue1708Request{base: baseURL(), method: http.MethodGet, path: r.path, apiKey: r.apiKey})
		if got.status != r.status {
			t.Errorf("%s answered HTTP %d; want %d: %s", r.label, got.status, r.status, got.body)
		}
		if got.instance == "" {
			t.Fatalf("%s answered with no %s header", r.label, instanceHeader)
		}
		if i > 0 && got.instance == previous {
			t.Errorf("%s was answered by %s, the process that answered the request before it", r.label, got.instance)
		}
		previous = got.instance
		seen[got.instance] = true
	}
	if len(seen) != 2 {
		t.Errorf("%d requests through %s were answered by %d processes: %v; want 2", len(requests), baseURL(), len(seen), seen)
	}
}

// issue1708Recorder is a transport that keeps the instance header of every
// response it carries.
type issue1708Recorder struct {
	mu        sync.Mutex
	instances map[string]int
}

func (r *issue1708Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("round trip to %s: %w", req.URL, err)
	}
	r.mu.Lock()
	r.instances[res.Header.Get(instanceHeader)]++
	r.mu.Unlock()
	return res, nil
}

// TestIssue1708_AnMCPSessionContinuesOnBothReplicas: an MCP session opened
// through the proxy keeps working while its requests alternate between the
// processes, which only a session store both replicas read allows. Every tool
// call succeeds, and both processes answered part of the session.
func TestIssue1708_AnMCPSessionContinuesOnBothReplicas(t *testing.T) {
	rec := &issue1708Recorder{instances: map[string]int{}}
	c := connectVia(t, baseURL(), devAPIKey(), rec, sessionTimeout)
	for range 4 {
		out := c.call("list_connections", nil)
		if len(out) == 0 {
			t.Fatalf("list_connections returned an empty result through %s", baseURL())
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if n := rec.instances[""]; n > 0 {
		t.Errorf("%d responses under the session carried no %s header", n, instanceHeader)
	}
	delete(rec.instances, "")
	if len(rec.instances) != 2 {
		t.Errorf("the session's requests were answered by %v; want both replicas", rec.instances)
	}
}

// TestIssue1708_AConnectionWrittenOnOneReplicaIsServedByEveryReplica is the
// helper the ticket asks for, used the way it is meant to be: a write on each
// replica in turn is served by every replica, each reached on its own.
func TestIssue1708_AConnectionWrittenOnOneReplicaIsServedByEveryReplica(t *testing.T) {
	forEachReplica(t, func(t *testing.T, writer *client) {
		t.Helper()
		name := issue1708Connect(t, writer, "written", issue1708FixtureURL, "10s")
		forEachReplica(t, func(t *testing.T, reader *client) {
			t.Helper()
			issue1708AwaitStatus(t, reader, name, "/v1/whoami", http.StatusOK)
		})
	})
}

// TestIssue1708_AnOrigin502ReachesTheCallerAsTheCDNsLine: a gateway call to an
// upstream nothing listens on is answered by each replica with a 502 that
// explains itself, and through the proxy that 502 reaches the caller as
// `error code: 502` and nothing more, from both replicas.
func TestIssue1708_AnOrigin502ReachesTheCallerAsTheCDNsLine(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	closed := listener.Addr().String()
	_ = listener.Close() //nolint:errcheck // the port is wanted closed

	name := issue1708Connect(t, connect(t), "refused", "http://"+closed, "5s")
	forEachReplica(t, func(t *testing.T, c *client) {
		t.Helper()
		got := issue1708AwaitStatus(t, c, name, "/v1/whoami", http.StatusBadGateway)
		if !strings.Contains(got.body, "connection refused") {
			t.Errorf("%s answered the refused dial without its reason: %q", c.base, got.body)
		}
	})
	issue1708AssertCDNLine(t, name, "/v1/whoami", http.StatusBadGateway, "connection refused")
}

// TestIssue1708_AnOrigin504ReachesTheCallerAsTheCDNsLine: a gateway call to an
// upstream that accepts the connection and never answers is answered by each
// replica with a 504 that explains itself, and through the proxy that 504
// reaches the caller as `error code: 504` and nothing more, from both
// replicas.
func TestIssue1708_AnOrigin504ReachesTheCallerAsTheCDNsLine(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	var held sync.WaitGroup
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		_ = listener.Close() //nolint:errcheck // the test is over
		held.Wait()
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			held.Go(func() {
				<-done
				_ = conn.Close() //nolint:errcheck // held open until the test ends
			})
		}
	}()

	name := issue1708Connect(t, connect(t), "silent", "http://"+listener.Addr().String(), "1s")
	forEachReplica(t, func(t *testing.T, c *client) {
		t.Helper()
		got := issue1708AwaitStatus(t, c, name, "/v1/whoami", http.StatusGatewayTimeout)
		if !strings.Contains(strings.ToLower(got.body), "deadline exceeded") && !strings.Contains(strings.ToLower(got.body), "timeout") {
			t.Errorf("%s answered the silent upstream without its reason: %q", c.base, got.body)
		}
	})
	issue1708AssertCDNLine(t, name, "/v1/whoami", http.StatusGatewayTimeout, "deadline")
}
