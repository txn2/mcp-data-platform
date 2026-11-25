package toolschema

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Intent string `json:"intent"`
}

func TestInputSchema_ReadsTheServersListing(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "s", Version: "v"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "search"}, func(context.Context, *mcp.CallToolRequest, echoIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	l := New(server, 0)
	s := l.InputSchema(context.Background(), "search")
	props, _ := s["properties"].(map[string]any)
	if _, ok := props["intent"]; !ok {
		t.Fatalf("schema lacks intent: %v", s)
	}
	if l.InputSchema(context.Background(), "nope") != nil {
		t.Error("an unregistered tool has a schema")
	}

	// A tool registered after the listing is found by listing again on a miss.
	mcp.AddTool(server, &mcp.Tool{Name: "later"}, func(context.Context, *mcp.CallToolRequest, echoIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	if l.InputSchema(context.Background(), "later") == nil {
		t.Error("a tool registered since the last listing was not found")
	}
}

func TestInputSchema_CachesWithinTheTTL(t *testing.T) {
	calls := 0
	now := time.Unix(0, 0)
	l := &Lookup{ttl: time.Minute, now: func() time.Time { return now }}
	l.list = func(context.Context) (map[string]map[string]any, error) {
		calls++
		return map[string]map[string]any{"a": {"type": "object"}}, nil
	}
	l.InputSchema(context.Background(), "a")
	l.InputSchema(context.Background(), "a")
	if calls != 1 {
		t.Errorf("listed %d times within the TTL, want 1", calls)
	}
	now = now.Add(2 * time.Minute)
	l.InputSchema(context.Background(), "a")
	if calls != 2 {
		t.Errorf("listed %d times after the TTL, want 2", calls)
	}
	l.list = func(context.Context) (map[string]map[string]any, error) { return nil, errors.New("down") }
	if l.InputSchema(context.Background(), "b") != nil {
		t.Error("a failed listing returned a schema")
	}
	var nilLookup *Lookup
	if nilLookup.InputSchema(context.Background(), "a") != nil {
		t.Error("a nil lookup returned a schema")
	}
	if New(nil, time.Second).InputSchema(context.Background(), "a") != nil {
		t.Error("a lookup with no server returned a schema")
	}
}

func TestDecode(t *testing.T) {
	if decode(map[string]any{"a": 1}) == nil {
		t.Error("a map did not decode")
	}
	if decode(struct {
		Type string `json:"type"`
	}{"object"})["type"] != "object" {
		t.Error("a struct did not decode")
	}
	if decode(func() {}) != nil || decode("x") != nil {
		t.Error("a non-object decoded")
	}
}
