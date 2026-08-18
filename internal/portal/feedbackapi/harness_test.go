package feedbackapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// The stores below are this package's own controllable doubles. They model the
// contracts in internal/portal/portaldomain, including their not-found shape:
// the PostgreSQL asset and collection stores return an error for a missing row,
// while the prompt store returns (nil, nil).

var errNoRow = errors.New("not found")

type mockAssetStore struct {
	getAsset  *portaldomain.Asset
	getErr    error
	listRes   []portaldomain.Asset
	listTotal int
	listErr   error
}

func (*mockAssetStore) Insert(_ context.Context, _ portaldomain.Asset) error { return nil }

func (m *mockAssetStore) Get(_ context.Context, _ string) (*portaldomain.Asset, error) {
	return m.getAsset, m.getErr
}

func (m *mockAssetStore) GetByIDs(_ context.Context, ids []string) (map[string]*portaldomain.Asset, error) {
	result := make(map[string]*portaldomain.Asset)
	if m.getAsset != nil {
		for _, id := range ids {
			if id == m.getAsset.ID {
				result[id] = m.getAsset
			}
		}
	}
	return result, m.getErr
}

func (*mockAssetStore) GetByIdempotencyKey(_ context.Context, _, _ string) (*portaldomain.Asset, error) {
	return nil, errNoRow
}

func (m *mockAssetStore) List(_ context.Context, _ portaldomain.AssetFilter) ([]portaldomain.Asset, int, error) {
	return m.listRes, m.listTotal, m.listErr
}

func (*mockAssetStore) Update(_ context.Context, _ string, _ portaldomain.AssetUpdate) error {
	return nil
}
func (*mockAssetStore) AppendProvenanceCapture(context.Context, string, portaldomain.ProvenanceCapture) error {
	return nil
}

func (*mockAssetStore) SoftDelete(_ context.Context, _ string) error { return nil }

type mockShareStore struct {
	listByAsset    []portaldomain.Share
	listByAssetE   error
	listByColl     []portaldomain.Share
	listByCollE    error
	sharedWithRes  []portaldomain.SharedAsset
	sharedWithTot  int
	sharedWithErr  error
	sharedCollRes  []portaldomain.SharedCollection
	sharedCollTot  int
	sharedCollErr  error
	collPerm       portaldomain.SharePermission
	collPermErr    error
	collAssetPerm  portaldomain.SharePermission
	collAssetPermE error
	promptRefs     []portaldomain.SharedPromptRef
	promptRefsErr  error
}

func (*mockShareStore) Insert(_ context.Context, _ portaldomain.Share) error { return nil }

func (*mockShareStore) GetByID(_ context.Context, _ string) (*portaldomain.Share, error) {
	return nil, errNoRow
}

func (*mockShareStore) GetByToken(_ context.Context, _ string) (*portaldomain.Share, error) {
	return nil, errNoRow
}

func (m *mockShareStore) ListByAsset(_ context.Context, _ string) ([]portaldomain.Share, error) {
	return m.listByAsset, m.listByAssetE
}

func (m *mockShareStore) ListByCollection(_ context.Context, _ string) ([]portaldomain.Share, error) {
	return m.listByColl, m.listByCollE
}

func (*mockShareStore) ListByPrompt(_ context.Context, _ string) ([]portaldomain.Share, error) {
	return nil, nil
}

func (m *mockShareStore) GetUserCollectionPermission(_ context.Context, _, _, _ string) (portaldomain.SharePermission, error) {
	return m.collPerm, m.collPermErr
}

func (m *mockShareStore) ListSharedWithUser(_ context.Context, _, _ string, _, _ int) ([]portaldomain.SharedAsset, int, error) {
	return m.sharedWithRes, m.sharedWithTot, m.sharedWithErr
}

func (m *mockShareStore) ListSharedCollectionsWithUser(_ context.Context, _, _ string, _, _ int) ([]portaldomain.SharedCollection, int, error) {
	return m.sharedCollRes, m.sharedCollTot, m.sharedCollErr
}

func (*mockShareStore) ListSharedWithUserSince(_ context.Context, _, _ string, _ time.Time, _ int) ([]portaldomain.SharedTargetRef, error) {
	return nil, nil
}

func (m *mockShareStore) ListSharedPromptsWithUser(_ context.Context, _, _ string) ([]portaldomain.SharedPromptRef, error) {
	return m.promptRefs, m.promptRefsErr
}

func (*mockShareStore) ListActiveShareSummaries(_ context.Context, _ []string) (map[string]portaldomain.ShareSummary, error) {
	return map[string]portaldomain.ShareSummary{}, nil
}

func (*mockShareStore) ListActiveCollectionShareSummaries(_ context.Context, _ []string) (map[string]portaldomain.ShareSummary, error) {
	return map[string]portaldomain.ShareSummary{}, nil
}

func (m *mockShareStore) GetUserAssetPermissionViaCollection(_ context.Context, _, _, _ string) (portaldomain.SharePermission, error) {
	if m.collAssetPerm != "" {
		return m.collAssetPerm, nil
	}
	return "", m.collAssetPermE
}

