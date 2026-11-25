package typecheckhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetypecheck"
)

type fakeQueue struct {
	err     error
	queued  int
	summary map[int]sitetypecheck.Summary
	latest  *sitetypecheck.Check
}

func (f *fakeQueue) Enqueue(context.Context, sitetypecheck.Request) (int64, error) {
	f.queued++
	return int64(f.queued), nil
}

func (f *fakeQueue) Summaries(context.Context, string) (map[int]sitetypecheck.Summary, error) {
	return f.summary, f.err
}

func (f *fakeQueue) Latest(context.Context, string, int) (*sitetypecheck.Check, error) {
	return f.latest, f.err
}

type fakeReleases struct{ build string }

func (f fakeReleases) ReadBuild(_ context.Context, _ sitesvc.Actor, _, _ string, n int) (*sitesvc.ReleaseBuild, error) {
	return &sitesvc.ReleaseBuild{Release: &site.Release{SiteID: "site_1", Number: n}, Source: fstest.MapFS{sitebuild.ControlFile: {Data: []byte(f.build)}}}, nil
}

func (fakeReleases) MayPublish(context.Context, sitesvc.Actor, string, string) error { return nil }

// harness is the routes over a queue; allowed false stands for a caller who
// may not see the site.
type harness struct {
	q       Queue
	allowed bool
}

// serve mounts the routes and serves one request.
func (h harness) serve(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	q, allowed := h.q, h.allowed
	mux := http.NewServeMux()
	Register(mux, Deps{
		Queue: q, Releases: fakeReleases{build: `{"entries":["a.ts"],"typecheck":{}}`},
		Site: func(w http.ResponseWriter, r *http.Request) (Caller, *http.Request, bool) {
			if !allowed {
				httpjson.WriteError(w, http.StatusNotFound, "no such site")
				return Caller{}, r, false
			}
			return Caller{SiteID: "site_1", Actor: sitesvc.Actor{Email: "pat@example.com"}}, r, true
		},
		Fail: func(w http.ResponseWriter, _ *http.Request, err error) {
			httpjson.WriteError(w, http.StatusInternalServerError, err.Error())
		},
	})
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRoutes(t *testing.T) {
	q := &fakeQueue{
		summary: map[int]sitetypecheck.Summary{3: {Status: sitetypecheck.StatusErrors, Errors: 2}},
		latest:  &sitetypecheck.Check{ID: 9, Status: sitetypecheck.StatusClean},
	}
	rec := harness{q, true}.serve(t, http.MethodGet, "/api/v1/portal/sites/docs/typechecks", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"releases":{"3":{"status":"errors","errors":2}}}`, rec.Body.String())

	rec = harness{q, true}.serve(t, http.MethodGet, "/api/v1/portal/sites/docs/releases/3/typecheck", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"clean"`)

	rec = harness{q, true}.serve(t, http.MethodPost, "/api/v1/portal/sites/docs/releases/3/typecheck", `{"environment":"stage"}`)
	assert.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.Equal(t, 1, q.queued)

	// A nil queue, typed or not: nothing listed, no check, and a re-check is
	// refused.
	var typedNil *sitetypecheck.Queue
	rec = harness{typedNil, true}.serve(t, http.MethodGet, "/api/v1/portal/sites/docs/typechecks", "")
	assert.JSONEq(t, `{"releases":{}}`, rec.Body.String())
	rec = harness{nil, true}.serve(t, http.MethodGet, "/api/v1/portal/sites/docs/typechecks", "")
	assert.JSONEq(t, `{"releases":{}}`, rec.Body.String())
	rec = harness{nil, true}.serve(t, http.MethodGet, "/api/v1/portal/sites/docs/releases/3/typecheck", "")
	assert.JSONEq(t, `{"release":3,"check":null}`, rec.Body.String())
	rec = harness{nil, true}.serve(t, http.MethodPost, "/api/v1/portal/sites/docs/releases/3/typecheck", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestRoutesRefuse(t *testing.T) {
	q := &fakeQueue{}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/portal/sites/docs/typechecks"},
		{http.MethodGet, "/api/v1/portal/sites/docs/releases/3/typecheck"},
		{http.MethodPost, "/api/v1/portal/sites/docs/releases/3/typecheck"},
	} {
		assert.Equal(t, http.StatusNotFound, harness{q, false}.serve(t, c.method, c.path, "").Code, "a caller who may not see the site: %s", c.path)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		assert.Equal(t, http.StatusBadRequest, harness{q, true}.serve(t, method, "/api/v1/portal/sites/docs/releases/x/typecheck", "").Code)
	}
	assert.Equal(t, http.StatusBadRequest, harness{q, true}.serve(t, http.MethodPost, "/api/v1/portal/sites/docs/releases/3/typecheck", "{").Code)

	failing := &fakeQueue{err: errors.New("down")}
	assert.Equal(t, http.StatusInternalServerError, harness{failing, true}.serve(t, http.MethodGet, "/api/v1/portal/sites/docs/typechecks", "").Code)
	assert.Equal(t, http.StatusInternalServerError, harness{failing, true}.serve(t, http.MethodGet, "/api/v1/portal/sites/docs/releases/3/typecheck", "").Code)
}
