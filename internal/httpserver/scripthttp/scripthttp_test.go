package scripthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// reportSource is a script whose static read yields one capability and one
// connection, which is what the version detail reports to a reader.
const reportSource = `res = platform.query(connection="warehouse", sql="SELECT 1")
platform.export(name="daily", rows=res["rows"])
`

// stubStore serves the script, version, and schedule reads the routes make.
type stubStore struct {
	scripts []script.Script
	// lastFilter records what List was asked for, which is where the portal
	// surface's visibility predicate is asserted: the rows a fake returns prove
	// nothing about the predicate the handler applied.
	lastFilter script.ListFilter
	version    *script.Version
	listErr    error
	getErr     error
	versionErr error
	// The transfer half (#1404): what the store was told to do and what it
	// should answer with.
	transferErr   error
	transferredBy script.Author
	// transferAsked is the request the store was handed, which is where the
	// handler's disposition is asserted; transferMoved is the receipt it
	// answers a move with.
	transferAsked script.TransferRequest
	transferMoved script.Transferred
	// The delete half (#1575): what the store should answer with, and the ids
	// it was actually asked to remove. deleteCascade is what the removal
	// reports having taken with the script (#1593), which the real store reads
	// in the delete's own transaction.
	deleteErr     error
	deletedIDs    []string
	deleteCascade script.Removed
	// The schedule half, served by the methods in schedules_test.go.
	schedule         *script.Schedule
	scheduleErr      error
	scheduleWriteErr error
}

