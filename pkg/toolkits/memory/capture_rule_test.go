package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// memory_capture, as a client lists it, tells the agent to record facts about
// data and never results, in the description and on content itself (#2128).
func TestMemoryCapture_ListedGuidanceExcludesResults(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "capture-rule", Version: "v0"}, nil)
	newTestToolkit(&mockStore{}, nil).RegisterTools(server)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = sess.Close() }()
	list, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var tool *mcp.Tool
	for _, tl := range list.Tools {
		if tl.Name == memoryCaptureToolName {
			tool = tl
		}
	}
	if tool == nil {
		t.Fatal("memory_capture is not listed")
	}
	if !strings.Contains(tool.Description, "never query results or row values") {
		t.Errorf("the description does not exclude results: %q", tool.Description)
	}
	schema, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatal(err)
	}
	if got := s.Properties["content"].Description; !strings.Contains(got, "never query results or row values") {
		t.Errorf("content does not exclude results: %q", got)
	}
}
