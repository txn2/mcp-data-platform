package httpserver

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// TestListenAndServe_AScriptRunOutlivesTheSessionClose is #2058 through the
// shutdown's real sequence. A managed-script run calls tools over the
// in-process session scriptsession opens; the drain closes the agents' live
// sessions after the settle so they reconnect to the new build, and must leave
// the run's session open. The run is given the HTTP drain's budget, from the
// signal on, and the server does not return until it has finished: two runs
// failed with `calling "tools/call": EOF` seconds after a SIGTERM when the
// close took their session with the agents'.
func TestListenAndServe_AScriptRunOutlivesTheSessionClose(t *testing.T) {
	orig := sessionDrainSettle
	sessionDrainSettle = 20 * time.Millisecond
	defer func() { sessionDrainSettle = orig }()

	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1.0"}, nil)
	var calls atomic.Int32
	mcp.AddTool(srv, &mcp.Tool{Name: "step"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})

	// An agent's session: closed by the drain.
	ctx := context.Background()
	at, act := mcp.NewInMemoryTransports()
	agent, err := srv.Connect(ctx, at, nil)
	if err != nil {
		t.Fatalf("agent connect: %v", err)
	}
	t.Cleanup(func() { _ = agent.Close() })
	agentClient, err := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1.0"}, nil).Connect(ctx, act, nil)
	if err != nil {
		t.Fatalf("agent client connect: %v", err)
	}
	t.Cleanup(func() { _ = agentClient.Close() })

	// The run's session, opened the way the run worker opens it.
	run, closeRun, err := scriptsession.Connect(ctx, srv, "script-run")
	if err != nil {
		t.Fatalf("script session: %v", err)
	}
	t.Cleanup(closeRun)
	if _, err := run.CallTool(ctx, "step", nil); err != nil {
		t.Fatalf("the run's first call: %v", err)
	}

	// An HTTP request in flight keeps Shutdown from finishing inside the
	// settle, which is what sends the drain on to close sessions.
	httpEntered, httpRelease := make(chan struct{}), make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(httpEntered) })
		<-httpRelease
		w.WriteHeader(http.StatusOK)
	})

	drainStarted, runRelease := make(chan struct{}), make(chan struct{})
	var runErr error
	var runFinished atomic.Bool
	hcfg := httpConfig{
		shutdownCfg: platform.ShutdownConfig{GracePeriod: 5 * time.Second, PreShutdownDelay: time.Millisecond},
		mcpServer:   srv,
		drainBackground: func(dctx context.Context) error {
			close(drainStarted)
			select {
			case <-runRelease:
			case <-dctx.Done():
				return dctx.Err()
			}
			_, runErr = run.CallTool(dctx, "step", nil)
			runFinished.Store(true)
			return nil
		},
	}

	ln := listenLocal(t)
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("closing the probe listener: %v", err)
	}
	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- listenAndServe(sctx, addr, handler, hcfg, nil) }()
	go func() {
		for {
			select {
			case <-httpEntered:
				return
			default:
			}
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr, http.NoBody)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	waitFor(t, httpEntered, "the HTTP request never reached the handler")

	cancel()
	waitFor(t, drainStarted, "the background drain did not start at the signal")
	deadline := time.After(3 * time.Second)
	for sessionCount(srv) != 1 {
		select {
		case <-deadline:
			t.Fatalf("the drain did not close the agent's session: %d sessions remain", sessionCount(srv))
		case <-time.After(10 * time.Millisecond):
		}
	}

	close(runRelease)
	close(httpRelease)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("listenAndServe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listenAndServe did not return once the drain finished")
	}
	if !runFinished.Load() {
		t.Fatal("listenAndServe returned before the run finished")
	}
	if runErr != nil {
		t.Fatalf("the run's call after the session close failed: %v", runErr)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("the tool was called %d times, want 2", got)
	}
}

func waitFor(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(msg)
	}
}
