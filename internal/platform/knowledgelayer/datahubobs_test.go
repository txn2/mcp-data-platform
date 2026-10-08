package knowledgelayer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/txn2/mcp-datahub/pkg/types"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// Every method of the apply_knowledge writer is one datahub_requests_total
// observation under its own operation, and a datahub.<operation> span under
// the caller's.
func TestObservedWriter_RecordsEveryMethod(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	require.NoError(t, err)
	prev := outbound.Metrics()
	outbound.SetDefaultMetrics(m)
	t.Cleanup(func() { outbound.SetDefaultMetrics(prev); _ = m.Shutdown(context.Background()) })
	sr := tracetest.NewSpanRecorder()
	tr := observability.NewTracerFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)), observability.TracingConfig{Enabled: true})
	ctx, root := tr.Start(context.Background(), "tool_call")

	w := observedWriter{w: &knowledgekit.NoopDataHubWriter{}}
	const urn = "urn:li:dataset:x"
	_, err = w.GetCurrentMetadata(ctx, urn)
	require.NoError(t, err)
	require.NoError(t, w.UpdateDescription(ctx, urn, "d"))
	require.NoError(t, w.UpdateColumnDescription(ctx, urn, "c", "d"))
	require.NoError(t, w.UpdateColumnDescriptionBatch(ctx, urn, map[string]string{"c": "d"}))
	require.NoError(t, w.ApplyTagChanges(ctx, urn, []string{"a"}, nil))
	require.NoError(t, w.ApplyGlossaryTermChanges(ctx, urn, []string{"a"}, nil))
	require.NoError(t, w.AddDocumentationLink(ctx, urn, "https://example.com", "d"))
	require.NoError(t, w.RemoveDocumentationLink(ctx, urn, "https://example.com"))
	_, err = w.CreateCuratedQuery(ctx, []string{urn}, "n", "SELECT 1", "d")
	require.NoError(t, err)
	require.NoError(t, w.UpsertStructuredProperties(ctx, urn, "p", []any{"v"}))
	require.NoError(t, w.RemoveStructuredProperty(ctx, urn, "p"))
	require.NoError(t, w.DeleteTag(ctx, "urn:li:tag:t"))
	require.NoError(t, w.SetCustomProperties(ctx, urn, map[string]string{"k": "v"}))
	require.NoError(t, w.RemoveCustomProperties(ctx, urn, []string{"k"}))
	_, err = w.RaiseIncident(ctx, urn, "t", "d")
	require.NoError(t, err)
	require.NoError(t, w.ResolveIncident(ctx, "urn:li:incident:i", "m"))
	_, err = w.GetIncidents(ctx, urn)
	require.NoError(t, err)
	_, err = w.UpsertContextDocument(ctx, urn, types.ContextDocumentInput{})
	require.NoError(t, err)
	require.NoError(t, w.DeleteContextDocument(ctx, "doc"))
	root.End()

	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(raw)

	ops := []string{
		"get_current_metadata", "update_description", "update_column_description", "update_column_description_batch",
		"apply_tag_changes", "apply_glossary_term_changes", "add_documentation_link", "remove_documentation_link",
		"create_curated_query", "upsert_structured_properties", "remove_structured_property", "delete_tag",
		"set_custom_properties", "remove_custom_properties", "raise_incident", "resolve_incident", "get_incidents",
		"upsert_context_document", "delete_context_document",
	}
	for _, op := range ops {
		assert.Contains(t, body, `datahub_requests_total{operation="`+op+`",status="ok"} 1`)
	}
	var children int
	for _, s := range sr.Ended() {
		if strings.HasPrefix(s.Name(), "datahub.") {
			children++
			assert.Equal(t, root.SpanContext().SpanID(), s.Parent().SpanID())
		}
	}
	assert.Equal(t, len(ops), children)
}
