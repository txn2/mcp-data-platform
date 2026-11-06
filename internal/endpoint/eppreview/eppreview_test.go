package eppreview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const form = `
def form_spec(req):
    return {"fields": [{"name": "email", "type": "email", "required": True}], "confirmation": "Thanks."}

def form_submit(req, values):
    if values["email"] == "taken@example.com":
        return {"errors": {"email": "taken"}}
    return None
`

var (
	clock = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	key   = []byte("0123456789abcdef0123456789abcdef")
	jane  = Opener{UserID: "u-jane", Email: "jane@example.com", Roles: []string{"analyst"}, AuthType: "oidc"}
)

func mockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := NewStore(db)
	s.now = func() time.Time { return clock }
	return s, mock
}

var recordColumns = []string{"id", "script_id", "version", "source", "created_by", "user_id", "roles", "auth_type", "created_at", "expires_at"}

func recordRow(version int, source string) *sqlmock.Rows {
	return sqlmock.NewRows(recordColumns).AddRow("fp1", "s1", version, source, "jane@example.com", "u-jane",
		[]byte(`["analyst"]`), "oidc", clock, clock.Add(TTL))
}

type versions struct{ src map[int]string }

var errNone = errors.New("none")

func (v versions) GetVersion(_ context.Context, _ string, n int) (*script.Version, error) {
	s, ok := v.src[n]
	if !ok {
		return nil, errNone
	}
	return &script.Version{Version: n, Source: s}, nil
}

func (versions) GetByID(_ context.Context, id string) (*script.Script, error) {
	if id != "s1" {
		return nil, errNone
	}
	return intake(), nil
}

func intake() *script.Script {
	return &script.Script{ID: "s1", Name: "intake", OwnerEmail: "jane@example.com", Version: 2}
}

// TestOpen is who opens a preview of what: the owner or an administrator,
// a saved version (the latest by default) or the unsaved source, of a form
// only; the row names the opener and the identity the drafts run as.
func TestOpen(t *testing.T) {
	s, mock := mockStore(t)
	p := &Previews{Store: s, Versions: versions{src: map[int]string{1: form, 2: form, 3: "def main():\n    pass\n"}}, BaseURL: "https://data.example.com"}
	ctx := context.Background()

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM form_previews WHERE expires_at < $1")).WithArgs(clock).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO form_previews")).
		WithArgs(sqlmock.AnyArg(), "s1", 2, "", "jane@example.com", "u-jane", []byte(`["analyst"]`), "oidc", clock.Add(TTL)).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(clock))
	opened, err := p.Open(ctx, jane, intake(), 0, "")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(opened.URL, "https://data.example.com/forms/_preview/fp"))
	assert.Equal(t, clock.Add(TTL), opened.ExpiresAt)

	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("INSERT").WithArgs(sqlmock.AnyArg(), "s1", 0, form, "admin@example.com", "u-admin", []byte(`[]`), "", clock.Add(TTL)).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(clock))
	_, err = p.Open(ctx, Opener{UserID: "u-admin", Email: "admin@example.com", Admin: true}, intake(), 0, form)
	require.NoError(t, err, "an administrator previews anyone's unsaved source")

	_, err = p.Open(ctx, Opener{UserID: "u-bob", Email: "bob@example.com"}, intake(), 0, "")
	require.ErrorIs(t, err, ErrNotAllowed)
	_, err = p.Open(ctx, Opener{}, intake(), 0, "")
	require.ErrorIs(t, err, ErrNotAllowed)
	var refused *RefusedError
	_, err = p.Open(ctx, jane, intake(), 9, "")
	require.ErrorAs(t, err, &refused)
	_, err = p.Open(ctx, jane, intake(), 3, "")
	require.ErrorAs(t, err, &refused, "a version that is not a form")

	mock.ExpectExec("DELETE").WillReturnError(errors.New("down"))
	_, err = p.Open(ctx, jane, intake(), 1, "")
	require.ErrorContains(t, err, "removing expired previews")
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("INSERT").WillReturnError(errors.New("down"))
	_, err = p.Open(ctx, jane, intake(), 1, "")
	require.ErrorContains(t, err, "storing the preview")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreGetAndOwner(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectQuery(regexp.QuoteMeta("WHERE id = $1 AND expires_at >= $2")).WithArgs("fp1", clock).WillReturnRows(recordRow(2, ""))
	owner, err := s.Owner(ctx, "fp1")
	require.NoError(t, err)
	assert.Equal(t, "jane@example.com", owner)

	mock.ExpectQuery("SELECT").WillReturnError(sql.ErrNoRows)
	_, err = s.Owner(ctx, "gone")
	require.ErrorIs(t, err, ErrNotFound, "an expired preview reads as none")
	mock.ExpectQuery("SELECT").WillReturnError(errors.New("down"))
	_, err = s.Get(ctx, "fp1")
	require.ErrorContains(t, err, "reading the preview")
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(recordColumns).AddRow("fp1", "s1", 2, "", "j", "u", []byte(`{`), "", clock, clock))
	_, err = s.Get(ctx, "fp1")
	require.ErrorContains(t, err, "decoding")
	require.NoError(t, mock.ExpectationsWereMet())
}

