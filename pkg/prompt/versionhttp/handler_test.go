package versionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// --- fakes ---

type fakeStore struct {
	prompts map[string]*prompt.Prompt // by id
	getErr  error
	listErr error
}

func (*fakeStore) Create(context.Context, *prompt.Prompt) error { return nil }
func (*fakeStore) Get(context.Context, string) (*prompt.Prompt, error) {
	return nil, nil //nolint:nilnil // interface contract
}

func (*fakeStore) GetPersonal(context.Context, string, string) (*prompt.Prompt, error) {
	return nil, nil //nolint:nilnil // interface contract
}

func (s *fakeStore) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.prompts[id], nil //nolint:nilnil // interface contract
}
func (*fakeStore) Update(context.Context, *prompt.Prompt) error { return nil }
func (*fakeStore) Delete(context.Context, string) error         { return nil }
func (*fakeStore) DeleteByID(context.Context, string) error     { return nil }
func (s *fakeStore) List(_ context.Context, f prompt.ListFilter) ([]prompt.Prompt, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	var out []prompt.Prompt
	for _, p := range s.prompts {
		if f.Scope != "" && p.Scope != f.Scope {
			continue
		}
		if f.OwnerEmail != "" && p.OwnerEmail != f.OwnerEmail {
			continue
		}
		if f.ExcludeSource != "" && p.Source == f.ExcludeSource {
			continue
		}
		if len(f.Personas) > 0 {
			match := false
			for _, per := range f.Personas {
				if slices.Contains(p.Personas, per) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, *p)
	}
	return out, nil
}
func (*fakeStore) Count(context.Context, prompt.ListFilter) (int, error) { return 0, nil }

type fakeVersions struct {
	versions   map[string][]prompt.Version // by prompt id
	listErr    error
	getErr     error
	approveErr error
	rejectErr  error
	approved   []int
	rejected   []int
}

func (*fakeVersions) UpdateWithVersion(context.Context, *prompt.Prompt, string) error { return nil }
func (*fakeVersions) CreateDraftVersion(context.Context, string, *prompt.Prompt, string) (int, error) {
	return 0, nil
}

func (v *fakeVersions) ListVersions(_ context.Context, promptID string) ([]prompt.Version, error) {
	if v.listErr != nil {
		return nil, v.listErr
	}
	return v.versions[promptID], nil
}

func (v *fakeVersions) GetVersion(_ context.Context, promptID string, n int) (*prompt.Version, error) {
	if v.getErr != nil {
		return nil, v.getErr
	}
	for _, ver := range v.versions[promptID] {
		if ver.Version == n {
			return &ver, nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract
}

func (v *fakeVersions) ApproveVersion(_ context.Context, promptID string, n int, approver string) (*prompt.Prompt, error) {
	if v.approveErr != nil {
		return nil, v.approveErr
	}
	v.approved = append(v.approved, n)
	now := time.Now().UTC()
	return &prompt.Prompt{
		ID: promptID, Name: "report", Scope: prompt.ScopeGlobal, Enabled: true,
		Version: n, Status: prompt.StatusApproved, ApprovedBy: approver, ApprovedAt: &now,
	}, nil
}

func (v *fakeVersions) RejectVersion(_ context.Context, _ string, n int) error {
	if v.rejectErr != nil {
		return v.rejectErr
	}
	v.rejected = append(v.rejected, n)
	return nil
}

type fakeRegistrar struct {
	registered   []string
	unregistered []string
}

func (r *fakeRegistrar) RegisterRuntimePrompt(p *prompt.Prompt) {
	r.registered = append(r.registered, p.Name)
}

func (r *fakeRegistrar) UnregisterRuntimePrompt(name string) {
	r.unregistered = append(r.unregistered, name)
}

type fakeUsage struct {
	usage  map[string]prompt.Usage
	gotIDs []string
	err    error
}

func (u *fakeUsage) PromptUsage(_ context.Context, ids []string) (map[string]prompt.Usage, error) {
	u.gotIDs = ids
	return u.usage, u.err
}

// passthrough is the identity middleware used in place of the surface auth.
func passthrough(h http.Handler) http.Handler { return h }

// newTestMux builds a handler over the fakes with both surfaces registered.
func newTestMux(t *testing.T, deps Deps) *http.ServeMux {
	t.Helper()
	if deps.AdminEmail == nil {
		deps.AdminEmail = func(*http.Request) string { return "admin@example.com" }
	}
	mux := http.NewServeMux()
	h := New(deps)
	h.RegisterAdmin(mux, "/api/v1/admin", passthrough)
	h.RegisterPortal(mux, passthrough)
	return mux
}

// fixture bundles the seeded fakes with the deps built over them.
type fixture struct {
	deps     Deps
	store    *fakeStore
	versions *fakeVersions
	reg      *fakeRegistrar
}

func seededDeps() fixture {
	store := &fakeStore{prompts: map[string]*prompt.Prompt{
		"p1": {
			ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Enabled: true,
			Status: prompt.StatusApproved, Version: 1,
		},
		"p2": {
			ID: "p2", Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
			Enabled: true, Status: prompt.StatusDraft, Version: 1,
		},
	}}
	versions := &fakeVersions{versions: map[string][]prompt.Version{
		"p1": {
			{ID: "v2", PromptID: "p1", Version: 2, Content: "draft", Status: prompt.VersionStatusDraft, Author: "jane@example.com"},
			{ID: "v1", PromptID: "p1", Version: 1, Content: "live", Status: prompt.VersionStatusApplied, Author: "jane@example.com"},
		},
	}}
	reg := &fakeRegistrar{}
	return fixture{
		deps:     Deps{Store: store, Versions: versions, Registrar: reg},
		store:    store,
		versions: versions,
		reg:      reg,
	}
}

func doReq(t *testing.T, mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody))
	return rec
}

func TestAdminListVersions(t *testing.T) {
	deps := seededDeps().deps
	mux := newTestMux(t, deps)

	rec := doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/p1/versions")
	require.Equal(t, http.StatusOK, rec.Code)
	var out versionListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, 2, out.Total)
	assert.Equal(t, 2, out.Data[0].Version, "newest first")
	assert.Equal(t, "jane@example.com", out.Data[0].Author)
	assert.Equal(t, "draft", out.Data[0].Content, "versions carry full content")

	rec = doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/missing/versions")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminGetVersion(t *testing.T) {
	deps := seededDeps().deps
	mux := newTestMux(t, deps)

	rec := doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/p1/versions/1")
	require.Equal(t, http.StatusOK, rec.Code)
	var v prompt.Version
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, "live", v.Content)

	assert.Equal(t, http.StatusNotFound, doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/p1/versions/9").Code)
	assert.Equal(t, http.StatusNotFound, doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/p1/versions/zero").Code)
}

func TestApproveVersion(t *testing.T) {
	fx := seededDeps()
	deps, versions, reg := fx.deps, fx.versions, fx.reg
	mux := newTestMux(t, deps)

	rec := doReq(t, mux, http.MethodPost, "/api/v1/admin/prompts/p1/versions/2/approve")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out prompt.Prompt
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, 2, out.Version)
	assert.Equal(t, "admin@example.com", out.ApprovedBy)
	assert.Equal(t, []int{2}, versions.approved)
	assert.Equal(t, []string{"report"}, reg.unregistered, "runtime metadata refreshed")
	assert.Equal(t, []string{"report"}, reg.registered)

	// An already-applied version is not approvable.
	assert.Equal(t, http.StatusConflict, doReq(t, mux, http.MethodPost, "/api/v1/admin/prompts/p1/versions/1/approve").Code)
	// A missing version 404s.
	assert.Equal(t, http.StatusNotFound, doReq(t, mux, http.MethodPost, "/api/v1/admin/prompts/p1/versions/9/approve").Code)
}

