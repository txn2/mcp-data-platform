package foldergrant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/grantsource"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

type auditLog struct{ events []middleware.AuditEvent }

func (a *auditLog) Log(_ context.Context, ev middleware.AuditEvent) error {
	a.events = append(a.events, ev)
	return nil
}

func routes(t *testing.T) (*http.ServeMux, sqlmock.Sqlmock, *auditLog) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	log := &auditLog{}
	mux := http.NewServeMux()
	NewAPI(Deps{
		Store: NewStore(db), Actor: func(*http.Request) string { return "admin@example.com" },
		PersonaExists:   func(n string) bool { return n == "finance" || n == "guests" },
		ExternalPersona: func(n string) bool { return n == "guests" },
		ExternalPerson:  func(_ context.Context, e string) bool { return e == "vendor@example.com" },
		FolderExists:    func(_ context.Context, p string) (bool, error) { return p != "nowhere", nil },
		Sites:           func(context.Context, string) ([]string, error) { return []string{"marketing"}, nil },
		Audit:           log,
	}).Register(mux, func(h http.Handler) http.Handler { return h })
	return mux, mock, log
}

func send(mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body)))
	return rec
}

var grantRow = []string{"id", "folder_path", "email", "persona", "created_by", "created_at"}

func TestList_NamesTheGrantsAndTheSites(t *testing.T) {
	mux, mock, _ := routes(t)
	mock.ExpectQuery("FROM portal_shares").WithArgs("reports/q3").WillReturnRows(sqlmock.NewRows(grantRow).
		AddRow("fg_1", "reports", "jane@example.com", "", "admin@example.com", time.Now()).
		AddRow("fg_2", "reports/q3", "", "finance", "admin@example.com", time.Now()))
	rec := send(mux, http.MethodGet, basePath+"?folder=reports/q3", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out GrantList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Grants, 2)
	assert.True(t, out.Grants[0].Inherited)
	assert.False(t, out.Grants[1].Inherited)
	assert.Equal(t, []string{"marketing"}, out.Sites, "a grantee's write changes what these serve")

	assert.Equal(t, http.StatusBadRequest, send(mux, http.MethodGet, basePath, "").Code, "no folder is the whole library")
	assert.Equal(t, http.StatusBadRequest, send(mux, http.MethodGet, basePath+"?folder=/abs", "").Code)
}

func TestAdd_Refusals(t *testing.T) {
	mux, _, log := routes(t)
	for body, want := range map[string]int{
		`not json`:             http.StatusBadRequest,
		`{"folder":"reports"}`: http.StatusBadRequest,
		`{"folder":"reports","email":"a@example.com","persona":"x"}`: http.StatusBadRequest,
		`{"folder":"","persona":"finance"}`:                          http.StatusBadRequest,
		`{"folder":"reports","email":"nobody"}`:                      http.StatusBadRequest,
		`{"folder":"reports","persona":"ghost"}`:                     http.StatusBadRequest,
		`{"folder":"reports","persona":"guests"}`:                    http.StatusBadRequest,
		`{"folder":"reports","email":"vendor@example.com"}`:          http.StatusBadRequest,
		`{"folder":"nowhere","persona":"finance"}`:                   http.StatusNotFound,
	} {
		assert.Equal(t, want, send(mux, http.MethodPost, basePath, body).Code, body)
	}
	require.NotEmpty(t, log.events, "a refused grant is audited too")
	assert.False(t, log.events[len(log.events)-1].Success)
}

func TestAdd_WritesAndAudits(t *testing.T) {
	mux, mock, log := routes(t)
	mock.ExpectQuery("SELECT id FROM portal_shares").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("INSERT INTO portal_shares").WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
	rec := send(mux, http.MethodPost, basePath, `{"folder":"reports","email":"Jane@Example.com"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var g Grant
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &g))
	assert.Equal(t, "jane@example.com", g.Email)
	assert.Equal(t, "admin@example.com", g.CreatedBy)
	require.Len(t, log.events, 1)
	assert.Equal(t, toolAdd, log.events[0].ToolName)
	assert.True(t, log.events[0].Success)

	mock.ExpectQuery("SELECT id FROM portal_shares").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("fg_1"))
	assert.Equal(t, http.StatusConflict, send(mux, http.MethodPost, basePath, `{"folder":"reports","email":"jane@example.com"}`).Code)
	mock.ExpectQuery("SELECT id FROM portal_shares").WillReturnError(errors.New("down"))
	assert.Equal(t, http.StatusInternalServerError, send(mux, http.MethodPost, basePath, `{"folder":"reports","persona":"finance"}`).Code)
}

func TestRemove(t *testing.T) {
	mux, mock, log := routes(t)
	mock.ExpectQuery("UPDATE portal_shares SET revoked").WithArgs("fg_1").WillReturnRows(sqlmock.NewRows(grantRow).
		AddRow("fg_1", "reports", "jane@example.com", "", "admin@example.com", time.Now()))
	rec := send(mux, http.MethodDelete, basePath+"/fg_1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "reports", log.events[0].Parameters["folder"])
	mock.ExpectQuery("UPDATE portal_shares SET revoked").WillReturnRows(sqlmock.NewRows(grantRow))
	assert.Equal(t, http.StatusNotFound, send(mux, http.MethodDelete, basePath+"/fg_9", "").Code)
	mock.ExpectQuery("UPDATE portal_shares SET revoked").WillReturnError(errors.New("down"))
	assert.Equal(t, http.StatusInternalServerError, send(mux, http.MethodDelete, basePath+"/fg_9", "").Code)
}

func TestSource(t *testing.T) {
	got, err := Source{}.WritableFolders(context.Background(), grantsource.Grantee{Email: "a@example.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{}, got)
	assert.Nil(t, NewStore(nil))
	var nilAPI *API
	nilAPI.Register(http.NewServeMux(), nil)
}

func TestList_ByPersona(t *testing.T) {
	mux, mock, _ := routes(t)
	mock.ExpectQuery("shared_with_persona = \\$1").WithArgs("finance").WillReturnRows(sqlmock.NewRows(grantRow).
		AddRow("fg_1", "reports", "", "finance", "admin@example.com", time.Now()))
	rec := send(mux, http.MethodGet, basePath+"?persona=finance", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"folder":"reports"`)
	mock.ExpectQuery("shared_with_persona = \\$1").WillReturnError(errors.New("down"))
	assert.Equal(t, http.StatusInternalServerError, send(mux, http.MethodGet, basePath+"?persona=finance", "").Code)
	mock.ExpectQuery("shared_with_persona = \\$1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
	assert.Equal(t, http.StatusInternalServerError, send(mux, http.MethodGet, basePath+"?persona=finance", "").Code)
}