// drafts runs a draft's handler in a bare Starlark thread, recording the
// identity it was asked to run as.
type drafts struct {
	asked []scriptdraft.Identity
	err   error
}

func (d *drafts) Run(_ context.Context, req scriptdraft.Request) (*scriptdraft.Outcome, error) {
	d.asked = append(d.asked, req.Identity)
	if d.err != nil {
		return nil, d.err
	}
	v, err := callHandler(req)
	if err != nil {
		// A draft's failure is its outcome, not the run's error.
		return &scriptdraft.Outcome{Err: err}, nil //nolint:nilerr // the script's failure is the outcome
	}
	if err := req.Handler.Returned(v); err != nil {
		return nil, fmt.Errorf("handing back what the handler returned: %w", err)
	}
	return &scriptdraft.Outcome{}, nil
}

// callHandler runs the request's handler from its source in a bare thread.
func callHandler(req scriptdraft.Request) (starlark.Value, error) {
	thread := &starlark.Thread{}
	globals, err := starlark.ExecFileOptions(scriptdialect.Options, thread, "form", req.Source, nil)
	if err != nil {
		return nil, fmt.Errorf("loading the source: %w", err)
	}
	v, err := starlark.Call(thread, globals[req.Handler.Name], req.Handler.Args, nil)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", req.Handler.Name, err)
	}
	return v, nil
}

