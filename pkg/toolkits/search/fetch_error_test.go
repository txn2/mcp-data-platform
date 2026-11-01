package search

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

// failingFetcher owns every reference and fails reading it with the text a
// PostgreSQL store returns.
type failingFetcher struct{}

func (failingFetcher) Name() string           { return "failing" }
func (failingFetcher) Scope() knowledge.Scope { return knowledge.ScopeShared }
func (failingFetcher) Search(context.Context, knowledge.Query) ([]knowledge.Hit, error) {
	return nil, nil
}

func (failingFetcher) Fetch(context.Context, string, knowledge.Caller) (*knowledge.Document, bool, error) {
	return nil, true, errors.New(`getting script x: pq: invalid input syntax for type uuid: "x" (22P02) SELECT id FROM scripts`)
}

// TestFetch_ProviderErrorTextNeverReachesTheResult is #2383's second
// criterion: when a provider fails, the caller gets a fixed message and none
// of the store's error text.
func TestFetch_ProviderErrorTextNeverReachesTheResult(t *testing.T) {
	tk := New("default", knowledge.NewRouter(nil, nil, failingFetcher{}))
	res, _, err := tk.handleFetch(ctxFor(userAID, userAEmail), &mcp.CallToolRequest{}, fetchInput{Reference: "mcp:script:x"})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("a provider failure should be a tool error, got %+v", res.Content)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content %T", res.Content[0])
	}
	text := tc.Text
	if !strings.Contains(text, fetchFailedMessage) {
		t.Errorf("result text = %q, want the fixed message", text)
	}
	for _, leaked := range []string{"pq:", "uuid", "22P02", "SELECT", "scripts"} {
		if strings.Contains(text, leaked) {
			t.Errorf("result text %q carries store error text %q", text, leaked)
		}
	}
}
