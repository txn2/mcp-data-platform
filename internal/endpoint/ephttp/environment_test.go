package ephttp

import (
	"context"
	"net/http"
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
