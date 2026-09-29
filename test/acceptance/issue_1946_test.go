//go:build integration

package acceptance

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1946: a session that listed tools under one build and makes its next
// request on another is sent notifications/tools/list_changed once. These
// criteria open real MCP client sessions against the running platform, record
// the session as having listed its tools under an older build (what a restart
// onto a new release leaves behind), and count what the client receives.
//
// A session is what the platform can resume, and only a client on protocol
// revision 2025-11-25 or earlier has one: it initializes, keeps its
// Mcp-Session-Id and opens the stream a session's notifications arrive on. A
// client on 2026-07-28 has no session to resume (SEP-2575), so the clients here
// speak 2025-11-25.
//
// "None" is observed against a sentinel rather than a wait: a prompt saved
// through manage_prompt sends notifications/prompts/list_changed to every
// session, after anything sent before it.
//
// Wire forms: tools/list takes no parameters. manage_prompt's command, name,
// display_name, description, content and scope are typed strings, sent as
// literal tools/call parameters.

// awaitNotice1946 bounds how long a notification may take to arrive: the
// prompt notifier debounces, and the event crosses the replicas' broadcaster.
const awaitNotice1946 = 30 * time.Second

// session1946 is a client session counting the list-changed notifications it
// receives.
type session1946 struct {
	cs      *mcp.ClientSession
	tools   atomic.Int32
	prompts chan struct{}
}

func connect1946(t *testing.T) *session1946 {
	t.Helper()
	s := &session1946{prompts: make(chan struct{}, 16)}
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance-1946", Version: "1.0.0"}, &mcp.ClientOptions{
		ToolListChangedHandler:   func(context.Context, *mcp.ToolListChangedRequest) { s.tools.Add(1) },
		PromptListChangedHandler: func(context.Context, *mcp.PromptListChangedRequest) { s.prompts <- struct{}{} },
	})
	httpClient := &http.Client{Transport: authRoundTripper{key: devAPIKey(), base: http.DefaultTransport}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: baseURL(), HTTPClient: httpClient},
		&mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if cs.ID() == "" {
		t.Fatalf("the platform gave the session no Mcp-Session-Id")
	}
	s.cs = cs
	return s
}

// sentinel1946 saves a prompt, which every session is told of, and waits until
// each session has been: anything sent to a session before it has arrived.
func sentinel1946(t *testing.T, admin *client, sessions ...*session1946) {
	t.Helper()
	name := "acceptance-1946-sentinel-" + unique1579()
	createPrompt1586(t, admin, name, "Acceptance #1946 sentinel.")
	for _, s := range sessions {
		select {
		case <-s.prompts:
		case <-time.After(awaitNotice1946):
			t.Fatalf("session %s was not told of the saved prompt within %s", s.cs.ID(), awaitNotice1946)
		}
	}
}

func (s *session1946) listTools(t *testing.T) {
	t.Helper()
	if _, err := s.cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
}

func TestIssue1946_ASessionResumedOnAnotherBuildIsToldOnce(t *testing.T) {
	admin := connect(t)
	resumed, current := connect1946(t), connect1946(t)
	resumed.listTools(t)
	current.listTools(t)
	sentinel1946(t, admin, resumed, current)

	// What a restart onto another release leaves: the session listed its tools
	// under the build before.
	issue1904Exec(t, issue1904DB(t), `UPDATE sessions SET state = state || '{"tools_build":"acceptance-older-build"}'::jsonb WHERE id = $1`, resumed.cs.ID())

	resumed.listTools(t)
	deadline := time.Now().Add(awaitNotice1946)
	for resumed.tools.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	resumed.listTools(t)
	current.listTools(t)
	sentinel1946(t, admin, resumed, current)

	if n := resumed.tools.Load(); n != 1 {
		t.Errorf("the resumed session was sent notifications/tools/list_changed %d times, want once", n)
	}
	if n := current.tools.Load(); n != 0 {
		t.Errorf("another session was sent the resumed session's notification %d times", n)
	}
}

func TestIssue1946_ASessionOnItsBuildIsToldNothing(t *testing.T) {
	admin := connect(t)
	s := connect1946(t)
	s.listTools(t)
	sentinel1946(t, admin, s)
	s.listTools(t)
	s.listTools(t)
	sentinel1946(t, admin, s)
	if n := s.tools.Load(); n != 0 {
		t.Errorf("a session whose build did not change was sent notifications/tools/list_changed %d times", n)
	}
}

func TestIssue1946_InitializeAdvertisesToolsListChanged(t *testing.T) {
	s := connect1946(t)
	caps := s.cs.InitializeResult().Capabilities
	if caps == nil || caps.Tools == nil || !caps.Tools.ListChanged {
		t.Fatalf("initialize must advertise tools.listChanged: %+v", caps)
	}
}
