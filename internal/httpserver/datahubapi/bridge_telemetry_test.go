package datahubapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// The portal catalog's reads and edits are DataHub requests like any other
// (#1896): each is counted by datahub_requests_total under its operation.
func TestBuildConnection_RecordsReadsAndEdits(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	prev := outbound.Metrics()
	outbound.SetDefaultMetrics(m)
	t.Cleanup(func() { outbound.SetDefaultMetrics(prev); _ = m.Shutdown(context.Background()) })

	// A refused credential: the client does not retry it.
	dh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(dh.Close)

	reader, writer, err := BuildConnection(newTestClient(t, dh.URL), "trino", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = reader.GetGlossaryTerm(ctx, "urn:li:glossaryTerm:x")
	_ = writer.UpdateDescription(ctx, "urn:li:dataset:x", "d")
	_, _ = writer.CreateTag(ctx, "certified", "")
	_ = writer.SetDomain(ctx, "urn:li:dataset:x", "urn:li:domain:d")
	_, _ = writer.UpsertContextDocument(ctx, DocumentInput{EntityURN: "urn:li:dataset:x", Title: "T", Content: "C"})

	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	for _, op := range []string{"get_glossary_term", "update_description", "create_tag", "set_domain", "upsert_context_document"} {
		if !strings.Contains(body, `datahub_requests_total{operation="`+op+`",status="upstream_err"}`) {
			t.Errorf("no datahub_requests_total for %s\n%s", op, body)
		}
		if !strings.Contains(body, `datahub_request_duration_seconds_count{operation="`+op+`"}`) {
			t.Errorf("no duration sample for %s", op)
		}
	}
}