func (*stubStore) Create(context.Context, *script.Script, script.Author) error { return nil }
func (*stubStore) GetByName(context.Context, string, string) (*script.Script, error) {
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (s *stubStore) GetByID(_ context.Context, id string) (*script.Script, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	for i := range s.scripts {
		if s.scripts[i].ID == id {
			return &s.scripts[i], nil
		}
	}
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (*stubStore) Update(context.Context, *script.Script) error { return nil }

// Transfer records the move the way the real store does — the owner on the live
// row and a new version number — so a handler test can assert what the caller
// is told about the script afterwards.
func (s *stubStore) Transfer(_ context.Context, req script.TransferRequest, author script.Author) (script.Transferred, error) {
	if s.transferErr != nil {
		return script.Transferred{}, s.transferErr
	}
	s.transferredBy, s.transferAsked = author, req
	for i := range s.scripts {
		if s.scripts[i].ID != req.ID {
			continue
		}
		if err := s.scripts[i].Transfer(req.NewOwnerEmail); err != nil {
			return script.Transferred{}, err //nolint:wrapcheck // the fake mirrors the store: the domain refusal is the caller's message
		}
		s.scripts[i].Version++
		if req.Outputs == script.OutputsMove {
			return s.transferMoved, nil
		}
		return script.Transferred{}, nil
	}
	return script.Transferred{}, errors.New("script not found")
}

// Delete removes the script the way the real store does, and answers a request
// for one it does not hold by wrapping script.ErrNotFound rather than silently
// succeeding: that is the contract the Postgres store states through
// RowsAffected, and a fake that swallowed it would leave the handler's
// already-deleted path untested.
func (s *stubStore) Delete(_ context.Context, id string) (script.Removed, error) {
	if s.deleteErr != nil {
		return script.Removed{}, s.deleteErr
	}
	for i := range s.scripts {
		if s.scripts[i].ID != id {
			continue
		}
		s.deletedIDs = append(s.deletedIDs, id)
		s.scripts = append(s.scripts[:i], s.scripts[i+1:]...)
		return s.deleteCascade, nil
	}
	return script.Removed{}, fmt.Errorf("delete script %s: %w", id, script.ErrNotFound)
}

func (s *stubStore) List(_ context.Context, filter script.ListFilter) ([]script.Script, error) {
	s.lastFilter = filter
	return s.scripts, s.listErr
}

func (*stubStore) UpdateWithVersion(context.Context, *script.Script, script.Author) error {
	return nil
}

func (s *stubStore) ListVersions(context.Context, string) ([]script.Version, error) {
	if s.versionErr != nil {
		return nil, s.versionErr
	}
	if s.version == nil {
		return []script.Version{}, nil
	}
	return []script.Version{*s.version}, nil
}

func (s *stubStore) GetVersion(_ context.Context, _ string, version int) (*script.Version, error) {
	if s.versionErr != nil {
		return nil, s.versionErr
	}
	if s.version == nil || s.version.Version != version {
		return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
	}
	return s.version, nil
}

func (s *stubStore) GetVersionByID(_ context.Context, _ string) (*script.Version, error) {
	if s.versionErr != nil {
		return nil, s.versionErr
	}
	return s.version, nil
}

// newStore returns a store holding one active script with one applied version.
func newStore() *stubStore {
	return &stubStore{
		scripts: []script.Script{{
			ID: "script_1", Name: "daily",
			OwnerEmail: "jane@example.com", Enabled: true, Status: script.StatusActive,
			Version: 1,
		}},
		version: &script.Version{
			ID: "sver_1", ScriptID: "script_1", Version: 1, Source: reportSource,
			Author: "jane@example.com", AuthorRoles: []string{"analyst"},
			Status: script.VersionStatusApplied,
		},
	}
}

// serve mounts the routes and runs one request against them.
func serve(t *testing.T, store *stubStore, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(Deps{
		Scripts: store, Versions: store, Schedules: store,
		AdminEmail: func(*http.Request) string { return "admin@example.com" },
	}).RegisterAdmin(mux, "/api/v1/admin", func(h http.Handler) http.Handler { return h })

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, reader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// decode reads a JSON response body.
func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

func TestListScripts(t *testing.T) {
	rec := serve(t, newStore(), http.MethodGet, "/api/v1/admin/scripts", "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := decode(t, rec)
	assert.Equal(t, float64(1), body["total"])
}

func TestListScripts_StoreFailure(t *testing.T) {
	store := newStore()
	store.listErr = errors.New("boom")
	rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestListVersions(t *testing.T) {
	rec := serve(t, newStore(), http.MethodGet, "/api/v1/admin/scripts/script_1/versions", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(1), decode(t, rec)["total"])
}

func TestListVersions_MissingScriptAndStoreFailures(t *testing.T) {
	t.Run("unknown script", func(t *testing.T) {
		rec := serve(t, newStore(), http.MethodGet, "/api/v1/admin/scripts/nope/versions", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("script read fails", func(t *testing.T) {
		store := newStore()
		store.getErr = errors.New("boom")
		rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts/script_1/versions", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("version read fails", func(t *testing.T) {
		store := newStore()
		store.versionErr = errors.New("boom")
		rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts/script_1/versions", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// TestGetVersion_ShowsWhatTheCodeReachesFor is the version detail: the snapshot
// together with what a static read of its source calls, names, and writes to.
func TestGetVersion_ShowsWhatTheCodeReachesFor(t *testing.T) {
	rec := serve(t, newStore(), http.MethodGet, "/api/v1/admin/scripts/script_1/versions/1", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode(t, rec)
	version, ok := body["version"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, reportSource, version["source"])

	referenced, ok := body["referenced"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.ElementsMatch(t, []any{"platform.query", "platform.export"}, referenced["capabilities"])
	assert.Equal(t, []any{"warehouse"}, referenced["connections"])
	assert.Equal(t, false, referenced["dynamic_connections"])
	assert.Equal(t, []any{"portal"}, referenced["destinations"],
		"an export naming no destination writes to the portal")
	assert.Equal(t, false, referenced["dynamic_destinations"])
}

func TestGetVersion_NotFound(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/scripts/script_1/versions/9",
		"/api/v1/admin/scripts/script_1/versions/0",
		"/api/v1/admin/scripts/script_1/versions/abc",
	} {
		t.Run(path, func(t *testing.T) {
			rec := serve(t, newStore(), http.MethodGet, path, "")
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestGetVersion_StoreFailure(t *testing.T) {
	store := newStore()
	store.versionErr = errors.New("boom")
	rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts/script_1/versions/1", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
