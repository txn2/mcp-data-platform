package notifyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakeInbox records which recipient each call was scoped to.
type fakeInbox struct {
	unread    int
	among     map[int64]bool
	marked    int64
	err       error
	recipient string
	ids       []int64
}

func (f *fakeInbox) CountUnread(_ context.Context, r string) (int, error) {
	f.recipient = r
	return f.unread, f.err
}

func (f *fakeInbox) UnreadAmong(_ context.Context, r string, ids []int64) (map[int64]bool, error) {
	f.recipient, f.ids = r, ids
	return f.among, f.err
}

func (f *fakeInbox) MarkRead(_ context.Context, r string, ids []int64) (int64, error) {
	f.recipient, f.ids = r, ids
	return f.marked, f.err
}

// inboxCall is one request to the inbox routes.
type inboxCall struct{ caller, method, target, body string }

func serveInbox(inbox notification.InboxStore, store notification.HistoryStore, c inboxCall) *httptest.ResponseRecorder {
	caller, method, target, body := c.caller, c.method, c.target, c.body
	api := &HistoryAPI{Store: store, Inbox: inbox, UserEmail: func(*http.Request) string { return caller }}
	mux := http.NewServeMux()
	api.Register(mux)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	mux.ServeHTTP(w, req)
	return w
}

func TestUnreadCount_IsTheCallersOwn(t *testing.T) {
	inbox := &fakeInbox{unread: 4}
	w := serveInbox(inbox, &fakeHistoryStore{}, inboxCall{"Bob <BOB@example.com>", http.MethodGet, "/api/v1/portal/notifications/unread-count", ""})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got UnreadCountResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Unread != 4 || inbox.recipient != "bob@example.com" {
		t.Errorf("got %+v for %q", got, inbox.recipient)
	}
	if w := serveInbox(inbox, &fakeHistoryStore{}, inboxCall{"", http.MethodGet, "/api/v1/portal/notifications/unread-count", ""}); w.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated = %d", w.Code)
	}
	inbox.err = errors.New("boom")
	if w := serveInbox(inbox, &fakeHistoryStore{}, inboxCall{"b@x.io", http.MethodGet, "/api/v1/portal/notifications/unread-count", ""}); w.Code != http.StatusInternalServerError {
		t.Errorf("store error = %d", w.Code)
	}
}

func TestMarkRead(t *testing.T) {
	inbox := &fakeInbox{marked: 2}
	w := serveInbox(inbox, &fakeHistoryStore{}, inboxCall{"b@x.io", http.MethodPost, "/api/v1/portal/notifications/read", `{"ids":[1,2]}`})
	if w.Code != http.StatusOK || inbox.recipient != "b@x.io" || len(inbox.ids) != 2 {
		t.Fatalf("status %d recipient %q ids %v", w.Code, inbox.recipient, inbox.ids)
	}
	var got MarkReadResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Marked != 2 {
		t.Errorf("marked = %d", got.Marked)
	}
	// No body marks every row read.
	w = serveInbox(inbox, &fakeHistoryStore{}, inboxCall{"b@x.io", http.MethodPost, "/api/v1/portal/notifications/read", ""})
	if w.Code != http.StatusOK || inbox.ids != nil {
		t.Errorf("mark all: %d %v", w.Code, inbox.ids)
	}
	for name, body := range map[string]string{"bad json": "{", "too many": `{"ids":[` + strings.Repeat("1,", maxMarkRead) + `1]}`} {
		if w := serveInbox(inbox, &fakeHistoryStore{}, inboxCall{"b@x.io", http.MethodPost, "/api/v1/portal/notifications/read", body}); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, w.Code)
		}
	}
	if w := serveInbox(inbox, &fakeHistoryStore{}, inboxCall{"", http.MethodPost, "/api/v1/portal/notifications/read", ""}); w.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated = %d", w.Code)
	}
	inbox.err = errors.New("boom")
	if w := serveInbox(inbox, &fakeHistoryStore{}, inboxCall{"b@x.io", http.MethodPost, "/api/v1/portal/notifications/read", ""}); w.Code != http.StatusInternalServerError {
		t.Errorf("store error = %d", w.Code)
	}
}

func TestHistory_MarksUnreadRowsAndFilters(t *testing.T) {
	store := &fakeHistoryStore{rows: []notification.Notification{sentRow()}, total: 1}
	inbox := &fakeInbox{among: map[int64]bool{3: true}}
	w := serveInbox(inbox, store, inboxCall{"bob@example.com", http.MethodGet, "/api/v1/portal/notifications?unread=true", ""})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !store.lastList.Unread {
		t.Error("unread=true filters the listing")
	}
	var got HistoryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Data) != 1 || !got.Data[0].Unread {
		t.Errorf("rows = %+v", got.Data)
	}
	inbox.err = errors.New("boom")
	w = serveInbox(inbox, store, inboxCall{"bob@example.com", http.MethodGet, "/api/v1/portal/notifications", ""})
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || got.Data[0].Unread {
		t.Errorf("a failed read-state lookup still lists, as read: %d %+v", w.Code, got.Data)
	}
}

func TestInboxRoutesNeedAnInbox(t *testing.T) {
	w := serveInbox(nil, &fakeHistoryStore{}, inboxCall{"b@x.io", http.MethodGet, "/api/v1/portal/notifications/unread-count", ""})
	if w.Code != http.StatusNotFound {
		t.Errorf("with no inbox the route is not mounted: %d", w.Code)
	}
}
