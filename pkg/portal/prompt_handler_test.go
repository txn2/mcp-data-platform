package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// --- Mock PromptStore ---

type mockPromptStore struct {
	prompts   map[string]*prompt.Prompt
	createErr error
	updateErr error
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

func (m *mockPromptStore) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	for _, p := range m.prompts {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, nil
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/prompts", nil)
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

	req := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/portal/prompts", nil), "alice@example.com")
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
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/v1/portal/prompts", bytes.NewReader(bodyBytes)), "alice@example.com")
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
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/v1/portal/prompts", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPortalDeletePrompt_OwnPrompt(t *testing.T) {
	h, store, registrar := newTestPortalPromptHandler()
	store.prompts["my-prompt"] = &prompt.Prompt{ID: "uuid-1", Name: "my-prompt", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}

	req := withUser(httptest.NewRequest(http.MethodDelete, "/api/v1/portal/prompts/uuid-1", nil), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, store.prompts, "my-prompt")
	assert.Contains(t, registrar.unregistered, "my-prompt")
}

func TestPortalDeletePrompt_OtherUserDenied(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["other-prompt"] = &prompt.Prompt{ID: "uuid-1", Name: "other-prompt", OwnerEmail: "bob@example.com"}

	req := withUser(httptest.NewRequest(http.MethodDelete, "/api/v1/portal/prompts/uuid-1", nil), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPortalDeletePrompt_AdminCanDeleteOthers(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["other-prompt"] = &prompt.Prompt{ID: "uuid-1", Name: "other-prompt", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com"}

	req := withUser(httptest.NewRequest(http.MethodDelete, "/api/v1/portal/prompts/uuid-1", nil), "admin@example.com", "admin")
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
	req := withUser(httptest.NewRequest(http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "new content", store.prompts["my-prompt"].Content)
	assert.Contains(t, registrar.unregistered, "my-prompt")
	assert.Contains(t, registrar.registered, "my-prompt")
}

func TestPortalUpdatePrompt_OtherUserDenied(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["other-prompt"] = &prompt.Prompt{
		ID: "uuid-1", Name: "other-prompt", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com",
	}

	body := portalPromptCreateRequest{Content: "hacked"}
	bodyBytes, _ := json.Marshal(body)
	req := withUser(httptest.NewRequest(http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "alice@example.com")
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
	req := withUser(httptest.NewRequest(http.MethodPut, "/api/v1/portal/prompts/uuid-1", bytes.NewReader(bodyBytes)), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPortalDeletePrompt_CannotDeleteGlobalScope(t *testing.T) {
	h, store, _ := newTestPortalPromptHandler()
	store.prompts["global-prompt"] = &prompt.Prompt{
		ID: "uuid-1", Name: "global-prompt", Scope: prompt.ScopeGlobal,
	}

	req := withUser(httptest.NewRequest(http.MethodDelete, "/api/v1/portal/prompts/uuid-1", nil), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

