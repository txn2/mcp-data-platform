package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// --- mock prompt store for platform tests ---

type mockPlatformPromptStore struct {
	prompts   map[string]*prompt.Prompt
	createErr error
	getErr    error
	updateErr error
	deleteErr error
	listErr   error
}

func newMockPlatformPromptStore() *mockPlatformPromptStore {
	return &mockPlatformPromptStore{prompts: make(map[string]*prompt.Prompt)}
}

func (m *mockPlatformPromptStore) Create(_ context.Context, p *prompt.Prompt) error {
	if m.createErr != nil {
		return m.createErr
	}
	p.ID = "gen-" + p.Name
	m.prompts[p.Name] = p
	return nil
}

func (m *mockPlatformPromptStore) Get(_ context.Context, name string) (*prompt.Prompt, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	p := m.prompts[name]
	return p, nil //nolint:nilnil // interface contract
}

func (m *mockPlatformPromptStore) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	for _, p := range m.prompts {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract
}

func (m *mockPlatformPromptStore) Update(_ context.Context, p *prompt.Prompt) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.prompts[p.Name] = p
	return nil
}

func (m *mockPlatformPromptStore) Delete(_ context.Context, name string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.prompts, name)
	return nil
}

func (m *mockPlatformPromptStore) DeleteByID(_ context.Context, id string) error {
	for name, p := range m.prompts {
		if p.ID == id {
			delete(m.prompts, name)
			return nil
		}
	}
	return nil
}

func (m *mockPlatformPromptStore) List(_ context.Context, f prompt.ListFilter) ([]prompt.Prompt, error) { //nolint:revive // interface impl
	if m.listErr != nil {
		return nil, m.listErr
	}
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

func (m *mockPlatformPromptStore) Count(_ context.Context, _ prompt.ListFilter) (int, error) {
	return len(m.prompts), nil
}

var _ prompt.Store = (*mockPlatformPromptStore)(nil)

// --- helpers ---

func newTestPlatformWithPromptStore() (*Platform, *mockPlatformPromptStore) {
	store := newMockPlatformPromptStore()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	p := &Platform{
		config:      &Config{Admin: AdminConfig{Persona: "admin"}},
		mcpServer:   srv,
		promptStore: store,
	}
	return p, store
}

func adminCtx() context.Context {
	pc := middleware.NewPlatformContext("")
	pc.PersonaName = "admin"
	pc.UserEmail = "admin@example.com"
	return middleware.WithPlatformContext(context.Background(), pc)
}

func userCtx(email, persona string) context.Context {
	pc := middleware.NewPlatformContext("")
	pc.PersonaName = persona
	pc.UserEmail = email
	return middleware.WithPlatformContext(context.Background(), pc)
}

func resultText(r *mcp.CallToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}

// --- handleManagePrompt dispatch ---

func TestHandleManagePrompt_UnknownCommand(t *testing.T) {
	p, _ := newTestPlatformWithPromptStore()
	r, _, _ := p.handleManagePrompt(context.Background(), managePromptInput{Command: "bogus"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "unknown command")
}

// --- handlePromptCreate ---

func TestHandlePromptCreate_Success(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	r, _, _ := p.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "my-prompt", Content: "hello {topic}", Scope: "global",
	})
	assert.False(t, r.IsError)
	assert.Contains(t, store.prompts, "my-prompt")
	assert.Equal(t, "global", store.prompts["my-prompt"].Scope)
}

func TestHandlePromptCreate_InvalidName(t *testing.T) {
	p, _ := newTestPlatformWithPromptStore()
	r, _, _ := p.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "INVALID NAME!", Content: "content",
	})
	assert.True(t, r.IsError)
}

func TestHandlePromptCreate_MissingContent(t *testing.T) {
	p, _ := newTestPlatformWithPromptStore()
	r, _, _ := p.handlePromptCreate(adminCtx(), managePromptInput{Name: "test"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "content is required")
}

func TestHandlePromptCreate_InvalidScope(t *testing.T) {
	p, _ := newTestPlatformWithPromptStore()
	r, _, _ := p.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "test", Content: "c", Scope: "invalid",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "invalid scope")
}

