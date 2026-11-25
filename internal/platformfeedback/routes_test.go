package platformfeedback

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

// serveAdmin mounts the routes behind a wrapper that marks the request as
// passed through it, and serves one request.
func serveAdmin(t *testing.T, svc *Service, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	wrapped := false
	wrap := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped = true
			next.ServeHTTP(w, r)
		})
	}
	Register(mux, wrap, svc, func(*http.Request) string { return "admin@example.com" })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
	if !wrapped {
		t.Errorf("%s %s was served without the admin wrapper", method, path)
	}
	return rec
}

func itemRow() *sqlmock.Rows {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	return sqlmock.NewRows([]string{"id", "trying_to", "blocked_by", "tool", "suggestion", "status", "report_count", "created_at", "last_reported_at"}).
		AddRow("pfb_1", "list shared assets", "no filter", "manage_asset", "add scope", StatusOpen, 3, now, now)
}

func TestRoutes_Settings(t *testing.T) {
	svc, mock, _ := wired(t, nil)
	expectSettings(mock, Settings{Enabled: true, Channels: []string{"ops"}})
	rec := serveAdmin(t, svc, http.MethodGet, settingsPath, "")
	var view SettingsView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET settings = %d %s", rec.Code, rec.Body)
	}
	if !view.Enabled || view.UpdatedBy != "admin@example.com" || view.UpdatedAt == nil || view.SessionLimit != DefaultSessionLimit {
		t.Errorf("view = %+v", view)
	}

	if rec := serveAdmin(t, svc, http.MethodPut, settingsPath, `{"enabled":true,"surprise":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown field = %d", rec.Code)
	}
	if rec := serveAdmin(t, svc, http.MethodPut, settingsPath, `{"channels":["gone"]}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "gone") {
		t.Errorf("a missing channel = %d %s", rec.Code, rec.Body)
	}
	mock.ExpectExec(qSaveSettings).WillReturnError(errDown)
	if rec := serveAdmin(t, svc, http.MethodPut, settingsPath, `{"enabled":true}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("a failed save = %d", rec.Code)
	}
	mock.ExpectExec(qSaveSettings).WithArgs(SectionName, sqlmock.AnyArg(), "admin@example.com").WillReturnResult(sqlmock.NewResult(1, 1))
	// The save reads back what is in force once, and answers with it.
	expectSettings(mock, Settings{Enabled: true, Channels: []string{"ops"}})
	if rec := serveAdmin(t, svc, http.MethodPut, settingsPath, `{"enabled":true,"channels":["ops"]}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Errorf("a save = %d %s", rec.Code, rec.Body)
	}
	met(t, mock)
}

func TestRoutes_ListAndGet(t *testing.T) {
	svc, mock, _ := wired(t, nil)
	now := time.Now()
	mock.ExpectQuery(qList).WithArgs(StatusOpen, "manage_asset", "%shared%", 50, 0).WillReturnRows(
		sqlmock.NewRows([]string{"id", "trying_to", "blocked_by", "tool", "suggestion", "status", "report_count", "created_at", "last_reported_at", "total"}).
			AddRow("pfb_1", "list shared assets", "no filter", "manage_asset", "", StatusOpen, 3, now, now, 7))
	mock.ExpectQuery(qStats).WillReturnRows(sqlmock.NewRows([]string{"o", "r", "f", "d"}).AddRow(4, 9, 2, 1))
	rec := serveAdmin(t, svc, http.MethodGet, postsPath+"?status=open&tool=manage_asset&q=shared&limit=900&offset=-3", "")
	var page ListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	if page.Total != 7 || len(page.Posts) != 1 || page.Posts[0].ReportCount != 3 || page.Stats != (Stats{Open: 4, ReportsThisWeek: 9, RefusedThisWeek: 2, DeliveryFailures: 1}) {
		t.Errorf("page = %+v", page)
	}

	mock.ExpectQuery(qList).WillReturnError(errDown)
	if rec := serveAdmin(t, svc, http.MethodGet, postsPath, ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("a failed list = %d", rec.Code)
	}
	mock.ExpectQuery(qList).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qStats).WillReturnError(errDown)
	if rec := serveAdmin(t, svc, http.MethodGet, postsPath, ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("failed counts = %d", rec.Code)
	}

	mock.ExpectQuery(qGet).WithArgs("pfb_1").WillReturnRows(itemRow())
	mock.ExpectQuery(qReports).WithArgs("pfb_1", reportsLimit).WillReturnRows(
		sqlmock.NewRows([]string{"persona", "calls", "reported_at"}).AddRow("analyst", pq.StringArray{"mcp:call:a"}, now).AddRow("admin", nil, now))
	mock.ExpectQuery(qDeliveries).WithArgs(keyPrefix + "pfb_1").WillReturnRows(
		sqlmock.NewRows([]string{"channel", "status", "attempts", "last_error", "sent_at"}).AddRow("ops", "sent", 1, "", now).AddRow("chat", "failed", 3, "timeout", nil))
	rec = serveAdmin(t, svc, http.MethodGet, postsPath+"/pfb_1", "")
	var d Detail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	if d.ID != "pfb_1" || len(d.Reports) != 2 || d.Reports[1].Calls == nil || len(d.Deliveries) != 2 || d.Deliveries[1].LastError != "timeout" {
		t.Errorf("detail = %+v", d)
	}
	if strings.Contains(rec.Body.String(), "dps_") || strings.Contains(rec.Body.String(), "user_id") {
		t.Errorf("a post's page shows a session or user id: %s", rec.Body)
	}

	mock.ExpectQuery(qGet).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if rec := serveAdmin(t, svc, http.MethodGet, postsPath+"/pfb_none", ""); rec.Code != http.StatusNotFound {
		t.Errorf("a missing post = %d", rec.Code)
	}
	mock.ExpectQuery(qGet).WillReturnError(errDown)
	if rec := serveAdmin(t, svc, http.MethodGet, postsPath+"/pfb_1", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("a failed read = %d", rec.Code)
	}
	mock.ExpectQuery(qGet).WillReturnRows(itemRow())
	mock.ExpectQuery(qReports).WillReturnError(errDown)
	if rec := serveAdmin(t, svc, http.MethodGet, postsPath+"/pfb_1", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("failed reports = %d", rec.Code)
	}
	mock.ExpectQuery(qGet).WillReturnRows(itemRow())
	mock.ExpectQuery(qReports).WillReturnRows(sqlmock.NewRows([]string{"persona", "calls", "reported_at"}))
	mock.ExpectQuery(qDeliveries).WillReturnError(errDown)
	if rec := serveAdmin(t, svc, http.MethodGet, postsPath+"/pfb_1", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("failed deliveries = %d", rec.Code)
	}
	met(t, mock)
}

