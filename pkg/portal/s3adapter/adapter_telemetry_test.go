package s3adapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/objectobs"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Every operation the adapter makes is counted under the purpose it was built
// for, a put with the bytes it stored, and a caller's context purpose wins.
func TestClientAdapter_ReportsEveryOperation(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	prev := outbound.Metrics()
	outbound.SetDefaultMetrics(m)
	t.Cleanup(func() { outbound.SetDefaultMetrics(prev); _ = m.Shutdown(context.Background()) })

	ctx := context.Background()
	a := &ClientAdapter{client: &fakeS3API{getBody: []byte("abcdef")}, purpose: observability.StoragePurposeResources}
	if err := a.PutObject(ctx, "b", "k", []byte("12345"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PutObjectStream(ctx, "b", "k", strings.NewReader("123"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.GetObject(ctx, "b", "k"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.GetObjectRange(ctx, "b", "k", 0, 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.ListDirectory(ctx, "b", "p/"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteObject(objectobs.WithPurpose(ctx, observability.StoragePurposeThumbnails), "b", "k"); err != nil {
		t.Fatal(err)
	}
	failing := &ClientAdapter{client: &fakeS3API{getErr: errors.New("api error NoSuchKey: gone")}, purpose: observability.StoragePurposeResources}
	if _, _, err := failing.GetObject(ctx, "b", "k"); err == nil {
		t.Fatal("expected an error")
	}

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
	for _, want := range []string{
		`storage_operations_total{operation="put",purpose="resources",result="ok"} 2`,
		`storage_bytes_written_total{purpose="resources"} 8`,
		`storage_operations_total{operation="get",purpose="resources",result="ok"} 1`,
		`storage_operations_total{operation="get",purpose="resources",reason="not_found",result="error"} 1`,
		`storage_operations_total{operation="get_range",purpose="resources",result="ok"} 1`,
		`storage_operations_total{operation="list",purpose="resources",result="ok"} 1`,
		`storage_operations_total{operation="delete",purpose="thumbnails",result="ok"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %s\n%s", want, body)
		}
	}
}

// New reports as the portal's assets.
func TestNew_PortalAssetsPurpose(t *testing.T) {
	if got := New(nil).purpose; got != observability.StoragePurposePortalAssets {
		t.Errorf("purpose = %q", got)
	}
}
