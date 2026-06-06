package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// --- Mock PromptStore ---

type mockPromptStore struct {
	prompts    map[string]*prompt.Prompt
	createErr  error
	updateErr  error
	deleteErr  error
	listResult []prompt.Prompt
	countVal   int
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
	// Mirror the real store: Get resolves only shared (non-personal) prompts.
	for _, p := range m.prompts {
		if p.Name == name && p.Scope != prompt.ScopePersonal {
			return p, nil
		}
	}
	return nil, nil //nolint:nilnil // Store interface contract: nil, nil means not found
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
	for _, p := range m.prompts {
		if p.ID == id {
			// Return a copy, like a real store: a handler mutating the returned
			// prompt must not change stored state until it calls Update.
			cp := *p
			return &cp, nil
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
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.prompts, name)
	return nil
}

func (m *mockPromptStore) DeleteByID(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	for name, p := range m.prompts {
		if p.ID == id {
			delete(m.prompts, name)
			return nil
		}
	}
	return nil
}

func (m *mockPromptStore) List(_ context.Context, _ prompt.ListFilter) ([]prompt.Prompt, error) {
	if m.listResult != nil {
		return m.listResult, nil
	}
	var result []prompt.Prompt
	for _, p := range m.prompts {
		result = append(result, *p)
	}
	return result, nil
}

func (m *mockPromptStore) Count(_ context.Context, _ prompt.ListFilter) (int, error) {
	return m.countVal, nil
}

var _ prompt.Store = (*mockPromptStore)(nil)

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

func newTestPromptHandler() (*Handler, *mockPromptStore, *mockPromptRegistrar) {
	store := newMockPromptStore()
	registrar := &mockPromptRegistrar{}
	h := NewHandler(Deps{
		PromptStore:     store,
		PromptRegistrar: registrar,
		Config:          testConfig(),
	}, nil)
	return h, store, registrar
}

func TestPromptRoutes_Registered(t *testing.T) {
	h, _, _ := newTestPromptHandler()
	require.NotNil(t, h)
}

func TestPromptRoutes_NotRegisteredWithNilStore(t *testing.T) {
	h := NewHandler(Deps{Config: testConfig()}, nil)
	require.NotNil(t, h)
	// No routes registered for prompts — handler still creates fine
}

func TestListPrompts_Empty(t *testing.T) {
	h, _, _ := newTestPromptHandler()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/prompts", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp adminPromptListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Data)
}

func TestCreatePrompt_Success(t *testing.T) {
	h, store, registrar := newTestPromptHandler()

	body := adminPromptCreateRequest{
		Name:    "test-prompt",
		Content: "Do something with {topic}",
		Scope:   "global",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, store.prompts, "test-prompt")
	assert.Equal(t, "global", store.prompts["test-prompt"].Scope)
	assert.Contains(t, registrar.registered, "test-prompt")
}

func TestCreatePrompt_MissingName(t *testing.T) {
	h, _, _ := newTestPromptHandler()
	body := adminPromptCreateRequest{Content: "something"}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePrompt_MissingContent(t *testing.T) {
	h, _, _ := newTestPromptHandler()
	body := adminPromptCreateRequest{Name: "test"}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetPrompt_Found(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["test"] = &prompt.Prompt{ID: "uuid-1", Name: "test", Content: "content"}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/prompts/uuid-1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var p prompt.Prompt
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	assert.Equal(t, "test", p.Name)
}

func TestGetPrompt_NotFound(t *testing.T) {
	h, _, _ := newTestPromptHandler()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/prompts/missing", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdatePrompt_Success(t *testing.T) {
	h, store, registrar := newTestPromptHandler()
	store.prompts["old-name"] = &prompt.Prompt{ID: "uuid-1", Name: "old-name", Content: "old", Enabled: true}

	update := adminPromptUpdateRequest{}
	newContent := "new content"
	update.Content = &newContent
	bodyBytes, _ := json.Marshal(update)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/prompts/uuid-1", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "new content", store.prompts["old-name"].Content)
	assert.Contains(t, registrar.unregistered, "old-name")
	assert.Contains(t, registrar.registered, "old-name")
}

func TestUpdatePrompt_NotFound(t *testing.T) {
	h, _, _ := newTestPromptHandler()
	bodyBytes := []byte(`{}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/prompts/missing", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeletePrompt_Success(t *testing.T) {
	h, store, registrar := newTestPromptHandler()
	store.prompts["test"] = &prompt.Prompt{ID: "uuid-1", Name: "test"}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/prompts/uuid-1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, store.prompts, "test")
	assert.Contains(t, registrar.unregistered, "test")
}

func TestDeletePrompt_NotFound(t *testing.T) {
	h, _, _ := newTestPromptHandler()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/prompts/missing", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCreatePrompt_StoreError(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.createErr = fmt.Errorf("db error")

	body := adminPromptCreateRequest{Name: "test", Content: "content"}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreatePrompt_DisabledNotRegistered(t *testing.T) {
	h, store, registrar := newTestPromptHandler()

	enabled := false
	body := adminPromptCreateRequest{
		Name:    "disabled-prompt",
		Content: "content",
		Enabled: &enabled,
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, store.prompts, "disabled-prompt")
	assert.NotContains(t, registrar.registered, "disabled-prompt")
}

func TestMatchesSearch(t *testing.T) {
	info := registry.PromptInfo{
		Name:        "weekly-report",
		Description: "Generate a weekly summary",
		Content:     "Analyze data for {topic}",
	}
	assert.True(t, matchesSearch(info, "weekly"))
	assert.True(t, matchesSearch(info, "summary"))
	assert.True(t, matchesSearch(info, "analyze"))
	assert.False(t, matchesSearch(info, "nonexistent"))
}

func TestUpdatePrompt_RenameConflict(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["prompt-a"] = &prompt.Prompt{ID: "uuid-a", Name: "prompt-a", Content: "a"}
	store.prompts["prompt-b"] = &prompt.Prompt{ID: "uuid-b", Name: "prompt-b", Content: "b"}

	newName := "prompt-b"
	update := adminPromptUpdateRequest{Name: &newName}
	bodyBytes, _ := json.Marshal(update)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/prompts/uuid-a", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

// The admin update path drives the lifecycle state machine: draft -> approved
// stamps the approver, and an invalid transition is rejected with 400.
func TestUpdatePrompt_StatusTransition(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["p"] = &prompt.Prompt{
		ID: "uuid-1", Name: "p", Scope: prompt.ScopeGlobal, Status: prompt.StatusDraft, Enabled: true,
	}

	approved := prompt.StatusApproved
	update := adminPromptUpdateRequest{Status: &approved}
	bodyBytes, _ := json.Marshal(update)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/prompts/uuid-1", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, prompt.StatusApproved, store.prompts["p"].Status)

	// draft is no longer reachable from approved; the state machine rejects it.
	draft := prompt.StatusDraft
	update = adminPromptUpdateRequest{Status: &draft}
	bodyBytes, _ = json.Marshal(update)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/prompts/uuid-1", bytes.NewReader(bodyBytes))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// Renaming a personal prompt into a name another personal prompt of the same
// owner already uses must surface a clean 409, not an opaque 500. The collision
// check runs against the per-owner personal namespace (GetPersonal), since
// Store.Get excludes personal scope.
func TestUpdatePrompt_PersonalRenameConflict(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["report"] = &prompt.Prompt{
		ID: "uuid-a", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@x", Content: "a",
	}
	store.prompts["summary"] = &prompt.Prompt{
		ID: "uuid-b", Name: "summary", Scope: prompt.ScopePersonal, OwnerEmail: "alice@x", Content: "b",
	}

	newName := "report"
	update := adminPromptUpdateRequest{Name: &newName}
	bodyBytes, _ := json.Marshal(update)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/prompts/uuid-b", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestUpdatePrompt_AllFields(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["test"] = &prompt.Prompt{
		ID: "uuid-1", Name: "test", Content: "old", Scope: "personal",
		OwnerEmail: "a@x.com", Source: "operator", Enabled: true,
	}

	newDisplay := "New Display"
	newDesc := "New Description"
	newContent := "New Content"
	newCategory := "analytics"
	newScope := "global"
	newOwner := "b@x.com"
	newSource := "agent"
	enabled := false
	update := adminPromptUpdateRequest{
		DisplayName: &newDisplay,
		Description: &newDesc,
		Content:     &newContent,
		Category:    &newCategory,
		Scope:       &newScope,
		Personas:    []string{"analyst"},
		OwnerEmail:  &newOwner,
		Source:      &newSource,
		Enabled:     &enabled,
	}
	bodyBytes, _ := json.Marshal(update)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/prompts/uuid-1", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	p := store.prompts["test"]
	assert.Equal(t, "New Display", p.DisplayName)
	assert.Equal(t, "New Description", p.Description)
	assert.Equal(t, "New Content", p.Content)
	assert.Equal(t, "analytics", p.Category)
	assert.Equal(t, "global", p.Scope)
	assert.Equal(t, []string{"analyst"}, p.Personas)
	assert.Equal(t, "b@x.com", p.OwnerEmail)
	assert.Equal(t, "agent", p.Source)
	assert.False(t, p.Enabled)
}

func TestUpdatePrompt_InvalidScope(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["test"] = &prompt.Prompt{ID: "uuid-1", Name: "test", Content: "c"}

	badScope := "invalid"
	update := adminPromptUpdateRequest{Scope: &badScope}
	bodyBytes, _ := json.Marshal(update)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/prompts/uuid-1", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePrompt_InvalidScope(t *testing.T) {
	h, _, _ := newTestPromptHandler()
	body := adminPromptCreateRequest{Name: "test", Content: "c", Scope: "invalid"}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListPrompts_WithSystemPrompts(t *testing.T) {
	store := newMockPromptStore()
	registrar := &mockPromptRegistrar{}
	provider := &mockPromptInfoProvider{
		infos: []registry.PromptInfo{
			{Name: "system-prompt", Description: "A system prompt", Content: "content"},
		},
	}
	h := NewHandler(Deps{
		PromptStore:        store,
		PromptRegistrar:    registrar,
		PromptInfoProvider: provider,
		Config:             testConfig(),
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/prompts", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp adminPromptListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, "system", resp.Data[0].Scope)
	assert.Equal(t, "system:system-prompt", resp.Data[0].ID)
}

func TestListPrompts_WithSearch(t *testing.T) {
	store := newMockPromptStore()
	provider := &mockPromptInfoProvider{
		infos: []registry.PromptInfo{
			{Name: "explore-data", Description: "Explore datasets"},
			{Name: "trace-lineage", Description: "Trace lineage"},
		},
	}
	h := NewHandler(Deps{
		PromptStore:        store,
		PromptInfoProvider: provider,
		Config:             testConfig(),
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/prompts?search=explore", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp adminPromptListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, "explore-data", resp.Data[0].Name)
}

// --- mock PromptInfoProvider ---

type mockPromptInfoProvider struct {
	infos []registry.PromptInfo
}

func (m *mockPromptInfoProvider) AllPromptInfos() []registry.PromptInfo {
	return m.infos
}

var _ PromptInfoProvider = (*mockPromptInfoProvider)(nil)

func TestApprovePromotion_Success(t *testing.T) {
	h, store, registrar := newTestPromptHandler()
	store.prompts["report"] = &prompt.Prompt{
		ID: "uuid-1", Name: "report", Scope: prompt.ScopePersonal, Status: prompt.StatusDraft,
		OwnerEmail: "u@x", Enabled: true,
		ReviewRequested: true, RequestedScope: prompt.ScopePersona, RequestedPersonas: []string{"analyst"},
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts/uuid-1/approve", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	got := store.prompts["report"]
	assert.Equal(t, prompt.ScopePersona, got.Scope)
	assert.Equal(t, []string{"analyst"}, got.Personas)
	assert.Equal(t, prompt.StatusApproved, got.Status)
	assert.False(t, got.ReviewRequested)
	assert.Empty(t, got.RequestedScope)
	assert.Contains(t, registrar.registered, "report")
}

func TestApprovePromotion_NoPendingRequest(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "uuid-1", Name: "report", Scope: prompt.ScopePersonal}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts/uuid-1/approve", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovePromotion_NameCollision(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	// A shared prompt already owns the name in the global namespace.
	store.prompts["report"] = &prompt.Prompt{ID: "shared-1", Name: "report", Scope: prompt.ScopeGlobal}
	store.prompts["personal:report"] = &prompt.Prompt{
		ID: "uuid-1", Name: "report", Scope: prompt.ScopePersonal, Enabled: true,
		ReviewRequested: true, RequestedScope: prompt.ScopeGlobal,
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts/uuid-1/approve", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestRejectPromotion(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["report"] = &prompt.Prompt{
		ID: "uuid-1", Name: "report", Scope: prompt.ScopePersonal,
		ReviewRequested: true, RequestedScope: prompt.ScopePersona, RequestedPersonas: []string{"analyst"},
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts/uuid-1/reject", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	got := store.prompts["report"]
	assert.False(t, got.ReviewRequested)
	assert.Equal(t, prompt.ScopePersonal, got.Scope)
}

func TestRejectPromotion_NoPendingRequest(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "uuid-1", Name: "report", Scope: prompt.ScopePersonal}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/prompts/uuid-1/reject", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// A direct admin scope change obsoletes any pending promotion request.
func TestUpdatePrompt_DirectScopeChangeClearsRequest(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["report"] = &prompt.Prompt{
		ID: "uuid-1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "u@x", Enabled: true,
		ReviewRequested: true, RequestedScope: prompt.ScopePersona, RequestedPersonas: []string{"analyst"},
	}

	newScope := prompt.ScopeGlobal
	body, _ := json.Marshal(adminPromptUpdateRequest{Scope: &newScope})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/prompts/uuid-1", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	got := store.prompts["report"]
	assert.Equal(t, prompt.ScopeGlobal, got.Scope)
	assert.False(t, got.ReviewRequested, "stale promotion request cleared on direct scope change")
}

// A direct admin promotion into the shared namespace is blocked on a name clash.
func TestUpdatePrompt_DirectScopeChangeCollision(t *testing.T) {
	h, store, _ := newTestPromptHandler()
	store.prompts["report"] = &prompt.Prompt{ID: "shared-1", Name: "report", Scope: prompt.ScopeGlobal}
	store.prompts["personal:report"] = &prompt.Prompt{
		ID: "uuid-1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "u@x",
	}

	newScope := prompt.ScopeGlobal
	body, _ := json.Marshal(adminPromptUpdateRequest{Scope: &newScope})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/prompts/uuid-1", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}