func (*mockShareStore) GetActiveShareForTarget(_ context.Context, _, _, _, _ string) (*portaldomain.Share, error) {
	return nil, nil //nolint:nilnil // test double: no share
}

func (*mockShareStore) Revoke(_ context.Context, _ string) error          { return nil }
func (*mockShareStore) IncrementAccess(_ context.Context, _ string) error { return nil }

type mockCollectionStore struct {
	getResult  *portaldomain.Collection
	getErr     error
	listResult []portaldomain.Collection
	listTotal  int
	listErr    error
}

func (*mockCollectionStore) Insert(_ context.Context, _ portaldomain.Collection) error { return nil }

func (m *mockCollectionStore) Get(_ context.Context, _ string) (*portaldomain.Collection, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.getResult == nil {
		return nil, errNoRow
	}
	return m.getResult, nil
}

func (m *mockCollectionStore) List(_ context.Context, _ portaldomain.CollectionFilter) ([]portaldomain.Collection, int, error) {
	total := m.listTotal
	if total == 0 {
		total = len(m.listResult)
	}
	return m.listResult, total, m.listErr
}

func (*mockCollectionStore) Update(_ context.Context, _, _, _ string) error { return nil }

func (*mockCollectionStore) UpdateConfig(_ context.Context, _ string, _ portaldomain.CollectionConfig) error {
	return nil
}
func (*mockCollectionStore) UpdateThumbnail(_ context.Context, _, _ string) error { return nil }
func (*mockCollectionStore) SoftDelete(_ context.Context, _ string) error         { return nil }

func (*mockCollectionStore) SetSections(_ context.Context, _ string, _ []portaldomain.CollectionSection) error {
	return nil
}

// mockMultiAssetStore resolves a whole batch of ids, which the activity feed
// needs when it labels threads across several assets.
type mockMultiAssetStore struct {
	mockAssetStore
	assets map[string]*portaldomain.Asset
}

func (m *mockMultiAssetStore) GetByIDs(_ context.Context, ids []string) (map[string]*portaldomain.Asset, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	result := make(map[string]*portaldomain.Asset)
	for _, id := range ids {
		if a, ok := m.assets[id]; ok {
			result[id] = a
		}
	}
	return result, nil
}

// mockPromptStore embeds prompt.Store so only the methods this surface calls
// need bodies; anything else panics loudly rather than silently succeeding. A
// missing prompt is (nil, nil), the real store's not-found contract.
type mockPromptStore struct {
	prompt.Store
	prompts map[string]*prompt.Prompt
	byID    *prompt.Prompt
	getErr  error
	listErr error
}

func newMockPromptStore() *mockPromptStore {
	return &mockPromptStore{prompts: make(map[string]*prompt.Prompt)}
}

// GetByID scans by id: the fixtures key the map by prompt name, as the real
// store's callers do, so a lookup by id must not read the map directly.
func (m *mockPromptStore) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.byID != nil {
		return m.byID, nil
	}
	for _, p := range m.prompts {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, nil //nolint:nilnil // prompt.Store contract: nil, nil means not found
}

func (m *mockPromptStore) List(_ context.Context, f prompt.ListFilter) ([]prompt.Prompt, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var out []prompt.Prompt
	for _, p := range m.prompts {
		if f.Scope != "" && p.Scope != f.Scope {
			continue
		}
		if f.OwnerEmail != "" && p.OwnerEmail != f.OwnerEmail {
			continue
		}
		out = append(out, *p)
	}
	return out, nil
}

// mockKnowledgePageStore embeds knowledgepage.Store for the same reason.
type mockKnowledgePageStore struct {
	knowledgepage.Store
	page   *knowledgepage.Page
	getErr error
}

func (m *mockKnowledgePageStore) Get(_ context.Context, _ string) (*knowledgepage.Page, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.page == nil {
		return nil, knowledgepage.ErrNotFound
	}
	return m.page, nil
}

// testAuthMiddleware injects user into every request's context, standing in for
// the portal authenticator the parent wraps the mux with.
func testAuthMiddleware(user *access.User) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user == nil {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(access.ContextWithUser(r.Context(), user)))
		})
	}
}

// newTestServer assembles the seam exactly as the parent does — one Config, one
// checker built from the same stores — and returns a mux that serves its routes
// behind the auth middleware. Tests drive real HTTP requests through it rather
// than calling handler methods, so route registration and the access checks are
// exercised together.
func newTestServer(cfg Config, user *access.User) http.Handler {
	if cfg.Access == nil {
		cfg.Access = access.New(access.Config{
			Assets:      cfg.Assets,
			Collections: cfg.Collections,
			Shares:      cfg.Shares,
			Prompts:     cfg.Prompts,
			AdminRoles:  []string{"admin"},
		})
	}
	mux := http.NewServeMux()
	h := New(cfg)
	h.Register(mux)
	h.RegisterInsightCapture(mux)
	return testAuthMiddleware(user)(mux)
}