func TestApproveVersion_StoreConflictSurfaces(t *testing.T) {
	fx := seededDeps()
	deps, versions := fx.deps, fx.versions
	versions.approveErr = fmt.Errorf("cannot approve a version of a deprecated prompt: %w", prompt.ErrVersionConflict)
	mux := newTestMux(t, deps)

	rec := doReq(t, mux, http.MethodPost, "/api/v1/admin/prompts/p1/versions/2/approve")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "deprecated")
}

// A non-conflict store failure on approve is a 500 with a generic message:
// driver detail never reaches the client.
func TestApproveVersion_StoreFailureIs500Generic(t *testing.T) {
	fx := seededDeps()
	deps, versions := fx.deps, fx.versions
	versions.approveErr = errors.New("dial tcp 10.0.0.5:5432: connection refused")
	mux := newTestMux(t, deps)

	rec := doReq(t, mux, http.MethodPost, "/api/v1/admin/prompts/p1/versions/2/approve")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "dial tcp")
}

func TestRejectVersion(t *testing.T) {
	fx := seededDeps()
	deps, versions := fx.deps, fx.versions
	mux := newTestMux(t, deps)

	rec := doReq(t, mux, http.MethodPost, "/api/v1/admin/prompts/p1/versions/2/reject")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []int{2}, versions.rejected)

	assert.Equal(t, http.StatusConflict, doReq(t, mux, http.MethodPost, "/api/v1/admin/prompts/p1/versions/1/reject").Code)
}

