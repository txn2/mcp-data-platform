package webhookwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func TestRegisterCompactionGauge_ReportsTheBacklog(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery(regexp.QuoteMeta("FROM webhook_windows")).
		WillReturnRows(sqlmock.NewRows([]string{"p", "r", "w", "o"}).AddRow(6, 0, 2, 30.0))
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()
	registerCompactionGauge(m, whstore.New(db))
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body := rec.Body.String()
	for _, want := range []string{
		`background_queue_items{loop="webhook_compactor",state="pending"} 6`,
		`background_queue_items{loop="webhook_compactor",state="waiting"} 2`,
		`background_queue_oldest_age_seconds{loop="webhook_compactor"} 30`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}

	failing, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = failing.Shutdown(context.Background()) }()
	mock.ExpectQuery(regexp.QuoteMeta("FROM webhook_windows")).WillReturnError(context.DeadlineExceeded)
	registerCompactionGauge(failing, whstore.New(db))
	rec = httptest.NewRecorder()
	failing.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	if strings.Contains(rec.Body.String(), "background_queue_items") {
		t.Error("a failed read reports no backlog")
	}
}