func TestHandlePromptCreate_NonAdminDeniedGlobalScope(t *testing.T) {
	p, _ := newTestPlatformWithPromptStore()
	r, _, _ := p.handlePromptCreate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "test", Content: "c", Scope: "global",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "only admins")
}

func TestHandlePromptCreate_NonAdminPersonalOK(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	r, _, _ := p.handlePromptCreate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "my-personal", Content: "content",
	})
	assert.False(t, r.IsError)
	assert.Equal(t, prompt.ScopePersonal, store.prompts["my-personal"].Scope)
	assert.Equal(t, "user@example.com", store.prompts["my-personal"].OwnerEmail)
}

func TestHandlePromptCreate_NilPersonasDefaultsToEmpty(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	r, _, _ := p.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "no-personas", Content: "content", Scope: "personal",
		// Personas intentionally omitted (nil)
	})
	assert.False(t, r.IsError)
	assert.Equal(t, []string{}, store.prompts["no-personas"].Personas)
}

func TestHandlePromptCreate_StoreErrorDoesNotLeakDetails(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.createErr = fmt.Errorf("pq: null value in column \"personas\" violates not-null constraint (23502)")
	r, _, _ := p.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "test", Content: "content",
	})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to create prompt")
	assert.NotContains(t, text, "pq:")
	assert.NotContains(t, text, "23502")
}

func TestHandlePromptCreate_StoreError(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.createErr = fmt.Errorf("db down")
	r, _, _ := p.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "test", Content: "content",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "failed to create")
}

// --- handlePromptUpdate ---

func TestHandlePromptUpdate_Success(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["old"] = &prompt.Prompt{
		ID: "id-1", Name: "old", Content: "old-content",
		Scope: prompt.ScopePersonal, OwnerEmail: "user@example.com", Enabled: true,
	}
	r, _, _ := p.handlePromptUpdate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "old", Content: "new-content",
	})
	assert.False(t, r.IsError)
	assert.Equal(t, "new-content", store.prompts["old"].Content)
}

func TestHandlePromptUpdate_NotFound(t *testing.T) {
	p, _ := newTestPlatformWithPromptStore()
	r, _, _ := p.handlePromptUpdate(adminCtx(), managePromptInput{Name: "missing"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "not found")
}

func TestHandlePromptUpdate_NonAdminDeniedNonPersonal(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["global"] = &prompt.Prompt{
		ID: "id-1", Name: "global", Scope: prompt.ScopeGlobal,
	}
	r, _, _ := p.handlePromptUpdate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "global", Content: "hacked",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "non-admins")
}

func TestHandlePromptUpdate_NonAdminDeniedOtherUser(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["other"] = &prompt.Prompt{
		ID: "id-1", Name: "other", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com",
	}
	r, _, _ := p.handlePromptUpdate(userCtx("alice@example.com", "analyst"), managePromptInput{
		Name: "other", Content: "hacked",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "your own")
}

func TestHandlePromptUpdate_ScopeChangeByNonAdmin(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["mine"] = &prompt.Prompt{
		ID: "id-1", Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "user@example.com",
	}
	r, _, _ := p.handlePromptUpdate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "mine", Scope: "global",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "only admins")
}

