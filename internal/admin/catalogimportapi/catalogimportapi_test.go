package catalogimportapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/catalogimport"
)

// fakeRunner records the import it was asked to start.
type fakeRunner struct {
	url, token, by string
	configured     string
	startErr       error
	runs           []catalogimport.Run
	runsErr        error
}

// StartConfigured starts from the configured DataHub, refusing as the runner
// does when there is none.
func (f *fakeRunner) StartConfigured(ctx context.Context, by string) (catalogimport.Run, error) {
	if f.configured == "" {
		return catalogimport.Run{}, catalogimport.ErrNoSource
	}
	return f.Start(ctx, f.configured, "configured-token", by)
}

func (f *fakeRunner) Configured() string { return f.configured }

func (f *fakeRunner) Start(_ context.Context, url, token, by string) (catalogimport.Run, error) {
	f.url, f.token, f.by = url, token, by
	if f.startErr != nil {
		return catalogimport.Run{}, f.startErr
	}
	return catalogimport.Run{ID: "r1", SourceURL: url, StartedBy: by, StartedAt: time.Now(), Running: true}, nil
}

func (f *fakeRunner) Runs(context.Context, int) ([]catalogimport.Run, error) {
	return f.runs, f.runsErr
}

func serve(t *testing.T, r *fakeRunner, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		Runner: r, Author: func(*http.Request) string { return "ana@example.com" },
	})
	req := httptest.NewRequestWithContext(context.Background(), method, importsPath, strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestStartAnImport(t *testing.T) {
	r := &fakeRunner{}
	w := serve(t, r, http.MethodPost, `{"url":" https://datahub.example.com:8080 ","token":"tok"}`)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	assert.Equal(t, "https://datahub.example.com:8080", r.url)
	assert.Equal(t, "tok", r.token)
	assert.Equal(t, "ana@example.com", r.by)
	var run catalogimport.Run
	require.NoError(t, json.NewDecoder(w.Body).Decode(&run))
	assert.Equal(t, "r1", run.ID)
	assert.True(t, run.Running)
}

func TestStartTheConfiguredImport(t *testing.T) {
	for _, body := range []string{"", "{}", `{"url":" ","token":""}`} {
		r := &fakeRunner{configured: "https://datahub.example.com:8080"}
		w := serve(t, r, http.MethodPost, body)
		require.Equal(t, http.StatusAccepted, w.Code, "body %q: %s", body, w.Body.String())
		assert.Equal(t, "https://datahub.example.com:8080", r.url)
		assert.Equal(t, "configured-token", r.token)
		assert.Equal(t, "ana@example.com", r.by)
	}
	w := serve(t, &fakeRunner{}, http.MethodPost, "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "names no DataHub")
}

func TestStartRefusals(t *testing.T) {
	cases := map[string]struct {
		body   string
		err    error
		status int
		want   string
	}{
		"not json":         {body: `nope`, status: http.StatusBadRequest, want: "not an import request"},
		"an unknown field": {body: `{"url":"http://d","token":"t","x":1}`, status: http.StatusBadRequest, want: "not an import request"},
		"no url":           {body: `{"token":"t"}`, status: http.StatusBadRequest, want: "url must be"},
		"not http":         {body: `{"url":"ftp://d","token":"t"}`, status: http.StatusBadRequest, want: "url must be"},
		"no token":         {body: `{"url":"http://d"}`, status: http.StatusBadRequest, want: "token is required"},
		"one is running":   {body: `{"url":"http://d","token":"t"}`, err: catalogimport.ErrRunning, status: http.StatusConflict, want: "already running"},
		"datahub fails":    {body: `{"url":"http://d","token":"t"}`, err: errors.New("opening DataHub at http://d: bad"), status: http.StatusBadRequest, want: "opening DataHub"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := serve(t, &fakeRunner{startErr: tc.err}, http.MethodPost, tc.body)
			assert.Equal(t, tc.status, w.Code)
			assert.Contains(t, w.Body.String(), tc.want)
		})
	}
}

func TestListImports(t *testing.T) {
	w := serve(t, &fakeRunner{runs: []catalogimport.Run{{ID: "r1"}}}, http.MethodGet, "")
	require.Equal(t, http.StatusOK, w.Code)
	var list RunList
	require.NoError(t, json.NewDecoder(w.Body).Decode(&list))
	require.Len(t, list.Imports, 1)

	w = serve(t, &fakeRunner{runs: []catalogimport.Run{}}, http.MethodGet, "")
	assert.JSONEq(t, `{"imports":[]}`, w.Body.String())

	w = serve(t, &fakeRunner{runs: []catalogimport.Run{}, configured: "https://datahub.example.com:8080"}, http.MethodGet, "")
	assert.JSONEq(t, `{"imports":[],"configured":"https://datahub.example.com:8080"}`, w.Body.String())

	w = serve(t, &fakeRunner{runsErr: errors.New("db")}, http.MethodGet, "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestNoRunnerMountsNothing(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, importsPath, http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Empty(t, (&handler{}).author(req))
}