func TestAdminUsage(t *testing.T) {
	deps := seededDeps().deps
	last := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	usage := &fakeUsage{usage: map[string]prompt.Usage{"p1": {RunCount: 12, LastRunAt: &last}}}
	deps.Usage = usage
	mux := newTestMux(t, deps)

	rec := doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/usage")
	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]prompt.Usage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, int64(12), out["p1"].RunCount)
	assert.ElementsMatch(t, []string{"p1", "p2"}, usage.gotIDs)
}

func TestAdminUsage_NoReaderReturnsEmpty(t *testing.T) {
	deps := seededDeps().deps
	mux := newTestMux(t, deps)

	rec := doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/usage")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "{}", rec.Body.String())
}

func TestPortalListVersions_Authorization(t *testing.T) {
	deps := seededDeps().deps

	// No identity: 401.
	deps.PortalUser = func(*http.Request) *PortalIdentity { return nil }
	mux := newTestMux(t, deps)
	assert.Equal(t, http.StatusUnauthorized, doReq(t, mux, http.MethodGet, "/api/v1/portal/prompts/p2/versions").Code)

	// The owner reads their own history.
	deps.PortalUser = func(*http.Request) *PortalIdentity {
		return &PortalIdentity{Email: "sarah@example.com"}
	}
	mux = newTestMux(t, deps)
	assert.Equal(t, http.StatusOK, doReq(t, mux, http.MethodGet, "/api/v1/portal/prompts/p2/versions").Code)
	// But not a shared prompt's history (admin curation surface).
	assert.Equal(t, http.StatusForbidden, doReq(t, mux, http.MethodGet, "/api/v1/portal/prompts/p1/versions").Code)

	// A non-owner is denied; an admin is not.
	deps.PortalUser = func(*http.Request) *PortalIdentity {
		return &PortalIdentity{Email: "bob@example.com"}
	}
	mux = newTestMux(t, deps)
	assert.Equal(t, http.StatusForbidden, doReq(t, mux, http.MethodGet, "/api/v1/portal/prompts/p2/versions").Code)

	deps.PortalUser = func(*http.Request) *PortalIdentity {
		return &PortalIdentity{Email: "root@example.com", IsAdmin: true}
	}
	mux = newTestMux(t, deps)
	assert.Equal(t, http.StatusOK, doReq(t, mux, http.MethodGet, "/api/v1/portal/prompts/p1/versions").Code)
}

