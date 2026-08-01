package notifyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakeHistoryStore records the filter it was asked for and returns canned
// rows, so the tests can assert what the handler asked the store for -- which
// is where the self-scoping lives.
type fakeHistoryStore struct {
	rows      []notification.Notification
	listErr   error
	total     int
	countErr  error
	lastList  notification.HistoryFilter
	lastCount notification.HistoryFilter
}

func (f *fakeHistoryStore) List(_ context.Context, filter notification.HistoryFilter) ([]notification.Notification, error) {
	f.lastList = filter
	return f.rows, f.listErr
}

func (f *fakeHistoryStore) Count(_ context.Context, filter notification.HistoryFilter) (int, error) {
	f.lastCount = filter
	return f.total, f.countErr
}

// CountsByStatus is unused by this endpoint; it exists to satisfy the store
// contract and returns an empty breakdown rather than a nil map.
func (*fakeHistoryStore) CountsByStatus(context.Context, notification.HistoryFilter) (map[string]int, error) {
	return map[string]int{}, nil
}

var _ notification.HistoryStore = (*fakeHistoryStore)(nil)

// serveHistory runs one request against the history endpoint as the given
// caller. An empty caller is an unauthenticated request.
func serveHistory(store notification.HistoryStore, caller, target string) *httptest.ResponseRecorder {
	api := &HistoryAPI{
		Store:     store,
		UserEmail: func(*http.Request) string { return caller },
		Retention: 30 * 24 * time.Hour,
	}
	mux := http.NewServeMux()
	api.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody))
	return w
}

func sentRow() notification.Notification {
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	return notification.Notification{
		ID:        3,
		Recipient: "bob@example.com",
		Category:  notification.CategoryShare,
		Status:    notification.StatusSent,
		Attempts:  1,
		LastError: "dial tcp 10.0.0.1:587: connection refused",
		Payload: notification.Payload{
			Kind: notification.KindAsset, ItemTitle: "Q3 Revenue",
			Actor: "alice@example.com", Link: "https://x.io/portal/assets/a1",
		},
		SentAt: &at, CreatedAt: at,
	}
}

// TestHistoryIsScopedToTheCaller is the authorization property of this
// endpoint: the recipient the store is queried for is the authenticated
// caller, whatever the request says.
func TestHistoryIsScopedToTheCaller(t *testing.T) {
	for name, target := range map[string]string{
		"no recipient asked for":  "/api/v1/portal/notifications",
		"recipient param ignored": "/api/v1/portal/notifications?recipient=alice@example.com",
		"empty recipient param":   "/api/v1/portal/notifications?recipient=",
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeHistoryStore{}
			serveHistory(store, "bob@example.com", target)
			if store.lastList.Recipient != "bob@example.com" {
				t.Errorf("queried recipient = %q, want the caller", store.lastList.Recipient)
			}
			if store.lastCount.Recipient != "bob@example.com" {
				t.Errorf("counted recipient = %q, want the caller", store.lastCount.Recipient)
			}
		})
	}
}

// TestHistoryNormalizesTheCallerIdentity keeps the lookup keyed the way the
// queue stores rows: an identity carrying a display name still finds them.
func TestHistoryNormalizesTheCallerIdentity(t *testing.T) {
	store := &fakeHistoryStore{}
	serveHistory(store, "Bob Jones <Bob@Example.COM>", "/api/v1/portal/notifications")
	if store.lastList.Recipient != "bob@example.com" {
		t.Errorf("queried recipient = %q, want the normalized address", store.lastList.Recipient)
	}
}

func TestHistoryRequiresAuthentication(t *testing.T) {
	store := &fakeHistoryStore{}
	w := serveHistory(store, "", "/api/v1/portal/notifications")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if store.lastList.Recipient != "" {
		t.Error("an unauthenticated request must not reach the store")
	}
}

func TestHistoryOmitsDeliveryErrorDetail(t *testing.T) {
	store := &fakeHistoryStore{rows: []notification.Notification{sentRow()}, total: 1}
	w := serveHistory(store, "bob@example.com", "/api/v1/portal/notifications")

	// The recipient can act on none of the platform's mail-infrastructure
	// failures, and the error text names internal hosts.
	if body := w.Body.String(); strings.Contains(body, "10.0.0.1") || strings.Contains(body, "last_error") {
		t.Errorf("recipient view must not carry delivery error detail: %s", body)
	}
}

func TestHistoryReturnsTheRecipientView(t *testing.T) {
	store := &fakeHistoryStore{rows: []notification.Notification{sentRow()}, total: 1}
	w := serveHistory(store, "bob@example.com", "/api/v1/portal/notifications")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp HistoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("rows = %d, want 1", len(resp.Data))
	}
	item := resp.Data[0]
	if item.Subject == "" {
		t.Error("subject must summarize the event")
	}
	if item.Category != notification.CategoryShare || item.Status != notification.StatusSent {
		t.Errorf("item = %+v", item)
	}
	if item.SentAt == nil {
		t.Error("a sent row must carry its send time")
	}
	// The screen states the window it covers so it does not read as a
	// complete record.
	if resp.RetentionDays != 30 {
		t.Errorf("retention_days = %d, want 30", resp.RetentionDays)
	}
	if resp.Total != 1 || resp.Page != 1 || resp.PerPage != notification.DefaultHistoryLimit {
		t.Errorf("paging = %+v", resp)
	}
}

func TestHistoryAppliesStatusAndCategoryAndPaging(t *testing.T) {
	store := &fakeHistoryStore{}
	serveHistory(store, "bob@example.com",
		"/api/v1/portal/notifications?status=failed&category=mention&per_page=5&page=4")

	got := store.lastList
	if got.Status != notification.StatusFailed || got.Category != notification.CategoryMention {
		t.Errorf("filter = %+v", got)
	}
	if got.Limit != 5 || got.Offset != 15 {
		t.Errorf("paging = limit %d offset %d, want 5/15", got.Limit, got.Offset)
	}
	if store.lastCount.Limit != 0 || store.lastCount.Offset != 0 {
		t.Errorf("count filter must not page: %+v", store.lastCount)
	}
}

func TestHistoryEmptyPageIsAnArray(t *testing.T) {
	w := serveHistory(&fakeHistoryStore{}, "bob@example.com", "/api/v1/portal/notifications")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["data"]) != "[]" {
		t.Errorf("data = %s, want an empty array rather than null", raw["data"])
	}
}

func TestHistoryStoreErrors(t *testing.T) {
	for name, store := range map[string]*fakeHistoryStore{
		"list fails":  {listErr: errors.New("boom")},
		"count fails": {countErr: errors.New("boom")},
	} {
		t.Run(name, func(t *testing.T) {
			w := serveHistory(store, "bob@example.com", "/api/v1/portal/notifications")
			if w.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", w.Code)
			}
		})
	}
}