// TestHandler is #2494's preview API: the person who opened it is answered
// by the form's own rules, as a draft under the identity they opened it with;
// anyone else, and a token for another preview, is refused.
func TestHandler(t *testing.T) {
	s, mock := mockStore(t)
	d := &drafts{}
	runner := &eprun.Runner{FormKey: epform.Key(key), Now: func() time.Time { return clock }}
	viewerKey := epform.ViewerKey(runner.FormKey)
	h := &Handler{Store: s, Scripts: versions{src: map[int]string{2: form}}, Runner: runner, Drafts: d, ViewerKey: viewerKey}
	mux := http.NewServeMux()
	h.Mount(mux)

	viewer := func(email, id string) string {
		tok, err := epform.SignViewer(viewerKey, eprun.PreviewPrefix+id, epform.Viewer{UserID: "u", Email: email}, time.Now())
		require.NoError(t, err)
		return tok
	}
	do := func(method, id, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), method, APIPrefix+id, strings.NewReader(body))
		r.Header.Set(epform.ViewerHeader, token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	mock.ExpectQuery("SELECT").WillReturnRows(recordRow(2, ""))
	w := do("GET", "fp1", viewer("jane@example.com", "fp1"), "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var spec struct {
		Token string `json:"form_token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &spec))
	require.Len(t, d.asked, 1)
	assert.Equal(t, scriptdraft.Identity{UserID: "u-jane", Email: "jane@example.com", Roles: []string{"analyst"}, AuthType: "oidc"}, d.asked[0],
		"the draft runs as the identity the preview was opened with")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	runner.Now = func() time.Time { return clock.Add(time.Minute) }
	mock.ExpectQuery("SELECT").WillReturnRows(recordRow(2, ""))
	w = do("POST", "fp1", viewer("jane@example.com", "fp1"), `{"email":"taken@example.com","form_token":"`+spec.Token+`"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())

	// The unsaved source a preview was opened with is what runs.
	mock.ExpectQuery("SELECT").WillReturnRows(recordRow(0, form))
	w = do("POST", "fp1", viewer("jane@example.com", "fp1"), `{"email":"ann@example.com","form_token":"`+spec.Token+`"}`)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "Thanks.")

	for name, tc := range map[string]struct {
		token  string
		rows   *sqlmock.Rows
		err    error
		status int
	}{
		"someone else":      {viewer("bob@example.com", "fp1"), recordRow(2, ""), nil, http.StatusForbidden},
		"another preview's": {viewer("jane@example.com", "fp9"), recordRow(2, ""), nil, http.StatusForbidden},
		"no token":          {"", recordRow(2, ""), nil, http.StatusForbidden},
		"expired":           {viewer("jane@example.com", "fp1"), nil, sql.ErrNoRows, http.StatusNotFound},
		"store down":        {viewer("jane@example.com", "fp1"), nil, errors.New("down"), http.StatusNotFound},
		"version gone":      {viewer("jane@example.com", "fp1"), recordRow(7, ""), nil, http.StatusNotFound},
	} {
		q := mock.ExpectQuery("SELECT")
		if tc.err != nil {
			q.WillReturnError(tc.err)
		} else {
			q.WillReturnRows(tc.rows)
		}
		assert.Equal(t, tc.status, do("GET", "fp1", tc.token, "").Code, name)
	}

	d.err = scriptdraft.ErrBusy
	mock.ExpectQuery("SELECT").WillReturnRows(recordRow(2, ""))
	w = do("GET", "fp1", viewer("jane@example.com", "fp1"), "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "form_spec failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHandlerScriptGone(t *testing.T) {
	s, mock := mockStore(t)
	h := &Handler{Store: s, Scripts: versions{}, Runner: &eprun.Runner{}, ViewerKey: epform.ViewerKey(epform.Key(key))}
	mux := http.NewServeMux()
	h.Mount(mux)
	tok, err := epform.SignViewer(h.ViewerKey, eprun.PreviewPrefix+"fp1", epform.Viewer{UserID: "u", Email: "jane@example.com"}, time.Now())
	require.NoError(t, err)
	rows := sqlmock.NewRows(recordColumns).AddRow("fp1", "gone", 2, "", "jane@example.com", "u", []byte(`[]`), "", clock, clock.Add(TTL))
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	r := httptest.NewRequestWithContext(context.Background(), "GET", APIPrefix+"fp1", http.NoBody)
	r.Header.Set(epform.ViewerHeader, tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestPortal is the script page's route: signed in, the script's owner or an
// administrator, a form; anything else refused with the reason.
func TestPortal(t *testing.T) {
	s, mock := mockStore(t)
	var who *Opener
	p := &Portal{
		Previews: &Previews{Store: s, Versions: versions{src: map[int]string{2: form, 3: "def main():\n    pass\n"}}},
		Scripts:  versions{}, Opener: func(*http.Request) *Opener { return who },
	}
	mux := http.NewServeMux()
	p.Register(mux, func(h http.Handler) http.Handler { return h })
	do := func(id, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/scripts/"+id+"/form-preview", strings.NewReader(body)))
		return w
	}
	assert.Equal(t, http.StatusUnauthorized, do("s1", `{}`).Code)
	who = &jane
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("INSERT").WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(clock))
	w := do("s1", `{"version":2}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"url":"/forms/_preview/fp`)

	assert.Equal(t, http.StatusBadRequest, do("s1", `{`).Code)
	assert.Equal(t, http.StatusNotFound, do("nope", `{}`).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, do("s1", `{"version":3}`).Code)
	who = &Opener{UserID: "u-bob", Email: "bob@example.com"}
	assert.Equal(t, http.StatusForbidden, do("s1", `{}`).Code)
	who = &jane
	mock.ExpectExec("DELETE").WillReturnError(errors.New("down"))
	assert.Equal(t, http.StatusInternalServerError, do("s1", `{}`).Code)
	require.NoError(t, mock.ExpectationsWereMet())

	assert.NotPanics(t, func() { (*Portal)(nil).Register(mux, nil) })
}
