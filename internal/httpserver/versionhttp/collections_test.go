package versionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// fakeCollections is an in-memory prompt.CollectionStore.
type fakeCollections struct {
	cols      map[string]*prompt.Collection // by id
	store     *fakeStore                    // assignment target
	nextID    int
	createErr error
	listErr   error
	assignErr error
	getErr    error
	deleteErr error
}

func (c *fakeCollections) CreateCollection(_ context.Context, col *prompt.Collection) error {
	if c.createErr != nil {
		return c.createErr
	}
	for _, existing := range c.cols {
		if strings.EqualFold(existing.Name, col.Name) {
			return prompt.ErrCollectionExists
		}
	}
	c.nextID++
	col.ID = "col-" + strings.Repeat("x", c.nextID)
	col.CreatedAt = time.Unix(1700000000, 0).UTC()
	col.UpdatedAt = col.CreatedAt
	c.cols[col.ID] = col
	return nil
}

func (c *fakeCollections) GetCollection(_ context.Context, id string) (*prompt.Collection, error) {
	if c.getErr != nil {
		return nil, c.getErr
	}
	return c.cols[id], nil //nolint:nilnil // interface contract
}

func (c *fakeCollections) ListCollections(context.Context) ([]prompt.Collection, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	out := []prompt.Collection{}
	for _, col := range c.cols {
		out = append(out, *col)
	}
	return out, nil
}

func (c *fakeCollections) UpdateCollection(_ context.Context, id, name, description string) error {
	col, ok := c.cols[id]
	if !ok {
		return prompt.ErrCollectionNotFound
	}
	for otherID, existing := range c.cols {
		if otherID != id && strings.EqualFold(existing.Name, name) {
			return prompt.ErrCollectionExists
		}
	}
	col.Name, col.Description = name, description
	return nil
}

func (c *fakeCollections) DeleteCollection(_ context.Context, id string) error {
	if c.deleteErr != nil {
		return c.deleteErr
	}
	delete(c.cols, id)
	return nil
}

func (c *fakeCollections) SetPromptCollection(_ context.Context, promptID, collectionID string) error {
	if c.assignErr != nil {
		return c.assignErr
	}
	if collectionID != "" {
		if _, ok := c.cols[collectionID]; !ok {
			return prompt.ErrCollectionNotFound
		}
	}
	if p, ok := c.store.prompts[promptID]; ok {
		p.CollectionID = collectionID
	}
	return nil
}

// collectionFixture seeds prompts plus one collection created by sarah.
func collectionFixture() (Deps, *fakeCollections) {
	fx := seededDeps()
	fx.store.prompts["p7"] = &prompt.Prompt{
		ID: "p7", Name: "sys", Scope: prompt.ScopeGlobal, Source: prompt.SourceSystem, Enabled: true,
	}
	cols := &fakeCollections{cols: map[string]*prompt.Collection{
		"col-1": {ID: "col-1", Name: "Sales", Description: "Sales SOPs", CreatedBy: "sarah@example.com"},
	}, store: fx.store}
	fx.deps.Collections = cols
	return fx.deps, cols
}

// asUser sets the portal identity accessor on a copy of deps.
func asUser(deps Deps, email string, isAdmin bool) Deps {
	deps.PortalUser = func(*http.Request) *PortalIdentity {
		return &PortalIdentity{Email: email, IsAdmin: isAdmin}
	}
	return deps
}

