package assettags

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

func reach() *portaldomain.Reach {
	return portaldomain.NewReach("u1", "ann@example.com", "analyst", portaldomain.ScopeAll)
}

func TestBuildList_ReachPrefixAndPage(t *testing.T) {
	query, args, err := BuildList(Query{Reach: reach(), Prefix: "Fin_%", Limit: 9999, Offset: -3})
	require.NoError(t, err)
	assert.Contains(t, query, "portal_shares", "the reach decides which assets count")
	assert.Contains(t, query, `LIKE $`)
	assert.Contains(t, query, "LIMIT 500 OFFSET 0", "limit and offset are clamped")
	assert.Equal(t, `fin\_\%%`, args[len(args)-1], "the prefix's wildcards match literally")

	query, args, err = BuildList(Query{})
	require.NoError(t, err)
	assert.NotContains(t, query, "portal_shares", "no reach is every live asset")
	assert.Contains(t, query, "LIMIT 50 OFFSET 0")
	assert.Empty(t, args)
}

func TestList_ReadsGroupedTags(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery("WITH e AS").WillReturnRows(sqlmock.NewRows([]string{"tag", "n", "spellings", "counts", "total"}).
		AddRow("finance", 14, pq.StringArray{"finance", "Finance"}, pq.Int64Array{12, 2}, 2).
		AddRow("q3", 3, pq.StringArray{"q3"}, pq.Int64Array{3}, 2))
	page, err := New(db).List(context.Background(), Query{Reach: reach()})
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)
	assert.Equal(t, Tag{Tag: "finance", Count: 14, Spellings: []Spelling{{"finance", 12}, {"Finance", 2}}}, page.Tags[0])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestList_EmptyAndErrors(t *testing.T) {
	page, err := New(nil).List(context.Background(), Query{})
	require.NoError(t, err)
	assert.NotNil(t, page.Tags)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	page, err = New(db).List(context.Background(), Query{Reach: portaldomain.NewReach("", "", "", portaldomain.ScopeAll)})
	require.NoError(t, err)
	assert.Empty(t, page.Tags, "an unidentified reach sees no tags")

	mock.ExpectQuery("WITH").WillReturnError(errors.New("boom"))
	_, err = New(db).List(context.Background(), Query{})
	require.Error(t, err)

	mock.ExpectQuery("WITH").WillReturnRows(sqlmock.NewRows([]string{"tag"}).AddRow("x"))
	_, err = New(db).List(context.Background(), Query{})
	require.Error(t, err)
}

func TestRetagNormalize(t *testing.T) {
	_, err := Retag{Sources: []string{"fin"}}.Normalize()
	require.Error(t, err)
	_, err = Retag{Target: strings.Repeat("x", 101), Sources: []string{"fin"}}.Normalize()
	require.Error(t, err)
	_, err = Retag{Target: "finance", Sources: []string{" ", ""}}.Normalize()
	require.Error(t, err)
	r, err := Retag{Target: " finance ", Sources: []string{" fin ", "Finance"}}.Normalize()
	require.NoError(t, err)
	assert.Equal(t, "finance", r.Target)
	assert.Equal(t, []string{"finance", "fin"}, r.matchSet(), "the target's other spellings are rewritten too, once")
}

func TestApply_RewritesAndCounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	now := time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)
	mock.ExpectExec("UPDATE portal_assets SET tags").
		WithArgs(pq.Array([]string{"finance", "fin"}), "finance", now).
		WillReturnResult(sqlmock.NewResult(0, 14))
	n, err := New(db).Apply(context.Background(), Retag{Sources: []string{"fin"}, Target: "finance"}, now)
	require.NoError(t, err)
	assert.Equal(t, int64(14), n)

	_, err = New(db).Apply(context.Background(), Retag{}, now)
	require.Error(t, err)

	mock.ExpectExec("UPDATE").WillReturnError(errors.New("boom"))
	_, err = New(db).Apply(context.Background(), Retag{Sources: []string{"fin"}, Target: "finance"}, now)
	require.Error(t, err)

	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewErrorResult(errors.New("no count")))
	_, err = New(db).Apply(context.Background(), Retag{Sources: []string{"fin"}, Target: "finance"}, now)
	require.Error(t, err)
}

func TestClose(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Finance", "finance", true},
		{"finance-reports", "Finance Reports", true},
		{"FinanceReports", "finance_reports", true},
		{"report", "reports", true},
		{"finanse", "finance", true},
		{"quarterly", "quartrely", true},
		{"q3", "q4", false},
		{"fin", "finance", false},
		{"sales", "legal", false},
		{"--", "__", false},
		{"marketing", "accounting", false},
	} {
		assert.Equal(t, c.want, Close(c.a, c.b), "%s ~ %s", c.a, c.b)
	}
}

