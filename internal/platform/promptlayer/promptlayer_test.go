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
	// Mirror the real store: a new prompt with no explicit status lands draft.
	if p.Status == "" {
		p.Status = prompt.StatusDraft
	}
	m.prompts[p.Name] = p
	return nil
}

func (m *mockPromptStore) Get(_ context.Context, name string) (*prompt.Prompt, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	// Mirror the real store: Get resolves only shared (non-personal) prompts;
	// personal prompts are per-owner and served via GetPersonal.
	for _, p := range m.prompts {
		if p.Name == name && p.Scope != prompt.ScopePersonal {
			return clonePrompt(p), nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract
}

func (m *mockPromptStore) GetPersonal(_ context.Context, ownerEmail, name string) (*prompt.Prompt, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for _, p := range m.prompts {
		if p.Scope == prompt.ScopePersonal && p.OwnerEmail == ownerEmail && p.Name == name {
			return clonePrompt(p), nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract
}

func (m *mockPromptStore) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for _, p := range m.prompts {
		if p.ID == id {
			return clonePrompt(p), nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract
}

func (m *mockPromptStore) ListPersonalByName(_ context.Context, name string) ([]prompt.Prompt, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	var out []prompt.Prompt
	for _, p := range m.prompts {
		if p.Scope == prompt.ScopePersonal && p.Name == name {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (m *mockPromptStore) Update(_ context.Context, p *prompt.Prompt) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	// Mirror the real store's updateTx: the row is matched by id and the full
	// row — collection_id included — is written from p, so state the caller
	// did not carry is overwritten.
	if p.ID != "" {
		for key, existing := range m.prompts {
			if existing.ID == p.ID {
				m.prompts[key] = clonePrompt(p)
				return nil
			}
		}
	}
	m.prompts[p.Name] = clonePrompt(p)
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
		if f.Status != "" && p.Status != f.Status {
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

// --- mock collection-capable prompt store ---

// mockCollectionStore layers the collection capability over mockPromptStore,
// modeling the real store's contract: SetPromptCollection is the only
// collection_id writer, refuses an unknown collection with
// ErrCollectionNotFound, and clears the placement on an empty id.
type mockCollectionStore struct {
	*mockPromptStore
	collections map[string]*prompt.Collection
	setErr      error
}

func newMockCollectionStore() *mockCollectionStore {
	return &mockCollectionStore{
		mockPromptStore: newMockPromptStore(),
		collections:     make(map[string]*prompt.Collection),
	}
}

func (m *mockCollectionStore) CreateCollection(_ context.Context, c *prompt.Collection) error {
	m.collections[c.ID] = c
	return nil
}

func (m *mockCollectionStore) GetCollection(_ context.Context, id string) (*prompt.Collection, error) {
	return m.collections[id], nil
}

func (m *mockCollectionStore) ListCollections(_ context.Context) ([]prompt.Collection, error) {
	out := make([]prompt.Collection, 0, len(m.collections))
	for _, c := range m.collections {
		out = append(out, *c)
	}
	return out, nil
}

func (m *mockCollectionStore) UpdateCollection(_ context.Context, id, name, description string) error {
	if c := m.collections[id]; c != nil {
		c.Name, c.Description = name, description
	}
	return nil
}

func (m *mockCollectionStore) DeleteCollection(_ context.Context, id string) error {
	delete(m.collections, id)
	return nil
}

func (m *mockCollectionStore) SetPromptCollection(_ context.Context, promptID, collectionID string) error {
	if m.setErr != nil {
		return m.setErr
	}
	if collectionID != "" && m.collections[collectionID] == nil {
		return prompt.ErrCollectionNotFound
	}
	for _, p := range m.prompts {
		if p.ID == promptID {
			p.CollectionID = collectionID
			return nil
		}
	}
	return nil
}

var _ prompt.CollectionStore = (*mockCollectionStore)(nil)

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

// newTestCollectionHandle builds a Handle whose store also carries the
// collection capability.
func newTestCollectionHandle() (*Handle, *mockCollectionStore) {
	store := newMockCollectionStore()
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