// doBodyReq performs a request with a JSON body.
func doBodyReq(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCollectionRoutes_NotRegisteredWithoutCapability(t *testing.T) {
	deps := seededDeps().deps
	deps.PortalUser = func(*http.Request) *PortalIdentity { return &PortalIdentity{Email: "x@example.com"} }
	mux := newTestMux(t, deps)

	assert.Equal(t, http.StatusNotFound, doReq(t, mux, http.MethodGet, "/api/v1/portal/prompt-collections").Code)
	assert.Equal(t, http.StatusNotFound, doReq(t, mux, http.MethodGet, "/api/v1/admin/prompt-collections").Code)
}

func TestListCollections(t *testing.T) {
	deps, _ := collectionFixture()
	mux := newTestMux(t, asUser(deps, "bob@example.com", false))

	for _, path := range []string{"/api/v1/portal/prompt-collections", "/api/v1/admin/prompt-collections"} {
		rec := doReq(t, mux, http.MethodGet, path)
		require.Equal(t, http.StatusOK, rec.Code, path)
		var out collectionListResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		require.Equal(t, 1, out.Total)
		assert.Equal(t, "Sales", out.Data[0].Name)
	}
}

func TestPortalCollections_RequireIdentity(t *testing.T) {
	deps, _ := collectionFixture()
	deps.PortalUser = func(*http.Request) *PortalIdentity { return nil }
	mux := newTestMux(t, deps)

	assert.Equal(t, http.StatusUnauthorized, doReq(t, mux, http.MethodGet, "/api/v1/portal/prompt-collections").Code)
	assert.Equal(t, http.StatusUnauthorized, doBodyReq(t, mux, http.MethodPost, "/api/v1/portal/prompt-collections", `{"name":"X"}`).Code)
	assert.Equal(t, http.StatusUnauthorized, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/p2/collection", `{"collection_id":"col-1"}`).Code)
}

func TestCreateCollection(t *testing.T) {
	deps, cols := collectionFixture()
	mux := newTestMux(t, asUser(deps, "bob@example.com", false))

	rec := doBodyReq(t, mux, http.MethodPost, "/api/v1/portal/prompt-collections", `{"name":"Marketing","description":"d"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created prompt.Collection
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "bob@example.com", created.CreatedBy, "portal create attributes the caller")
	assert.NotEmpty(t, created.ID)

	// Name collisions are 409; invalid bodies, names, and oversized
	// descriptions are 400.
	assert.Equal(t, http.StatusConflict, doBodyReq(t, mux, http.MethodPost, "/api/v1/portal/prompt-collections", `{"name":"sales"}`).Code)
	assert.Equal(t, http.StatusBadRequest, doBodyReq(t, mux, http.MethodPost, "/api/v1/portal/prompt-collections", `{"name":"  "}`).Code)
	assert.Equal(t, http.StatusBadRequest, doBodyReq(t, mux, http.MethodPost, "/api/v1/portal/prompt-collections", `not-json`).Code)
	longDesc := `{"name":"Bounded","description":"` + strings.Repeat("a", 2001) + `"}`
	assert.Equal(t, http.StatusBadRequest, doBodyReq(t, mux, http.MethodPost, "/api/v1/portal/prompt-collections", longDesc).Code)

	// The admin surface attributes the admin identity accessor.
	rec = doBodyReq(t, mux, http.MethodPost, "/api/v1/admin/prompt-collections", `{"name":"Ops"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "admin@example.com", created.CreatedBy)
	assert.Len(t, cols.cols, 3)
}

func TestUpdateCollection_Permissions(t *testing.T) {
	deps, cols := collectionFixture()

	// A non-creator cannot rename it.
	mux := newTestMux(t, asUser(deps, "bob@example.com", false))
	assert.Equal(t, http.StatusForbidden, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompt-collections/col-1", `{"name":"Hijacked"}`).Code)

	// The creator can.
	mux = newTestMux(t, asUser(deps, "sarah@example.com", false))
	rec := doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompt-collections/col-1", `{"name":"Sales Ops","description":"renamed"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Sales Ops", cols.cols["col-1"].Name)

	// An admin can via either surface; missing ids are 404.
	mux = newTestMux(t, asUser(deps, "root@example.com", true))
	assert.Equal(t, http.StatusOK, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompt-collections/col-1", `{"name":"Sales"}`).Code)
	assert.Equal(t, http.StatusOK, doBodyReq(t, mux, http.MethodPut, "/api/v1/admin/prompt-collections/col-1", `{"name":"Sales Team"}`).Code)
	assert.Equal(t, http.StatusNotFound, doBodyReq(t, mux, http.MethodPut, "/api/v1/admin/prompt-collections/missing", `{"name":"X"}`).Code)

	// Renaming onto another collection's name is a 409.
	rec = doBodyReq(t, mux, http.MethodPost, "/api/v1/admin/prompt-collections", `{"name":"Ops"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, http.StatusConflict, doBodyReq(t, mux, http.MethodPut, "/api/v1/admin/prompt-collections/col-1", `{"name":"ops"}`).Code)
}

func TestDeleteCollection_Permissions(t *testing.T) {
	deps, cols := collectionFixture()

	mux := newTestMux(t, asUser(deps, "bob@example.com", false))
	assert.Equal(t, http.StatusForbidden, doReq(t, mux, http.MethodDelete, "/api/v1/portal/prompt-collections/col-1").Code)

	// A delete-path store failure is a 500, and the collection survives.
	cols.deleteErr = assert.AnError
	mux = newTestMux(t, asUser(deps, "sarah@example.com", false))
	assert.Equal(t, http.StatusInternalServerError, doReq(t, mux, http.MethodDelete, "/api/v1/portal/prompt-collections/col-1").Code)
	cols.deleteErr = nil

	assert.Equal(t, http.StatusOK, doReq(t, mux, http.MethodDelete, "/api/v1/portal/prompt-collections/col-1").Code)
	assert.Empty(t, cols.cols)
	assert.Equal(t, http.StatusNotFound, doReq(t, mux, http.MethodDelete, "/api/v1/portal/prompt-collections/col-1").Code)
}

func TestAdminDeleteCollection(t *testing.T) {
	deps, cols := collectionFixture()
	mux := newTestMux(t, deps)

	assert.Equal(t, http.StatusOK, doReq(t, mux, http.MethodDelete, "/api/v1/admin/prompt-collections/col-1").Code)
	assert.Empty(t, cols.cols)
	assert.Equal(t, http.StatusNotFound, doReq(t, mux, http.MethodDelete, "/api/v1/admin/prompt-collections/missing").Code)
}

func TestLoadCollection_StoreFailure(t *testing.T) {
	deps, cols := collectionFixture()
	cols.getErr = assert.AnError
	mux := newTestMux(t, asUser(deps, "sarah@example.com", false))

	assert.Equal(t, http.StatusInternalServerError, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompt-collections/col-1", `{"name":"X"}`).Code)
}

func TestAssignCollection_Portal(t *testing.T) {
	deps, _ := collectionFixture()

	// The owner organizes their own personal prompt.
	mux := newTestMux(t, asUser(deps, "sarah@example.com", false))
	rec := doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/p2/collection", `{"collection_id":"col-1"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var updated prompt.Prompt
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	assert.Equal(t, "col-1", updated.CollectionID)

	// Clearing releases the prompt to the uncollected group. (Fresh var:
	// collection_id is omitempty, so an empty value is absent from the JSON.)
	rec = doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/p2/collection", `{"collection_id":""}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var cleared prompt.Prompt
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cleared))
	assert.Empty(t, cleared.CollectionID)

	// A non-admin cannot organize a shared prompt or someone else's personal
	// prompt; an admin can organize shared prompts.
	assert.Equal(t, http.StatusForbidden, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/p1/collection", `{"collection_id":"col-1"}`).Code)
	mux = newTestMux(t, asUser(deps, "bob@example.com", false))
	assert.Equal(t, http.StatusForbidden, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/p2/collection", `{"collection_id":"col-1"}`).Code)
	mux = newTestMux(t, asUser(deps, "root@example.com", true))
	assert.Equal(t, http.StatusOK, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/p1/collection", `{"collection_id":"col-1"}`).Code)

	// System rows are read-only on every surface.
	assert.Equal(t, http.StatusForbidden, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/p7/collection", `{"collection_id":"col-1"}`).Code)
	assert.Equal(t, http.StatusForbidden, doBodyReq(t, mux, http.MethodPut, "/api/v1/admin/prompts/p7/collection", `{"collection_id":"col-1"}`).Code)

	// Unknown targets are 404: the prompt, or the collection (deleted in a race).
	assert.Equal(t, http.StatusNotFound, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/missing/collection", `{"collection_id":"col-1"}`).Code)
	assert.Equal(t, http.StatusNotFound, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/p2/collection", `{"collection_id":"missing"}`).Code)
	assert.Equal(t, http.StatusBadRequest, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/p2/collection", `not-json`).Code)
}

func TestAssignCollection_Admin(t *testing.T) {
	deps, _ := collectionFixture()
	mux := newTestMux(t, asUser(deps, "root@example.com", true))

	rec := doBodyReq(t, mux, http.MethodPut, "/api/v1/admin/prompts/p1/collection", `{"collection_id":"col-1"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var updated prompt.Prompt
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	assert.Equal(t, "col-1", updated.CollectionID)
}

func TestCollections_StoreFailures(t *testing.T) {
	deps, cols := collectionFixture()
	cols.listErr = assert.AnError
	cols.createErr = assert.AnError
	cols.assignErr = assert.AnError
	mux := newTestMux(t, asUser(deps, "sarah@example.com", false))

	assert.Equal(t, http.StatusInternalServerError, doReq(t, mux, http.MethodGet, "/api/v1/portal/prompt-collections").Code)
	assert.Equal(t, http.StatusInternalServerError, doBodyReq(t, mux, http.MethodPost, "/api/v1/portal/prompt-collections", `{"name":"X"}`).Code)
	assert.Equal(t, http.StatusInternalServerError, doBodyReq(t, mux, http.MethodPut, "/api/v1/portal/prompts/p2/collection", `{"collection_id":"col-1"}`).Code)
}
