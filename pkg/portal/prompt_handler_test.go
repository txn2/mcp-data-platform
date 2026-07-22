package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// --- Mock PromptStore ---

type mockPromptStore struct {
	prompts    map[string]*prompt.Prompt
	createErr  error
	updateErr  error
	getByIDErr error
}

func newMockPromptStore() *mockPromptStore {
	return &mockPromptStore{prompts: make(map[string]*prompt.Prompt)}
}

func (m *mockPromptStore) Create(_ context.Context, p *prompt.Prompt) error {
	if m.createErr != nil {
		return m.createErr
	}
	p.ID = "generated-uuid"
	m.prompts[p.Name] = p
	return nil
}

func (m *mockPromptStore) Get(_ context.Context, name string) (*prompt.Prompt, error) {
	p := m.prompts[name]
	return p, nil
}

func (m *mockPromptStore) GetPersonal(_ context.Context, ownerEmail, name string) (*prompt.Prompt, error) {
	for _, p := range m.prompts {
		if p.Scope == prompt.ScopePersonal && p.OwnerEmail == ownerEmail && p.Name == name {
			return p, nil
		}
	}
	return nil, nil //nolint:nilnil // Store interface contract: nil, nil means not found
}

func (m *mockPromptStore) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	for _, p := range m.prompts {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, nil //nolint:nilnil // Store interface contract: nil, nil means not found
}

func (m *mockPromptStore) Update(_ context.Context, p *prompt.Prompt) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.prompts[p.Name] = p
	return nil
}

func (m *mockPromptStore) Delete(_ context.Context, name string) error {
	delete(m.prompts, name)
	return nil
}

func (m *mockPromptStore) DeleteByID(_ context.Context, id string) error {
	for name, p := range m.prompts {
		if p.ID == id {
			delete(m.prompts, name)
			return nil
		}
	}
	return nil
}

func (m *mockPromptStore) List(_ context.Context, f prompt.ListFilter) ([]prompt.Prompt, error) {
	var result []prompt.Prompt
	for _, p := range m.prompts {
		if f.Scope != "" && p.Scope != f.Scope {
			continue
		}
		if f.OwnerEmail != "" && p.OwnerEmail != f.OwnerEmail {
			continue
		}
		result = append(result, *p)
	}
	return result, nil
}

func (m *mockPromptStore) Count(_ context.Context, _ prompt.ListFilter) (int, error) {
	return len(m.prompts), nil
}

var _ PromptStore = (*mockPromptStore)(nil)

// --- Mock PromptRegistrar ---

type mockPromptRegistrar struct {
	registered   []string
	unregistered []string
}

func (m *mockPromptRegistrar) RegisterRuntimePrompt(p *prompt.Prompt) {
	m.registered = append(m.registered, p.Name)
}

func (m *mockPromptRegistrar) UnregisterRuntimePrompt(name string) {
	m.unregistered = append(m.unregistered, name)
}

var _ PromptRegistrar = (*mockPromptRegistrar)(nil)

func withUser(r *http.Request, email string, roles ...string) *http.Request {
	ctx := context.WithValue(r.Context(), portalUserKey, &User{
		UserID: "user-123",
		Email:  email,
		Roles:  roles,
	})
	return r.WithContext(ctx)
}

func newTestPortalPromptHandler() (*Handler, *mockPromptStore, *mockPromptRegistrar) {
	store := newMockPromptStore()
	registrar := &mockPromptRegistrar{}
	h := NewHandler(Deps{
		PromptStore:     store,
		PromptRegistrar: registrar,
		AdminRoles:      []string{"admin"},
		AssetStore:      &noopAssetStore{},
	}, nil)
	return h, store, registrar
}