func TestHandlePromptUpdate_StoreGetError(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.getErr = fmt.Errorf("pq: connection refused")
	r, _, _ := p.handlePromptUpdate(adminCtx(), managePromptInput{Name: "test", Content: "c"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to get prompt")
	assert.NotContains(t, text, "pq:")
}

func TestHandlePromptUpdate_StoreUpdateError(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["test"] = &prompt.Prompt{
		ID: "id-1", Name: "test", Scope: prompt.ScopeGlobal,
	}
	store.updateErr = fmt.Errorf("pq: disk full")
	r, _, _ := p.handlePromptUpdate(adminCtx(), managePromptInput{Name: "test", Content: "c"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to update prompt")
	assert.NotContains(t, text, "pq:")
}

// --- handlePromptDelete ---

func TestHandlePromptDelete_Success(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["del"] = &prompt.Prompt{
		ID: "id-1", Name: "del", Scope: prompt.ScopePersonal, OwnerEmail: "user@example.com",
	}
	r, _, _ := p.handlePromptDelete(userCtx("user@example.com", "analyst"), managePromptInput{Name: "del"})
	assert.False(t, r.IsError)
	assert.NotContains(t, store.prompts, "del")
}

func TestHandlePromptDelete_NotFound(t *testing.T) {
	p, _ := newTestPlatformWithPromptStore()
	r, _, _ := p.handlePromptDelete(adminCtx(), managePromptInput{Name: "missing"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "not found")
}

func TestHandlePromptDelete_NonAdminDeniedNonPersonal(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["global"] = &prompt.Prompt{
		ID: "id-1", Name: "global", Scope: prompt.ScopeGlobal,
	}
	r, _, _ := p.handlePromptDelete(userCtx("user@example.com", "analyst"), managePromptInput{Name: "global"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "non-admins")
}

func TestHandlePromptDelete_StoreGetError(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.getErr = fmt.Errorf("pq: timeout")
	r, _, _ := p.handlePromptDelete(adminCtx(), managePromptInput{Name: "test"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to get prompt")
	assert.NotContains(t, text, "pq:")
}

func TestHandlePromptDelete_StoreDeleteError(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["test"] = &prompt.Prompt{
		ID: "id-1", Name: "test", Scope: prompt.ScopeGlobal,
	}
	store.deleteErr = fmt.Errorf("pq: constraint violation")
	r, _, _ := p.handlePromptDelete(adminCtx(), managePromptInput{Name: "test"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to delete prompt")
	assert.NotContains(t, text, "pq:")
}

// --- handlePromptList ---

func TestHandlePromptList_Admin(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["a"] = &prompt.Prompt{ID: "1", Name: "a", Scope: prompt.ScopeGlobal, Enabled: true}
	store.prompts["b"] = &prompt.Prompt{ID: "2", Name: "b", Scope: prompt.ScopePersonal, Enabled: true, OwnerEmail: "u@x.com"}
	r, _, _ := p.handlePromptList(adminCtx(), managePromptInput{Command: "list"})
	assert.False(t, r.IsError)

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &resp))
	countVal, _ := resp["count"].(float64)
	count := int(countVal)
	assert.Equal(t, 2, count)
}

func TestHandlePromptList_NonAdminNoScope(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["personal"] = &prompt.Prompt{
		ID: "1", Name: "personal", Scope: prompt.ScopePersonal, Enabled: true, OwnerEmail: "user@example.com",
	}
	store.prompts["global"] = &prompt.Prompt{
		ID: "2", Name: "global", Scope: prompt.ScopeGlobal, Enabled: true,
	}
	r, _, _ := p.handlePromptList(userCtx("user@example.com", "analyst"), managePromptInput{Command: "list"})
	assert.False(t, r.IsError)

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &resp))
	countVal, _ := resp["count"].(float64)
	count := int(countVal)
	assert.Equal(t, 2, count) // personal + global
}

func TestHandlePromptList_NonAdminWithScope(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["g1"] = &prompt.Prompt{ID: "1", Name: "g1", Scope: prompt.ScopeGlobal, Enabled: true}
	store.prompts["p1"] = &prompt.Prompt{ID: "2", Name: "p1", Scope: prompt.ScopePersonal, Enabled: true, OwnerEmail: "user@example.com"}
	r, _, _ := p.handlePromptList(userCtx("user@example.com", "analyst"), managePromptInput{
		Command: "list", Scope: "global",
	})
	assert.False(t, r.IsError)

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &resp))
	countVal, _ := resp["count"].(float64)
	count := int(countVal)
	assert.Equal(t, 1, count) // only global
}

func TestHandlePromptList_StoreError(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.listErr = fmt.Errorf("pq: too many connections")
	r, _, _ := p.handlePromptList(adminCtx(), managePromptInput{Command: "list"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to list prompts")
	assert.NotContains(t, text, "pq:")
}

// --- handlePromptGet ---

func TestHandlePromptGet_Found(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["test"] = &prompt.Prompt{
		ID: "id-1", Name: "test", Content: "content", Scope: prompt.ScopeGlobal,
	}
	r, _, _ := p.handlePromptGet(adminCtx(), managePromptInput{Name: "test"})
	assert.False(t, r.IsError)
	assert.Contains(t, resultText(r), "test")
}

func TestHandlePromptGet_NotFound(t *testing.T) {
	p, _ := newTestPlatformWithPromptStore()
	r, _, _ := p.handlePromptGet(adminCtx(), managePromptInput{Name: "missing"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "not found")
}

func TestHandlePromptGet_NonAdminDeniedOtherPersonal(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["secret"] = &prompt.Prompt{
		ID: "id-1", Name: "secret", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com",
	}
	r, _, _ := p.handlePromptGet(userCtx("alice@example.com", "engineer"), managePromptInput{Name: "secret"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "your own")
}

func TestHandlePromptGet_StoreError(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.getErr = fmt.Errorf("pq: connection reset")
	r, _, _ := p.handlePromptGet(adminCtx(), managePromptInput{Name: "test"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to get prompt")
	assert.NotContains(t, text, "pq:")
}

// --- applyPromptUpdates ---

func TestApplyPromptUpdates(t *testing.T) {
	existing := &prompt.Prompt{Name: "test", Scope: prompt.ScopePersonal}
	msg := applyPromptUpdates(existing, managePromptInput{
		DisplayName: "New Display",
		Description: "New Desc",
		Content:     "New Content",
		Category:    "cat",
		Arguments:   []prompt.Argument{{Name: "a"}},
		Personas:    []string{"analyst"},
	}, true)
	assert.Empty(t, msg)
	assert.Equal(t, "New Display", existing.DisplayName)
	assert.Equal(t, "New Desc", existing.Description)
	assert.Equal(t, "New Content", existing.Content)
	assert.Equal(t, "cat", existing.Category)
	assert.Len(t, existing.Arguments, 1)
	assert.Equal(t, []string{"analyst"}, existing.Personas)
}

// --- schema and helpers ---

func TestManagePromptSchema(t *testing.T) {
	schema := managePromptSchema()
	assert.NotNil(t, schema)

	m, ok := schema.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "object", m["type"])

	props, ok := m["properties"].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, props, "command")
	assert.Contains(t, props, "name")
	assert.Contains(t, props, "content")
	assert.Contains(t, props, "scope")
	assert.Contains(t, props, "personas")
	assert.Contains(t, props, "search")

	required, ok := m["required"].([]string)
	assert.True(t, ok)
	assert.Contains(t, required, "command")
}

func TestPromptErrorResult(t *testing.T) {
	result := promptErrorResult("something went wrong")
	assert.True(t, result.IsError)
	assert.Len(t, result.Content, 1)
}

func TestPromptJSONResult(t *testing.T) {
	result, meta, err := promptJSONResult(map[string]string{"status": "ok"})
	assert.NoError(t, err)
	assert.Nil(t, meta)
	assert.False(t, result.IsError)
	assert.Len(t, result.Content, 1)
}

func TestResolveEmail_Anonymous(t *testing.T) {
	email := resolveEmail(t.Context())
	assert.Equal(t, "anonymous", email)
}

func TestResolveEmail_FromContext(t *testing.T) {
	email := resolveEmail(userCtx("alice@example.com", "analyst"))
	assert.Equal(t, "alice@example.com", email)
}

func TestIsAdminPersona_NoContext(t *testing.T) {
	p := &Platform{config: &Config{Admin: AdminConfig{Persona: "admin"}}}
	assert.False(t, p.isAdminPersona(t.Context()))
}

func TestIsAdminPersona_AdminContext(t *testing.T) {
	p := &Platform{config: &Config{Admin: AdminConfig{Persona: "admin"}}}
	assert.True(t, p.isAdminPersona(adminCtx()))
}

func TestIsBuiltinDisabled(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]bool
		prompt   string
		expected bool
	}{
		{"nil map", nil, "explore-available-data", false},
		{"not in map", map[string]bool{}, "explore-available-data", false},
		{"enabled", map[string]bool{"explore-available-data": true}, "explore-available-data", false},
		{"disabled", map[string]bool{"explore-available-data": false}, "explore-available-data", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Platform{config: &Config{Server: ServerConfig{BuiltinPrompts: tt.config}}}
			assert.Equal(t, tt.expected, p.isBuiltinDisabled(tt.prompt))
		})
	}
}
