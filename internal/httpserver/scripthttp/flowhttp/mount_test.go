package flowhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp/flowhttp"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// stores answers the three reads the flow routes make; every other method of
// the store interfaces is left unimplemented and is not reached.
type stores struct {
	script.Store
	script.VersionStore
	script.RunStore
	source string
	run    *script.Run
}

func (*stores) GetByID(_ context.Context, id string) (*script.Script, error) {
	if id != "s1" {
		return nil, nil //nolint:nilnil // the store contract: nil, nil is not found
	}
	return &script.Script{ID: "s1", OwnerEmail: "jane@example.com", Version: 2}, nil
}

func (s *stores) GetVersion(_ context.Context, id string, n int) (*script.Version, error) {
	if n < 1 || n > 2 {
		return nil, nil //nolint:nilnil // the store contract: nil, nil is not found
	}
	return &script.Version{ScriptID: id, Version: n, Source: s.source}, nil
}

func (s *stores) GetRun(_ context.Context, id string) (*script.Run, error) {
	if s.run == nil || id != s.run.ID {
		return nil, script.ErrRunNotFound
	}
	return s.run, nil
}

var (
	owner    = &scripthttp.PortalIdentity{UserID: "u1", Email: "jane@example.com"}
	stranger = &scripthttp.PortalIdentity{UserID: "u2", Email: "bob@example.com"}
)

func serve(t *testing.T, st *stores, who *scripthttp.PortalIdentity, path string) *httptest.ResponseRecorder {
	t.Helper()
	deps := scripthttp.Deps{
		Scripts: st, Versions: st, Runs: st,
		PortalUser: func(*http.Request) *scripthttp.PortalIdentity { return who },
	}
	h := scripthttp.New(deps)
	mux := http.NewServeMux()
	pass := func(next http.Handler) http.Handler { return next }
	flowhttp.ForPortal(h, deps, nil, nil).RegisterPortal(mux, pass)
	flowhttp.ForAdmin(h, deps).RegisterAdmin(mux, "/api/v1/admin", pass)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec
}

const src = "rows = platform.query(\"SELECT 1\", connection=\"warehouse\")\nplatform.export(\"daily\", rows[\"rows\"], format=\"csv\")\n"

// The graph is read under the source's rule, everyone signed in; the script and
// version are found the way every other script route finds them.
func TestForPortal_TheGraphUnderTheSourcesRule(t *testing.T) {
	st := &stores{source: src}
	for _, who := range []*scripthttp.PortalIdentity{owner, stranger} {
		rec := serve(t, st, who, "/api/v1/portal/scripts/s1/versions/1/graph")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
	assert.Equal(t, http.StatusUnauthorized, serve(t, st, nil, "/api/v1/portal/scripts/s1/versions/1/graph").Code)
	for _, path := range []string{
		"/api/v1/portal/scripts/nope/versions/1/graph",
		"/api/v1/portal/scripts/s1/versions/9/graph",
	} {
		assert.Equal(t, http.StatusNotFound, serve(t, st, stranger, path).Code, path)
	}
	assert.Equal(t, http.StatusOK, serve(t, st, nil, "/api/v1/admin/scripts/s1/versions/2/graph").Code)
	assert.Equal(t, http.StatusOK, serve(t, st, owner, "/api/v1/portal/scripts/s1/versions/2/graph?compare=1").Code)
}

// A run is drawn under the run's rule: the owner and its requester read it, a
// stranger is told it does not exist.
func TestForPortal_ARunUnderTheRunsRule(t *testing.T) {
	st := &stores{source: src, run: &script.Run{
		ID: "dpx_1", ScriptID: "s1", Version: 1, Status: script.RunStatusSucceeded, CreatedAt: time.Now(),
		RequestedBy: "bob@example.com",
		Outputs:     []script.RunOutput{{Name: "daily", RowCount: 3, CallSite: []string{"2:16"}}},
	}}
	rec := serve(t, st, owner, "/api/v1/portal/scripts/s1/runs/dpx_1/flow")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Version int `json:"version"`
		Calls   int `json:"calls"`
		Nodes   map[string]struct {
			Rows    int  `json:"rows"`
			Reached bool `json:"reached"`
		} `json:"nodes"`
		Other []any `json:"other_calls"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Version)
	assert.Equal(t, 0, body.Calls, "no audit store, no calls")
	assert.Equal(t, 3, body.Nodes["op:2"].Rows, "the export is drawn from the run's outputs")
	assert.NotNil(t, body.Other)

	assert.Equal(t, http.StatusOK, serve(t, st, stranger, "/api/v1/portal/scripts/s1/runs/dpx_1/flow").Code,
		"whoever requested the run reads it")
	st.run.RequestedBy = ""
	assert.Equal(t, http.StatusNotFound, serve(t, st, stranger, "/api/v1/portal/scripts/s1/runs/dpx_1/flow").Code)
	assert.Equal(t, http.StatusNotFound, serve(t, st, owner, "/api/v1/portal/scripts/s1/runs/nope/flow").Code)
	assert.Equal(t, http.StatusUnauthorized, serve(t, st, nil, "/api/v1/portal/scripts/s1/runs/dpx_1/flow").Code)

	st.run.Version = 7
	assert.Equal(t, http.StatusNotFound, serve(t, st, owner, "/api/v1/portal/scripts/s1/runs/dpx_1/flow").Code,
		"a run of a version the script no longer has")
}
