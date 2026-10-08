package resourcewrite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/internal/unarchive"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

func TestRefusalReason(t *testing.T) {
	for want, err := range map[string]error{
		unarchive.LimitRatio: markRefusal(&unarchive.LimitError{Limit: unarchive.LimitRatio}),
		"encrypted":          markRefusal(unarchive.ErrEncrypted),
		"unsafe_name":        fmt.Errorf("x: %w", markRefusal(unarchive.ErrUnsafeName)),
		"no_members":         markRefusal(unarchive.ErrNoMembers),
		"other":              errors.New("anything"),
	} {
		if got := refusalReason(err); got != want {
			t.Errorf("refusalReason(%v) = %q, want %q", err, got, want)
		}
	}
}

// TestRecordExtraction counts the members and bytes an extraction wrote and
// the refusal class when the archive was refused (#1898).
func TestRecordExtraction(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	prev := opsobs.Metrics()
	opsobs.SetMetrics(m)
	t.Cleanup(func() { opsobs.SetMetrics(prev) })

	ctx := context.Background()
	recordExtraction(ctx, &Extracted{Members: []ExtractedMember{
		{Landing: toolkit.ResourceLanding{SizeBytes: 100}}, {Landing: toolkit.ResourceLanding{SizeBytes: 23}},
	}}, nil)
	recordExtraction(ctx, nil, refusal(&resource.Resource{URI: "mcp://r/a.zip"}, unarchive.ErrCorrupt))
	recordExtraction(ctx, nil, errors.New("storage down"))

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		"archive_members_extracted_total 2",
		"archive_extracted_bytes_total 123",
		`archive_refusals_total{reason="corrupt"} 1`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("scrape missing %q", want)
		}
	}
	if strings.Contains(string(body), `reason="other"`) {
		t.Error("a storage failure was counted as an archive refusal")
	}
}