func TestPortalListPrompts_Unauthenticated(t *testing.T) {
	h, _, _ := newTestPortalPromptHandler()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPortalListPrompts_Authenticated(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["my-prompt"] = &prompt.Prompt{
		ID: "uuid-1", Name: "my-prompt", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com",
	}
	store.prompts["global-prompt"] = &prompt.Prompt{
		ID: "uuid-2", Name: "global-prompt", Scope: prompt.ScopeGlobal,
	}

	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp portalPromptListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.GreaterOrEqual(t, len(resp.Personal)+len(resp.Available), 1)
}

func TestPortalCreatePrompt_Success(t *testing.T) {
	h, store, registrar := newTestPortalPromptHandler()

	body := portalPromptCreateRequest{Name: "my-prompt", Content: "test content"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, store.prompts, "my-prompt")
	assert.Equal(t, prompt.ScopePersonal, store.prompts["my-prompt"].Scope)
	assert.Equal(t, "alice@example.com", store.prompts["my-prompt"].OwnerEmail)
	assert.Contains(t, registrar.registered, "my-prompt")
}

func TestPortalCreatePrompt_MissingName(t *testing.T) {
	h, _, _ := newTestPortalPromptHandler()
	body := portalPromptCreateRequest{Content: "something"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPortalDeletePrompt_OwnPrompt(t *testing.T) {
	h, store, registrar := newTestPortalPromptHandler()
	store.prompts["my-prompt"] = &prompt.Prompt{ID: "uuid-1", Name: "my-prompt", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}

	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/prompts/uuid-1", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, store.prompts, "my-prompt")
	assert.Contains(t, registrar.unregistered, "my-prompt")
}

func TestPortalDeletePrompt_OtherUserDenied(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["other-prompt"] = &prompt.Prompt{ID: "uuid-1", Name: "other-prompt", OwnerEmail: "bob@example.com"}

	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/prompts/uuid-1", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPortalDeletePrompt_AdminCanDeleteOthers(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["other-prompt"] = &prompt.Prompt{ID: "uuid-1", Name: "other-prompt", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com"}

	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/prompts/uuid-1", http.NoBody), "admin@example.com", "admin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPortalUpdatePrompt_OwnPrompt(t *testing.T) {
	h, store, registrar := newTestPortalPromptHandler()
	store.prompts["my-prompt"] = &prompt.Prompt{
		ID: "uuid-1", Name: "my-prompt", Content: "old", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com", Enabled: true,
	}

	body := portalPromptCreateRequest{Content: "new content"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "new content", store.prompts["my-prompt"].Content)
	// Personal prompts are served per-caller from the store and are not tracked
	// in the name-keyed runtime metadata, so no (un)registration occurs.
	assert.NotContains(t, registrar.unregistered, "my-prompt")
	assert.NotContains(t, registrar.registered, "my-prompt")
}

// An admin may edit a shared (non-personal) prompt through the portal; those
// are tracked in the name-keyed runtime metadata, so reregistration occurs.
func TestPortalUpdatePrompt_AdminSharedReregisters(t *testing.T) {
	h, store, registrar := newTestPortalPromptHandler()
	store.prompts["g"] = &prompt.Prompt{
		ID: "uuid-1", Name: "g", Content: "old", Scope: prompt.ScopeGlobal, Enabled: true,
	}

	body := portalPromptCreateRequest{Content: "new content"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "admin@example.com", "admin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, registrar.unregistered, "g")
	assert.Contains(t, registrar.registered, "g")
}

func TestPortalUpdatePrompt_OtherUserDenied(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["other-prompt"] = &prompt.Prompt{
		ID: "uuid-1", Name: "other-prompt", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com",
	}

	body := portalPromptCreateRequest{Content: "hacked"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPortalUpdatePrompt_CannotUpdateGlobalScope(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["global-prompt"] = &prompt.Prompt{
		ID: "uuid-1", Name: "global-prompt", Scope: prompt.ScopeGlobal,
	}

	body := portalPromptCreateRequest{Content: "modified"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPortalDeletePrompt_CannotDeleteGlobalScope(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["global-prompt"] = &prompt.Prompt{
		ID: "uuid-1", Name: "global-prompt", Scope: prompt.ScopeGlobal,
	}

	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/prompts/uuid-1", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPortalUpdatePrompt_RenameConflict(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["prompt-a"] = &prompt.Prompt{
		ID: "uuid-a", Name: "prompt-a", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com", Enabled: true,
	}
	store.prompts["prompt-b"] = &prompt.Prompt{
		ID: "uuid-b", Name: "prompt-b", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com",
	}

	body := portalPromptCreateRequest{Name: "prompt-b"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-a", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestPortalUpdatePrompt_InvalidName(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["my-prompt"] = &prompt.Prompt{
		ID: "uuid-1", Name: "my-prompt", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com",
	}

	body := portalPromptCreateRequest{Name: "INVALID NAME!"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPortalUpdatePrompt_AllFields(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["my-prompt"] = &prompt.Prompt{
		ID: "uuid-1", Name: "my-prompt", Content: "old", Scope: prompt.ScopePersonal,
		OwnerEmail: "alice@example.com", Enabled: true,
	}

	body := portalPromptCreateRequest{
		DisplayName: "Updated",
		Description: "Updated desc",
		Content:     "new content",
		Category:    "analytics",
		Arguments:   []prompt.Argument{{Name: "topic", Required: true}},
	}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	p := store.prompts["my-prompt"]
	assert.Equal(t, "Updated", p.DisplayName)
	assert.Equal(t, "Updated desc", p.Description)
	assert.Equal(t, "new content", p.Content)
	assert.Equal(t, "analytics", p.Category)
	assert.Len(t, p.Arguments, 1)
}

func newTestPortalPromptShareHandler() (*Handler, *mockPromptStore, *mockShareStore) {
	pstore := newMockPromptStore()
	sstore := &mockShareStore{}
	h := NewHandler(Deps{
		PromptStore: pstore,
		ShareStore:  sstore,
		AdminRoles:  []string{"admin"},
		AssetStore:  &noopAssetStore{},
	}, nil)
	return h, pstore, sstore
}

func TestCreatePromptShare_OwnerOnly(t *testing.T) {
	h, store, sstore := newTestPortalPromptShareHandler()
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com",
	}
	body, _ := json.Marshal(createShareRequest{SharedWithEmail: "bob@example.com", Permission: "viewer"})

	// Owner can share.
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", bytes.NewReader(body)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
	require.NotNil(t, sstore.inserted)
	assert.Equal(t, "p1", sstore.inserted.PromptID)
	assert.Equal(t, "bob@example.com", sstore.inserted.SharedWithEmail)

	// A non-owner cannot.
	req = withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", bytes.NewReader(body)), "mallory@example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreatePromptShare_RejectsNonPersonal(t *testing.T) {
	h, store, _ := newTestPortalPromptShareHandler()
	store.prompts["g"] = &prompt.Prompt{ID: "g1", Name: "g", Scope: prompt.ScopeGlobal, OwnerEmail: "alice@example.com"}
	body, _ := json.Marshal(createShareRequest{SharedWithEmail: "bob@example.com"})
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/g1/shares", bytes.NewReader(body)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListSharedPrompts(t *testing.T) {
	h, store, sstore := newTestPortalPromptShareHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	sstore.promptRefs = []SharedPromptRef{{PromptID: "p1", ShareID: "s1", SharedBy: "alice@example.com", Permission: "viewer"}}

	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/shared-prompts", http.NoBody), "bob@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var out []SharedPrompt
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "report", out[0].Prompt.Name)
	assert.Equal(t, "alice@example.com", out[0].SharedBy)
}

func TestCreatePromptShare_RequiresRecipient(t *testing.T) {
	h, store, _ := newTestPortalPromptShareHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	body, _ := json.Marshal(createShareRequest{}) // no recipient
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", bytes.NewReader(body)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePromptShare_RejectsSelfShare(t *testing.T) {
	h, store, _ := newTestPortalPromptShareHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	body, _ := json.Marshal(createShareRequest{SharedWithEmail: "Alice@example.com"})
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", bytes.NewReader(body)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePromptShare_NotFound(t *testing.T) {
	h, _, _ := newTestPortalPromptShareHandler()
	body, _ := json.Marshal(createShareRequest{SharedWithEmail: "bob@example.com"})
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/missing/shares", bytes.NewReader(body)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListPromptShares_OwnerOnly(t *testing.T) {
	h, store, sstore := newTestPortalPromptShareHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	sstore.listByAsset = nil // unused

	// Owner lists.
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/p1/shares", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Non-owner forbidden.
	req = withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/p1/shares", http.NoBody), "mallory@example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestRevokePromptShare_OwnerOnly(t *testing.T) {
	h, store, sstore := newTestPortalPromptShareHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	sstore.getByIDShare = &Share{ID: "s1", PromptID: "p1", CreatedBy: "alice@example.com"}

	// Owner revokes.
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/shares/s1", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Non-owner forbidden.
	req = withUser(httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/shares/s1", http.NoBody), "mallory@example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreatePromptShare_GetByIDError(t *testing.T) {
	h, store, _ := newTestPortalPromptShareHandler()
	store.getByIDErr = errors.New("db down")
	body, _ := json.Marshal(createShareRequest{SharedWithEmail: "bob@example.com"})
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", bytes.NewReader(body)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreatePromptShare_InvalidBody(t *testing.T) {
	h, store, _ := newTestPortalPromptShareHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", bytes.NewReader([]byte("{not json"))), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePromptShare_InvalidPermission(t *testing.T) {
	h, store, _ := newTestPortalPromptShareHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	body, _ := json.Marshal(createShareRequest{SharedWithEmail: "bob@example.com", Permission: "owner"})
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", bytes.NewReader(body)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePromptShare_InsertError(t *testing.T) {
	h, store, sstore := newTestPortalPromptShareHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	sstore.insertErr = errors.New("db down")
	body, _ := json.Marshal(createShareRequest{SharedWithEmail: "bob@example.com", Permission: "viewer"})
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", bytes.NewReader(body)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreatePromptShare_Unauthenticated(t *testing.T) {
	h, _, _ := newTestPortalPromptShareHandler()
	body, _ := json.Marshal(createShareRequest{SharedWithEmail: "bob@example.com"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListPromptShares_Unauthenticated(t *testing.T) {
	h, _, _ := newTestPortalPromptShareHandler()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/p1/shares", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListPromptShares_GetByIDError(t *testing.T) {
	h, store, _ := newTestPortalPromptShareHandler()
	store.getByIDErr = errors.New("db down")
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/p1/shares", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListPromptShares_NotFound(t *testing.T) {
	h, _, _ := newTestPortalPromptShareHandler()
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/missing/shares", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListPromptShares_ListError(t *testing.T) {
	h, store, sstore := newTestPortalPromptShareHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	sstore.listByPromptE = errors.New("db down")
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/p1/shares", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListSharedPrompts_Unauthenticated(t *testing.T) {
	h, _, _ := newTestPortalPromptShareHandler()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/shared-prompts", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListSharedPrompts_ListError(t *testing.T) {
	h, _, sstore := newTestPortalPromptShareHandler()
	sstore.promptRefsErr = errors.New("db down")
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/shared-prompts", http.NoBody), "bob@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListSharedPrompts_SkipsMissingPrompt(t *testing.T) {
	h, store, sstore := newTestPortalPromptShareHandler()
	// ref points at a prompt that no longer exists; it is skipped.
	sstore.promptRefs = []SharedPromptRef{
		{PromptID: "gone", ShareID: "s1", SharedBy: "alice@example.com", Permission: "viewer"},
		{PromptID: "p1", ShareID: "s2", SharedBy: "alice@example.com", Permission: "viewer"},
	}
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/shared-prompts", http.NoBody), "bob@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var out []SharedPrompt
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "report", out[0].Prompt.Name)
}

func TestRevokePromptShare_PromptMissing(t *testing.T) {
	h, _, sstore := newTestPortalPromptShareHandler()
	// share references a prompt the store no longer has.
	sstore.getByIDShare = &Share{ID: "s1", PromptID: "gone", CreatedBy: "alice@example.com"}
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/shares/s1", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRevokePromptShare_NoPromptStore(t *testing.T) {
	sstore := &mockShareStore{getByIDShare: &Share{ID: "s1", PromptID: "p1", CreatedBy: "alice@example.com"}}
	h := NewHandler(Deps{
		ShareStore: sstore,
		AssetStore: &noopAssetStore{},
		AdminRoles: []string{"admin"},
	}, nil)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/shares/s1", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- Versioning and read-only guards (#1009) ---

// mockVersionPromptStore adds the versioning capability to the mock store,
// with clone-on-read like the real store (a scanned row is never aliased to
// the stored state).
type mockVersionPromptStore struct {
	*mockPromptStore
	draftContents []string
}

func (*mockVersionPromptStore) clone(p *prompt.Prompt) *prompt.Prompt {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}

func (m *mockVersionPromptStore) GetByID(ctx context.Context, id string) (*prompt.Prompt, error) {
	p, err := m.mockPromptStore.GetByID(ctx, id)
	return m.clone(p), err
}

func (m *mockVersionPromptStore) UpdateWithVersion(ctx context.Context, p *prompt.Prompt, _ string) error {
	return m.Update(ctx, p)
}

func (m *mockVersionPromptStore) CreateDraftVersion(_ context.Context, _ string, proposed *prompt.Prompt, _ string) (int, error) {
	m.draftContents = append(m.draftContents, proposed.Content)
	return 7, nil
}

func (*mockVersionPromptStore) ListVersions(context.Context, string) ([]prompt.Version, error) {
	return nil, nil
}

func (*mockVersionPromptStore) GetVersion(context.Context, string, int) (*prompt.Version, error) {
	return nil, nil //nolint:nilnil // interface contract
}

func (*mockVersionPromptStore) ApproveVersion(context.Context, string, int, string) (*prompt.Prompt, error) {
	return nil, nil //nolint:nilnil // unused in these tests
}

func (*mockVersionPromptStore) RejectVersion(context.Context, string, int) error { return nil }

var _ prompt.VersionStore = (*mockVersionPromptStore)(nil)

func newVersionedPortalPromptHandler() (*Handler, *mockVersionPromptStore, *mockPromptRegistrar) {
	store := &mockVersionPromptStore{mockPromptStore: newMockPromptStore()}
	registrar := &mockPromptRegistrar{}
	h := NewHandler(Deps{
		PromptStore:     store,
		PromptRegistrar: registrar,
		AdminRoles:      []string{"admin"},
		AssetStore:      &noopAssetStore{},
	}, nil)
	return h, store, registrar
}

// An admin's content edit to an approved global prompt through the portal is
// deferred as a pending draft version: 202, no live-row write, no runtime
// re-registration of the draft content.
func TestPortalUpdatePrompt_ApprovedSharedContentEditPends(t *testing.T) {
	h, store, registrar := newVersionedPortalPromptHandler()
	store.prompts["g"] = &prompt.Prompt{
		ID: "uuid-1", Name: "g", Content: "approved body", Scope: prompt.ScopeGlobal,
		Status: prompt.StatusApproved, Enabled: true, Version: 1,
	}

	body := portalPromptCreateRequest{Content: "draft body"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "admin@example.com", "admin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	var out prompt.EditOutcome
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.False(t, out.Applied)
	assert.Equal(t, 7, out.PendingVersion)
	assert.Equal(t, []string{"draft body"}, store.draftContents)
	assert.Equal(t, "approved body", store.prompts["g"].Content, "the live row keeps the approved snapshot")
	assert.Empty(t, registrar.registered, "draft content is never re-registered")
	assert.Empty(t, registrar.unregistered)
}

// A gated content edit combined with a rename (a non-versioned change) is
// rejected whole as a conflict.
func TestPortalUpdatePrompt_MixedGatedEditConflicts(t *testing.T) {
	h, store, _ := newVersionedPortalPromptHandler()
	store.prompts["g"] = &prompt.Prompt{
		ID: "uuid-1", Name: "g", Content: "approved body", Scope: prompt.ScopePersona,
		Personas: []string{"analyst"}, Status: prompt.StatusApproved, Enabled: true, Version: 1,
	}

	body := portalPromptCreateRequest{Content: "draft body", Name: "g-renamed"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "admin@example.com", "admin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Empty(t, store.draftContents)
}

// System rows are read-only through the portal for every caller, including
// admins, on both update and delete.
func TestPortalPrompt_SystemRowsReadOnly(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["sys"] = &prompt.Prompt{
		ID: "uuid-sys", Name: "sys", Content: "config body", Scope: prompt.ScopeGlobal,
		Source: prompt.SourceSystem, Status: prompt.StatusApproved, Enabled: true,
	}

	body := portalPromptCreateRequest{Content: "hijacked"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-sys", bytes.NewReader(bodyBytes)), "admin@example.com", "admin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "read-only")
	assert.Equal(t, "config body", store.prompts["sys"].Content)

	req = withUser(httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/prompts/uuid-sys", http.NoBody), "admin@example.com", "admin")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NotNil(t, store.prompts["sys"])
}

// A store failure during the versioned apply is a 500.
func TestPortalUpdatePrompt_StoreFailureIs500(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["my-prompt"] = &prompt.Prompt{
		ID: "uuid-1", Name: "my-prompt", Content: "old", Scope: prompt.ScopePersonal,
		OwnerEmail: "alice@example.com", Enabled: true,
	}
	store.updateErr = errors.New("db down")

	body := portalPromptCreateRequest{Content: "new"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
