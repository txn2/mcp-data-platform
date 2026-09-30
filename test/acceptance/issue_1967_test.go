//go:build integration

package acceptance

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1967: a client on protocol revision 2026-07-28 (SEP-2575) opens no
// GET stream; it is told a list changed only on its subscriptions/listen
// stream, held by the replica that received the listen. These criteria hold a
// listening client on one replica of the two-replica dev stack, make each
// change through a session on the OTHER replica, and wait for the client's own
// list-changed handler to run: a prompt saved through manage_prompt, a managed
// resource created through manage_resource, and a gateway connection to the
// mcp-test fixture added and removed through the admin REST API. A client on
// 2025-11-25, told on its GET stream, is held to the same three.
//
// Wire forms: manage_prompt's command, name, display_name, description,
// content and scope, and manage_resource's action, filename, display_name,
// path, description, content and content_type are typed strings, sent as
// literal tools/call parameters. The admin connection write takes one shape,
// `{"config": {...}, "description": ...}`. tools/list takes no parameters.

// awaitNotice1967 bounds how long a notification may take to arrive: the
// prompt and resource notifiers debounce, the event crosses the replicas'
// broadcaster, and a gateway change crosses the reload bus and dials the
// upstream on each replica.
const awaitNotice1967 = 60 * time.Second

// mcpTestFixture1967 is the mcp-test fixture as the platform reaches it in the
// dev stack (dev/start.sh registers it the same way).
const mcpTestFixture1967 = "http://localhost:9281/"

// listener1967 is a client whose list-changed handlers signal each
// notification it receives.
type listener1967 struct {
	cs                        *mcp.ClientSession
	tools, prompts, resources chan struct{}
	acked                     chan struct{}
}

// listen1967 connects a client on version to the replica at base, with a
// handler for each list-changed notification.
func listen1967(t *testing.T, base, version string) *listener1967 {
	t.Helper()
	l := &listener1967{
		tools: make(chan struct{}, 64), prompts: make(chan struct{}, 64),
		resources: make(chan struct{}, 64), acked: make(chan struct{}, 4),
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance-1967", Version: "1.0.0"}, &mcp.ClientOptions{
		ToolListChangedHandler:     func(context.Context, *mcp.ToolListChangedRequest) { l.tools <- struct{}{} },
		PromptListChangedHandler:   func(context.Context, *mcp.PromptListChangedRequest) { l.prompts <- struct{}{} },
		ResourceListChangedHandler: func(context.Context, *mcp.ResourceListChangedRequest) { l.resources <- struct{}{} },
	})
	// The client does not wait for the listen to be accepted before Connect
	// returns; the acknowledgment is what says the stream is open.
	client.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "notifications/subscriptions/acknowledged" {
				l.acked <- struct{}{}
			}
			return next(ctx, method, req)
		}
	})
	httpClient := &http.Client{Transport: authRoundTripper{key: devAPIKey(), base: http.DefaultTransport}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base, HTTPClient: httpClient},
		&mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatalf("connecting on %s to %s: %v", version, base, err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if got := cs.InitializeResult().ProtocolVersion; got != version {
		t.Fatalf("the platform agreed protocol revision %s, want %s", got, version)
	}
	l.cs = cs
	if version == "2026-07-28" {
		wait1967(t, l.acked, "the listen was acknowledged")
	} else if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	return l
}

// wait1967 waits for one signal on ch.
func wait1967(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(awaitNotice1967):
		t.Fatalf("not within %s: %s", awaitNotice1967, what)
	}
}

// drain1967 empties ch, so the next wait is answered by the next change.
func drain1967(ch chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// lists1967 reports whether the listener's replica lists a tool whose name
// starts with prefix.
func (l *listener1967) lists1967(t *testing.T, prefix string) bool {
	t.Helper()
	res, err := l.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	for _, tool := range res.Tools {
		if strings.HasPrefix(tool.Name, prefix) {
			return true
		}
	}
	return false
}

// awaitListed1967 waits until the listener's replica lists (or no longer
// lists) the connection's tools: the notification says the list changed, and
// this is the list it changed to.
func (l *listener1967) awaitListed1967(t *testing.T, prefix string, want bool) {
	t.Helper()
	deadline := time.Now().Add(awaitNotice1967)
	for l.lists1967(t, prefix) != want {
		if time.Now().After(deadline) {
			t.Fatalf("after the notification, tools/list listing %s* is %v, want %v", prefix, !want, want)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// fixtureKey1967 is the key the platform presents to the mcp-test fixture.
func fixtureKey1967() string {
	if v := os.Getenv("MCPTEST_DEV_KEY"); v != "" {
		return v
	}
	return "mcptest-dev-key-2024"
}

// toldOfEachChange1967 makes each change through writer and waits for l to be
// told of it.
func toldOfEachChange1967(t *testing.T, writer *client, l *listener1967) {
	t.Helper()
	stamp := unique1579()

	drain1967(l.prompts)
	createPrompt1586(t, writer, "acceptance-1967-"+stamp, "Acceptance #1967: a prompt saved on another replica.")
	wait1967(t, l.prompts, "the client was told a prompt was saved")

	drain1967(l.resources)
	created := writer.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     "acc-1967-" + stamp + ".md",
		"display_name": "Acceptance 1967 " + stamp,
		"path":         "acceptance/issue-1967",
		"description":  "Acceptance #1967: a resource created on another replica.",
		"content":      "# 1967\n",
		"content_type": "text/markdown",
	})
	id, _ := created["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", created)
	}
	t.Cleanup(func() { _, _ = writer.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	wait1967(t, l.resources, "the client was told a resource was created")

	name := "acc-1967-" + stamp
	path := "/api/v1/admin/connection-instances/mcp/" + name
	t.Cleanup(func() { _, _ = writer.rest(http.MethodDelete, path, http.NoBody) })
	drain1967(l.tools)
	status, body := writer.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"endpoint": mcpTestFixture1967, "auth_mode": "api_key", "credential": fixtureKey1967(),
			"connection_name": name, "connect_timeout": "5s", "call_timeout": "10s",
		},
		"description": "Acceptance #1967: a gateway connection added on another replica.",
	}))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("PUT %s: HTTP %d: %v", path, status, body)
	}
	wait1967(t, l.tools, "the client was told a gateway connection's tools were added")
	l.awaitListed1967(t, name+"__", true)

	drain1967(l.tools)
	if status, body := writer.rest(http.MethodDelete, path, http.NoBody); status >= http.StatusMultipleChoices {
		t.Fatalf("DELETE %s: HTTP %d: %v", path, status, body)
	}
	wait1967(t, l.tools, "the client was told a gateway connection's tools were removed")
	l.awaitListed1967(t, name+"__", false)
}

// eachReplica1967 runs fn with a listener on each replica and a writer on the
// other.
func eachReplica1967(t *testing.T, version string) {
	t.Helper()
	found := replicas(t)
	for i, r := range found {
		t.Run(r.name, func(t *testing.T) {
			other := found[(i+1)%len(found)]
			l := listen1967(t, r.base, version)
			writer := connectAtFor(t, other.base, devAPIKey(), 10*time.Minute)
			toldOfEachChange1967(t, writer, l)
		})
	}
}

func TestIssue1967_AListeningClientIsToldOfEachChangeOnAnotherReplica(t *testing.T) {
	eachReplica1967(t, "2026-07-28")
}

func TestIssue1967_AClientWithASessionIsToldOfTheSameChanges(t *testing.T) {
	eachReplica1967(t, "2025-11-25")
}
