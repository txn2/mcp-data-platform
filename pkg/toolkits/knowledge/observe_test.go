package knowledge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

func installMetrics(t *testing.T) func() string {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	prev := opsobs.Metrics()
	opsobs.SetMetrics(m)
	t.Cleanup(func() { opsobs.SetMetrics(prev) })
	return func() string {
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
		b, _ := io.ReadAll(rec.Body)
		return string(b)
	}
}

// TestRecordApplyOutcome counts an apply by sink and result, leaves a
// confirmation round-trip and an unknown sink uncounted.
func TestRecordApplyOutcome(t *testing.T) {
	scrape := installMetrics(t)
	ctx := context.Background()
	ok := toolkit.JSONResult(map[string]any{"applied": true})
	confirm := toolkit.JSONResult(map[string]any{"confirmation_required": true})

	recordApplyOutcome(ctx, "", ok, nil)
	recordApplyOutcome(ctx, sinkKnowledgePage, toolkit.ErrorResult("refused"), nil)
	recordApplyOutcome(ctx, sinkAgentInstructions, nil, errors.New("boom"))
	recordApplyOutcome(ctx, sinkDataHub, confirm, nil)
	recordApplyOutcome(ctx, "made_up_sink", ok, nil)
	recordReviews(ctx, StatusApproved, 2)
	recordReviews(ctx, StatusRejected, 1)

	body := scrape()
	for _, want := range []string{
		`knowledge_changes_total{result="applied",sink="datahub"} 1`,
		`knowledge_changes_total{result="failed",sink="knowledge_page"} 1`,
		`knowledge_changes_total{result="failed",sink="agent_instructions"} 1`,
		`knowledge_changes_total{result="approved",sink="insight"} 2`,
		`knowledge_changes_total{result="rejected",sink="insight"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
	if strings.Contains(body, "made_up_sink") {
		t.Error("a caller-chosen sink became a label value")
	}
}

func TestAwaitsConfirmation(t *testing.T) {
	if !awaitsConfirmation(toolkit.JSONResult(map[string]any{"confirmation_required": true})) {
		t.Error("confirmation not recognized")
	}
	if awaitsConfirmation(&mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{}, &mcp.TextContent{Text: "not json"}}}) {
		t.Error("a non-confirmation recognized")
	}
}
