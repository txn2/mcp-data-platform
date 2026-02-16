package trino

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

func TestProgressInjector_Before(t *testing.T) {
	t.Run("no session or token", func(t *testing.T) {
		injector := &ProgressInjector{}
		ctx, err := injector.Before(context.Background(), trinotools.NewToolContext(trinotools.ToolQuery, nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// No notifier should be set
		notifier := trinotools.GetProgressNotifier(ctx)
		if notifier != nil {
			t.Error("expected nil notifier when no session/token")
		}
	})

	t.Run("session without token", func(t *testing.T) {
		injector := &ProgressInjector{}
		// We can't construct a real ServerSession but we can test nil handling.
		// Setting a nil session (via typed nil) should not inject a notifier.
		ctx := mcpcontext.WithServerSession(context.Background(), (*mcp.ServerSession)(nil))
		ctx, err := injector.Before(ctx, trinotools.NewToolContext(trinotools.ToolQuery, nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		notifier := trinotools.GetProgressNotifier(ctx)
		if notifier != nil {
			t.Error("expected nil notifier when no token")
		}
	})

	t.Run("token without session", func(t *testing.T) {
		injector := &ProgressInjector{}
		ctx := mcpcontext.WithProgressToken(context.Background(), "tok-1")
		ctx, err := injector.Before(ctx, trinotools.NewToolContext(trinotools.ToolQuery, nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		notifier := trinotools.GetProgressNotifier(ctx)
		if notifier != nil {
			t.Error("expected nil notifier when no session")
		}
	})
}

func TestProgressInjector_After(t *testing.T) {
	injector := &ProgressInjector{}
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "test"},
		},
	}
	got, err := injector.After(context.Background(), nil, result, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != result {
		t.Error("expected result to be passed through")
	}
}

func TestMcpProgressNotifier_Notify(_ *testing.T) {
	// We can't test with a real ServerSession (requires internal wiring),
	// but we can verify the type implements the interface.
	var _ trinotools.ProgressNotifier = (*mcpProgressNotifier)(nil)
}

func TestCreateToolkit_ProgressEnabled(t *testing.T) {
	// When ProgressEnabled is true, the toolkit should include the ProgressInjector
	// middleware without panicking.
	cfg := Config{
		Host:            "localhost",
		User:            "test",
		ProgressEnabled: true,
	}
	cfg = applyDefaults("test", cfg)

	client, err := createClient(cfg)
	if err != nil {
		t.Fatalf("createClient failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	toolkit := createToolkit(client, cfg)
	if toolkit == nil {
		t.Fatal("expected non-nil toolkit")
	}
}

func TestCreateToolkit_ProgressDisabled(t *testing.T) {
	cfg := Config{
		Host:            "localhost",
		User:            "test",
		ProgressEnabled: false,
	}
	cfg = applyDefaults("test", cfg)

	client, err := createClient(cfg)
	if err != nil {
		t.Fatalf("createClient failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	toolkit := createToolkit(client, cfg)
	if toolkit == nil {
		t.Fatal("expected non-nil toolkit")
	}
}
