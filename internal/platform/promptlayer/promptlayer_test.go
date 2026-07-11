package promptlayer

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// --- mock prompt store ---

type mockPromptStore struct {
	prompts   map[string]*prompt.Prompt
	createErr error
	getErr    error
	updateErr error
	deleteErr error
	listErr   error
}

func newMockPromptStore() *mockPromptStore {
	return &mockPromptStore{prompts: make(map[string]*prompt.Prompt)}
}

func (m *mockPromptStore) Create(_ context.Context, p *prompt.Prompt) error {
	if m.createErr != nil {
		return m.createErr
	}
	p.ID = "gen-" + p.Name
	m.prompts[p.Name] = p
	return nil
}

func (m *mockPromptStore) Get(_ context.Context, name string) (*prompt.Prompt, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	p := m.prompts[name]
	return p, nil //nolint:nilnil // interface contract
}

func (m *mockPromptStore) GetPersonal(_ context.Context, ownerEmail, name string) (*prompt.Prompt, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for _, p := range m.prompts {
		if p.Scope == prompt.ScopePersonal && p.OwnerEmail == ownerEmail && p.Name == name {
			return p, nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract
}

func (m *mockPromptStore) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	for _, p := range m.prompts {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract
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

func (m *mockPromptStore) List(_ context.Context, f prompt.ListFilter) ([]prompt.Prompt, error) { //nolint:revive // interface impl
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
		if f.Source != "" && p.Source != f.Source {
			continue
		}
		if f.ExcludeSource != "" && p.Source == f.ExcludeSource {
			continue
		}
		result = append(result, *p)
	}
	return result, nil
}

func (m *mockPromptStore) Count(_ context.Context, _ prompt.ListFilter) (int, error) {
	return len(m.prompts), nil
}

var _ prompt.Store = (*mockPromptStore)(nil)

// --- mock toolkit ---

type mockToolkit struct {
	kind string
	name string
}

func (m *mockToolkit) Kind() string                          { return m.kind }
func (m *mockToolkit) Name() string                          { return m.name }
func (*mockToolkit) Connection() string                      { return "" }
func (*mockToolkit) RegisterTools(_ *mcp.Server)             {}
func (*mockToolkit) Tools() []string                         { return nil }
func (*mockToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (*mockToolkit) SetQueryProvider(_ query.Provider)       {}
func (*mockToolkit) Close() error                            { return nil }

// mockToolkitWithPrompts adds PromptDescriber to mockToolkit.
type mockToolkitWithPrompts struct {
	mockToolkit
	prompts []registry.PromptInfo
}

func (m *mockToolkitWithPrompts) PromptInfos() []registry.PromptInfo {
	return m.prompts
}

// --- stub share lister ---

type stubShareLister struct {
	promptRefs []portal.SharedPromptRef
	err        error
}

func (s *stubShareLister) ListSharedPromptsWithUser(_ context.Context, _, _ string) ([]portal.SharedPromptRef, error) {
	return s.promptRefs, s.err
}

var _ ShareLister = (*stubShareLister)(nil)

// --- context + handle helpers ---

// newTestHandle builds a Handle backed by an in-memory prompt store with the
// admin persona set to "admin". The registry is empty; tests that exercise the
// registration path set their own.
func newTestHandle() (*Handle, *mockPromptStore) {
	store := newMockPromptStore()
	h := &Handle{
		store:        store,
		adminPersona: "admin",
		registry:     registry.NewRegistry(),
	}
	return h, store
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