func TestRoutes_Status(t *testing.T) {
	svc, mock, _ := wired(t, nil)
	path := postsPath + "/pfb_1/status"
	if rec := serveAdmin(t, svc, http.MethodPut, path, `{`); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad body = %d", rec.Code)
	}
	if rec := serveAdmin(t, svc, http.MethodPut, path, `{"status":"shipped"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown status = %d", rec.Code)
	}
	mock.ExpectExec(qSetStatus).WithArgs("pfb_1", StatusPlanned, "admin@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
	if rec := serveAdmin(t, svc, http.MethodPut, path, `{"status":"planned"}`); rec.Code != http.StatusNoContent {
		t.Errorf("a status change = %d %s", rec.Code, rec.Body)
	}
	mock.ExpectExec(qSetStatus).WillReturnResult(sqlmock.NewResult(0, 0))
	if rec := serveAdmin(t, svc, http.MethodPut, path, `{"status":"done"}`); rec.Code != http.StatusNotFound {
		t.Errorf("a missing post = %d", rec.Code)
	}
	mock.ExpectExec(qSetStatus).WillReturnError(errDown)
	if rec := serveAdmin(t, svc, http.MethodPut, path, `{"status":"declined"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("a failed write = %d", rec.Code)
	}
	met(t, mock)
}

func TestRoutes_Resend(t *testing.T) {
	svc, mock, sender := wired(t, nil)
	path := postsPath + "/pfb_1/resend"
	on := Settings{Enabled: true, Channels: []string{"ops", "broken"}}
	if rec := serveAdmin(t, svc, http.MethodPost, path, `{`); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad body = %d", rec.Code)
	}
	expectSettings(mock, on)
	if rec := serveAdmin(t, svc, http.MethodPost, path, `{"channel":"elsewhere"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "elsewhere") {
		t.Errorf("a channel the settings do not name = %d %s", rec.Code, rec.Body)
	}
	expectSettings(mock, on)
	mock.ExpectQuery(qGet).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if rec := serveAdmin(t, svc, http.MethodPost, path, `{"channel":"ops"}`); rec.Code != http.StatusNotFound {
		t.Errorf("a missing post = %d", rec.Code)
	}
	expectSettings(mock, on)
	mock.ExpectQuery(qGet).WillReturnRows(itemRow())
	if rec := serveAdmin(t, svc, http.MethodPost, path, `{"channel":"ops"}`); rec.Code != http.StatusNoContent {
		t.Errorf("a resend = %d %s", rec.Code, rec.Body)
	}
	if len(sender.sent) != 1 || !strings.HasPrefix(sender.sent[0].Document.Key, keyPrefix+"pfb_1:resend:") || !strings.Contains(sender.sent[0].Document.Body, "list shared assets") {
		t.Errorf("the resend = %+v; want a key of its own and the post's words", sender.sent)
	}
	expectSettings(mock, on)
	mock.ExpectQuery(qGet).WillReturnRows(itemRow())
	if rec := serveAdmin(t, svc, http.MethodPost, path, `{"channel":"broken"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("a channel that refused = %d", rec.Code)
	}
	met(t, mock)
}

// TestStore_ScanFailures: a row the store cannot read is an error, never a
// silently short answer.
func TestStore_ScanFailures(t *testing.T) {
	svc, mock, _ := wired(t, nil)
	ctx := context.Background()
	mock.ExpectQuery(qList).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("pfb_1"))
	if _, _, err := svc.store.list(ctx, ListFilter{}); err == nil {
		t.Error("list read a short row")
	}
	mock.ExpectQuery(qReports).WillReturnRows(sqlmock.NewRows([]string{"persona"}).AddRow("a"))
	if _, err := svc.store.reports(ctx, "pfb_1"); err == nil {
		t.Error("reports read a short row")
	}
	mock.ExpectQuery(qDeliveries).WillReturnRows(sqlmock.NewRows([]string{"channel"}).AddRow("ops"))
	if _, err := svc.store.deliveries(ctx, "k"); err == nil {
		t.Error("deliveries read a short row")
	}
	mock.ExpectQuery(qOpen).WillReturnRows(openRows().AddRow("pfb_1", "t", "bad"))
	if _, err := svc.store.open(ctx); err == nil {
		t.Error("open read a bad embedding")
	}
	met(t, mock)
}
