package ephttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
)

// envRunner answers /environments/{env}/endpoints/{name} for the
// environments it knows (#2696).
type envRunner struct {
	*runner
	envs  map[string]bool
	asked []string
}

func (e *envRunner) ResolveIn(ctx context.Context, name, env string) (*eprun.Target, error) {
	e.asked = append(e.asked, env+"/"+name)
	if !e.envs[env] {
		return nil, eprun.ErrNoEnvironment
	}
	return e.Resolve(ctx, name)
}

// An application endpoint answers in another environment under
// /environments/{env}/endpoints/{name}, and an environment it lacks answers
// as no endpoint does (#2696).
func TestEnvironmentRoute(t *testing.T) {
	f := newFixture()
	er := &envRunner{runner: f.runner, envs: map[string]bool{"dev": true}}
	h := &Handler{Runner: er, Checker: f.checker}
	mux := http.NewServeMux()
	h.Mount(mux)
	f.mux = mux

	rec := f.do("GET", "/environments/dev/endpoints/open/42", nil, "")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]any{"id": "42"}, fields(t, rec)["params"])
	assert.Equal(t, []string{"dev/open"}, er.asked)

	rec = f.do("GET", "/environments/qa/endpoints/open/42", nil, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = f.do("GET", "/endpoints/open/42", nil, "")
	assert.Equal(t, http.StatusOK, rec.Code, "the last environment is the plain address")

	// A runner that knows no environments answers none.
	f2 := newFixture()
	assert.Equal(t, http.StatusNotFound, f2.do("GET", "/environments/dev/endpoints/open/42", nil, "").Code)
}

// A request on a site environment's host resolves the endpoint in the
// application environment its route names, and the application's own page
// origin, naming none, in the last (#2709).
func TestServePage_ResolvesInTheRoutesEnvironment(t *testing.T) {
	f := newFixture()
	er := &envRunner{runner: f.runner, envs: map[string]bool{"dev": true}}
	h := &Handler{Runner: er, Checker: f.checker}
	serve := func(env string) int {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://shop-web--dev.pages.example.com/endpoints/open", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServePage(rec, req, PageRoute{Application: "app_shop", Environment: env, Name: "open"})
		return rec.Code
	}
	serve("dev")
	assert.Equal(t, []string{"dev/open"}, er.asked)
	assert.Equal(t, http.StatusNotFound, serve("qa"), "an environment the application lacks answers nothing")
	er.asked = nil
	serve("")
	assert.Empty(t, er.asked, "the page origin names no environment and resolves the last")
}