func TestVariants(t *testing.T) {
	existing := []Tag{
		{Tag: "finance", Count: 14},
		{Tag: "Finance", Count: 1},
		{Tag: "finances", Count: 20},
		{Tag: "q3", Count: 5},
	}
	got := Variants([]string{"Finance", "q3", " "}, existing, 0)
	assert.Equal(t, []Variant{
		{Tag: "finances", Count: 20, CloseTo: "Finance"},
		{Tag: "finance", Count: 14, CloseTo: "Finance"},
	}, got, "the given spelling itself is never offered; q3 has no variant")
	assert.Len(t, Variants([]string{"Finance"}, existing, 1), 1)
	assert.Equal(t, []Variant{}, Variants(nil, existing, 1))
}

type fakeStore struct {
	page    Page
	lastQ   Query
	retag   Retag
	moved   int64
	listErr error
	err     error
}

func (f *fakeStore) List(_ context.Context, q Query) (Page, error) {
	f.lastQ = q
	return f.page, f.listErr
}

func (f *fakeStore) Apply(_ context.Context, r Retag, _ time.Time) (int64, error) {
	f.retag = r
	return f.moved, f.err
}

func TestPortalRoute_ScopesToTheCallersReach(t *testing.T) {
	store := &fakeStore{page: Page{Tags: []Tag{{Tag: "finance", Count: 2}}, Total: 1}}
	mux := http.NewServeMux()
	MountPortal(store)(mux, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/tags?prefix=fi&limit=5", http.NoBody)
	req = req.WithContext(access.ContextWithUser(req.Context(), &access.User{UserID: "u1", Email: "ann@example.com"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"tag":"finance"`)
	assert.Equal(t, "fi", store.lastQ.Prefix)
	assert.Equal(t, 5, store.lastQ.Limit)
	require.NotNil(t, store.lastQ.Reach)
	assert.Equal(t, portaldomain.ScopeAll, store.lastQ.Reach.Scope)

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/tags", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	store.listErr = errors.New("boom")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

type audits struct{ events []middleware.AuditEvent }

func (a *audits) Log(_ context.Context, ev middleware.AuditEvent) error {
	a.events = append(a.events, ev)
	return nil
}

func TestAdminRoutes(t *testing.T) {
	store := &fakeStore{moved: 14, page: Page{Tags: []Tag{}}}
	log := &audits{}
	mux := http.NewServeMux()
	wrapped := 0
	wrap := func(h http.Handler) http.Handler { wrapped++; return h }
	RegisterAdmin(mux, wrap, AdminConfig{Store: store, Audit: log, Author: func(*http.Request) string { return "admin@example.com" }})
	assert.Equal(t, 3, wrapped, "every administrator route sits behind the admin API's authentication")

	post := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, bytes.NewBufferString(body)))
		return rec
	}

	rec := post("/api/v1/admin/tags/merge", `{"from":["fin","Fin"],"into":"finance"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var res RetagResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, RetagResult{Tag: "finance", Moved: 14}, res)
	require.Len(t, log.events, 1)
	assert.Equal(t, ToolMerge, log.events[0].ToolName)
	assert.Equal(t, "admin@example.com", log.events[0].UserEmail)
	assert.Equal(t, "finance", log.events[0].Parameters["into"])

	rec = post("/api/v1/admin/tags/rename", `{"from":"fin","to":"finance"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, Retag{Sources: []string{"fin"}, Target: "finance"}, store.retag)
	assert.Equal(t, ToolRename, log.events[1].ToolName)

	assert.Equal(t, http.StatusBadRequest, post("/api/v1/admin/tags/rename", `nope`).Code)
	assert.Equal(t, http.StatusBadRequest, post("/api/v1/admin/tags/merge", `nope`).Code)
	assert.Equal(t, http.StatusBadRequest, post("/api/v1/admin/tags/rename", `{"from":"fin","to":""}`).Code)

	store.err = errors.New("boom")
	rec = post("/api/v1/admin/tags/rename", `{"from":"fin","to":"finance"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.False(t, log.events[len(log.events)-1].Success, "a failed rewrite is audited as failed")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tags?q=fi", http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "fi", store.lastQ.Prefix)
	assert.Nil(t, store.lastQ.Reach, "the administrator list spans every asset")

	store.listErr = errors.New("boom")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tags", http.NoBody))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAdminRoutes_NoAudit(t *testing.T) {
	store := &fakeStore{moved: 1}
	mux := http.NewServeMux()
	RegisterAdmin(mux, func(h http.Handler) http.Handler { return h }, AdminConfig{Store: store})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/tags/rename", strings.NewReader(`{"from":"a","to":"b"}`)))
	assert.Equal(t, http.StatusOK, rec.Code)
}