func TestPortalUsage_ScopesToVisiblePrompts(t *testing.T) {
	fx := seededDeps()
	deps, store := fx.deps, fx.store
	store.prompts["p3"] = &prompt.Prompt{
		ID: "p3", Name: "team", Scope: prompt.ScopePersona,
		Personas: []string{"analyst"}, Enabled: true,
	}
	store.prompts["p4"] = &prompt.Prompt{
		ID: "p4", Name: "other", Scope: prompt.ScopePersonal,
		OwnerEmail: "someone-else@example.com", Enabled: true,
	}
	usage := &fakeUsage{usage: map[string]prompt.Usage{}}
	deps.Usage = usage
	deps.PortalUser = func(*http.Request) *PortalIdentity {
		return &PortalIdentity{Email: "sarah@example.com", Persona: "analyst"}
	}
	mux := newTestMux(t, deps)

	rec := doReq(t, mux, http.MethodGet, "/api/v1/portal/prompts/usage")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.ElementsMatch(t, []string{"p1", "p2", "p3"}, usage.gotIDs,
		"own personal + global + persona prompts; never another user's personal prompt")
}

func TestPortalUsage_AdminSeesAll(t *testing.T) {
	deps := seededDeps().deps
	usage := &fakeUsage{usage: map[string]prompt.Usage{}}
	deps.Usage = usage
	deps.PortalUser = func(*http.Request) *PortalIdentity {
		return &PortalIdentity{Email: "root@example.com", IsAdmin: true}
	}
	mux := newTestMux(t, deps)

	rec := doReq(t, mux, http.MethodGet, "/api/v1/portal/prompts/usage")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.ElementsMatch(t, []string{"p1", "p2"}, usage.gotIDs)
}

func TestPortalUsage_Unauthorized(t *testing.T) {
	deps := seededDeps().deps
	deps.PortalUser = func(*http.Request) *PortalIdentity { return nil }
	mux := newTestMux(t, deps)
	assert.Equal(t, http.StatusUnauthorized, doReq(t, mux, http.MethodGet, "/api/v1/portal/prompts/usage").Code)
}

func TestUsage_ReaderErrorIs500(t *testing.T) {
	deps := seededDeps().deps
	deps.Usage = &fakeUsage{err: errors.New("db down")}
	mux := newTestMux(t, deps)
	assert.Equal(t, http.StatusInternalServerError, doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/usage").Code)
}

// Store and version-store failures surface as 500s on every read route, and
// as a conflict on a reject that fails after validation.
func TestStoreErrorsSurfaceAs500(t *testing.T) {
	fx := seededDeps()
	deps, store, versions := fx.deps, fx.store, fx.versions
	mux := newTestMux(t, deps)

	versions.listErr = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/p1/versions").Code)
	versions.listErr = nil

	versions.getErr = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/p1/versions/1").Code)
	assert.Equal(t, http.StatusInternalServerError, doReq(t, mux, http.MethodPost, "/api/v1/admin/prompts/p1/versions/2/approve").Code)
	versions.getErr = nil

	store.getErr = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/p1/versions").Code)
	store.getErr = nil

	store.listErr = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, doReq(t, mux, http.MethodGet, "/api/v1/admin/prompts/usage").Code)
	store.listErr = nil

	versions.rejectErr = fmt.Errorf("draft changed underneath: %w", prompt.ErrVersionConflict)
	assert.Equal(t, http.StatusConflict, doReq(t, mux, http.MethodPost, "/api/v1/admin/prompts/p1/versions/2/reject").Code)
	versions.rejectErr = errors.New("dial tcp: connection refused")
	assert.Equal(t, http.StatusInternalServerError, doReq(t, mux, http.MethodPost, "/api/v1/admin/prompts/p1/versions/2/reject").Code)
}

func TestPortalUsage_ListErrorIs500(t *testing.T) {
	fx := seededDeps()
	deps, store := fx.deps, fx.store
	deps.Usage = &fakeUsage{usage: map[string]prompt.Usage{}}
	deps.PortalUser = func(*http.Request) *PortalIdentity {
		return &PortalIdentity{Email: "sarah@example.com", Persona: "analyst"}
	}
	store.listErr = errors.New("db down")
	mux := newTestMux(t, deps)
	assert.Equal(t, http.StatusInternalServerError, doReq(t, mux, http.MethodGet, "/api/v1/portal/prompts/usage").Code)
}
