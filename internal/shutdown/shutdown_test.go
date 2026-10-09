package shutdown

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/inprocess"
)

func sessionCount(s *mcp.Server) int {
	n := 0
	for range s.Sessions() {
		n++
	}
	return n
}

// mcpServerWithLiveSession returns an MCP server with one connected client
// session and a cleanup that closes both ends.
func mcpServerWithLiveSession(t *testing.T) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1.0"}, nil)
	ctx := context.Background()
	c1, c2 := mcp.NewInMemoryTransports()
	serverSess, err := srv.Connect(ctx, c1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSess.Close() })
	clientSess, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1.0"}, nil).Connect(ctx, c2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = clientSess.Close() })
	return srv
}

func waitFor(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(msg)
	}
}

// An in-process session is left open by the close; an agent's is closed
// (#2058).
func TestCloseSessions_LeavesInProcessSessions(t *testing.T) {
	srv := mcpServerWithLiveSession(t)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(inprocess.Track(ss))
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "run", Version: "1.0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	CloseSessions(context.Background(), srv)
	deadline := time.After(2 * time.Second)
	for sessionCount(srv) != 1 {
		select {
		case <-deadline:
			t.Fatalf("%d sessions remain, want the in-process one", sessionCount(srv))
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := cs.Ping(context.Background(), nil); err != nil {
		t.Fatalf("the in-process session was closed: %v", err)
	}
}

func TestDrains(t *testing.T) {
	var d Drains
	d.Add(nil)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("no drains is no work, got %v", err)
	}
	release := make(chan struct{})
	var mu sync.Mutex
	ran := 0
	d.Add(func(context.Context) error {
		<-release // waits for the other drain: they run concurrently or never return
		mu.Lock()
		ran++
		mu.Unlock()
		return errors.New("first")
	})
	d.Add(func(context.Context) error {
		close(release)
		mu.Lock()
		ran++
		mu.Unlock()
		return nil
	})
	if err := d.Run(context.Background()); err == nil || err.Error() != "shutdown drains: first" {
		t.Fatalf("Run = %v, want the first drain's error", err)
	}
	if ran != 2 {
		t.Fatalf("%d drains ran, want 2", ran)
	}
}

func TestCloseMCPSessions(t *testing.T) {
	CloseSessions(context.Background(), nil) // nil server is a no-op

	srv := mcpServerWithLiveSession(t)
	if got := sessionCount(srv); got != 1 {
		t.Fatalf("expected 1 live session before close, got %d", got)
	}

	CloseSessions(context.Background(), srv)

	deadline := time.After(2 * time.Second)
	for sessionCount(srv) != 0 {
		select {
		case <-deadline:
			t.Fatalf("session not closed: %d remain", sessionCount(srv))
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestCloseMCPSessions_BoundedByContext proves the close does not hang past the
// grace deadline when a session has a long-running in-flight tool call (Close is
// graceful and blocks on that call). With a short context it must return promptly.
func TestCloseMCPSessions_BoundedByContext(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1.0"}, nil)
	callStarted := make(chan struct{})
	releaseCall := make(chan struct{})
	var once sync.Once
	mcp.AddTool(srv, &mcp.Tool{Name: "slow"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		once.Do(func() { close(callStarted) })
		<-releaseCall
		return &mcp.CallToolResult{}, nil, nil
	})

	ctx := context.Background()
	c1, c2 := mcp.NewInMemoryTransports()
	serverSess, err := srv.Connect(ctx, c1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = serverSess.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1.0"}, nil)
	clientSess, err := client.Connect(ctx, c2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = clientSess.Close() }()

	// Fire a tool call that hangs, so the session has an in-flight request.
	go func() { _, _ = clientSess.CallTool(ctx, &mcp.CallToolParams{Name: "slow"}) }()
	<-callStarted
	defer close(releaseCall)

	// CloseSessions must respect the short deadline rather than block on the call.
	deadlineCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	returned := make(chan struct{})
	go func() { CloseSessions(deadlineCtx, srv); close(returned) }()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("CloseSessions hung past the context deadline on an in-flight call")
	}
}

// TestInBackground covers the nil drain (no platform) and a drain whose
// error is logged rather than returned: the shutdown goes on either way.
func TestInBackground(t *testing.T) {
	waitFor(t, InBackground(nil, time.Second), "a nil drain is closed at once")
	var budget time.Duration
	done := InBackground(func(ctx context.Context) error {
		dl, _ := ctx.Deadline()
		budget = time.Until(dl)
		return context.DeadlineExceeded
	}, time.Minute)
	waitFor(t, done, "the drain did not return")
	if budget <= 0 || budget > time.Minute {
		t.Fatalf("the drain ran under a budget of %s, want under a minute", budget)
	}
}
