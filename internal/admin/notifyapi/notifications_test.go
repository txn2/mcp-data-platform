package notifyapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakeHistory is the canned-result double for HistoryStore. It records the
// filter it was handed so tests can assert on query-parameter extraction.
type fakeHistory struct {
	rows      []notification.Notification
	listErr   error
	total     int
	countErr  error
	counts    map[string]int
	countsErr error
	// The list and count filters are recorded separately: one request calls
	// both, and they differ by design (the count drops paging).
	lastList   notification.HistoryFilter
	lastCount  notification.HistoryFilter
	lastCounts notification.HistoryFilter
}

func (f *fakeHistory) List(_ context.Context, filter notification.HistoryFilter) ([]notification.Notification, error) {
	f.lastList = filter
	return f.rows, f.listErr
}

func (f *fakeHistory) Count(_ context.Context, filter notification.HistoryFilter) (int, error) {
	f.lastCount = filter
	return f.total, f.countErr
}

func (f *fakeHistory) CountsByStatus(_ context.Context, filter notification.HistoryFilter) (map[string]int, error) {
	f.lastCounts = filter
	return f.counts, f.countsErr
}

var _ HistoryStore = (*fakeHistory)(nil)

// serve runs one request against the registered routes.
func serve(cfg Config, target string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	Register(mux, cfg)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody))
	return w
}

func failedRow() notification.Notification {
	sent := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	return notification.Notification{
		ID:        7,
		Recipient: "bob@example.com",
		Category:  notification.CategoryShare,
		Status:    notification.StatusFailed,
		Attempts:  5,
		LastError: "dial tcp 10.0.0.1:587: connection refused",
		Payload: notification.Payload{
			Kind: notification.KindAsset, ItemTitle: "Q3 Revenue",
			Actor: "alice@example.com", Link: "https://x.io/portal/assets/a1",
		},
		CreatedAt: sent,
	}
}

func TestListNotificationsReturnsFailureDetail(t *testing.T) {
	store := &fakeHistory{rows: []notification.Notification{failedRow()}, total: 1}
	w := serve(Config{History: store}, "/api/v1/admin/notifications")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp notificationListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("rows = %d, want 1", len(resp.Data))
	}
	row := resp.Data[0]
	// An admin diagnosing a bounced delivery needs all three of these.
	if row.LastError != "dial tcp 10.0.0.1:587: connection refused" {
		t.Errorf("last_error = %q", row.LastError)
	}
	if row.Attempts != 5 {
		t.Errorf("attempts = %d, want 5", row.Attempts)
	}
	if row.Recipient != "bob@example.com" {
		t.Errorf("recipient = %q", row.Recipient)
	}
	// The subject is the line the recipient's email carried, so a reported
	// message can be matched to its row.
	if row.Subject == "" || row.ItemTitle != "Q3 Revenue" {
		t.Errorf("subject = %q, item_title = %q", row.Subject, row.ItemTitle)
	}
	if resp.Total != 1 || resp.Page != 1 || resp.PerPage != notification.DefaultHistoryLimit {
		t.Errorf("paging = %+v", resp)
	}
}

func TestListNotificationsAppliesFilters(t *testing.T) {
	store := &fakeHistory{}
	serve(Config{History: store},
		"/api/v1/admin/notifications?recipient=Bob+%3Cbob%40example.com%3E&status=failed&category=share&per_page=10&page=3")

	got := store.lastList
	// The recipient is normalized so a pasted display-name form finds the
	// rows, which are keyed by the bare address.
	if got.Recipient != "bob@example.com" {
		t.Errorf("recipient = %q, want the bare address", got.Recipient)
	}
	if got.Status != notification.StatusFailed || got.Category != notification.CategoryShare {
		t.Errorf("filter = %+v", got)
	}
	if got.Limit != 10 || got.Offset != 20 {
		t.Errorf("paging = limit %d offset %d, want 10/20", got.Limit, got.Offset)
	}
}

func TestListNotificationsCountIgnoresPaging(t *testing.T) {
	store := &fakeHistory{total: 96}
	w := serve(Config{History: store}, "/api/v1/admin/notifications?per_page=10&page=3")

	if store.lastCount.Limit != 0 || store.lastCount.Offset != 0 {
		t.Errorf("count filter must not page: %+v", store.lastCount)
	}
	if store.lastList.Limit != 10 || store.lastList.Offset != 20 {
		t.Errorf("list filter must page: %+v", store.lastList)
	}
	var resp notificationListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 96 || resp.Page != 3 || resp.PerPage != 10 {
		t.Errorf("paging = %+v", resp)
	}
}

func TestListNotificationsEmptyPageIsAnArray(t *testing.T) {
	w := serve(Config{History: &fakeHistory{}}, "/api/v1/admin/notifications")

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["data"]) != "[]" {
		t.Errorf("data = %s, want an empty array rather than null", raw["data"])
	}
}

func TestListNotificationsStoreErrors(t *testing.T) {
	for name, store := range map[string]*fakeHistory{
		"list fails":  {listErr: context.DeadlineExceeded},
		"count fails": {countErr: context.DeadlineExceeded},
	} {
		t.Run(name, func(t *testing.T) {
			w := serve(Config{History: store}, "/api/v1/admin/notifications")
			if w.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", w.Code)
			}
		})
	}
}

func TestNotificationStats(t *testing.T) {
	store := &fakeHistory{counts: map[string]int{
		notification.StatusSent:    842,
		notification.StatusFailed:  7,
		notification.StatusPending: 3,
	}}
	w := serve(Config{History: store, Retention: 30 * 24 * time.Hour},
		"/api/v1/admin/notifications/stats")

	var resp notificationStatsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Sent != 842 || resp.Failed != 7 || resp.Pending != 3 || resp.Sending != 0 {
		t.Errorf("counts = %+v", resp)
	}
	if resp.Total != 852 {
		t.Errorf("total = %d, want 852", resp.Total)
	}
	// The window is stated so the tab does not read as a complete archive.
	if resp.RetentionDays != 30 {
		t.Errorf("retention_days = %d, want 30", resp.RetentionDays)
	}
}

func TestNotificationStatsIgnoresStatusFilter(t *testing.T) {
	store := &fakeHistory{counts: map[string]int{}}
	serve(Config{History: store},
		"/api/v1/admin/notifications/stats?status=failed&category=share&recipient=bob@example.com")

	got := store.lastCounts
	// A page narrowed to failures must still show how many rows sent.
	if got.Status != "" {
		t.Errorf("status filter must not narrow the breakdown: %q", got.Status)
	}
	if got.Category != notification.CategoryShare || got.Recipient != "bob@example.com" {
		t.Errorf("the other filters must still apply: %+v", got)
	}
}

func TestNotificationStatsStoreError(t *testing.T) {
	w := serve(Config{History: &fakeHistory{countsErr: context.DeadlineExceeded}},
		"/api/v1/admin/notifications/stats")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// TestRegisterWithoutStoreMountsNothing is the no-database contract: with no
// queue to read, the routes stay off rather than serving an empty list that
// reads as "nothing failed".
func TestRegisterWithoutStoreMountsNothing(t *testing.T) {
	w := serve(Config{}, "/api/v1/admin/notifications")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
