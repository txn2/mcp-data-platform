package access

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// errStore is the failure a store returns when the query itself fails, as
// opposed to naming no row.
var errStore = errors.New("store unavailable")

// errNotFound is what the PostgreSQL asset and collection stores return for a
// missing row: a wrapped scan error, not (nil, nil). The prompt store is the
// exception and returns (nil, nil) — fakePromptStore models that separately.
var errNotFound = errors.New("querying asset: sql: no rows in result set")

type fakeAssetStore struct {
	portaldomain.AssetStore
	assets map[string]*portaldomain.Asset
	err    error
}

func (f *fakeAssetStore) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if f.err != nil {
		return nil, f.err
	}
	a, ok := f.assets[id]
	if !ok {
		return nil, errNotFound
	}
	return a, nil
}

func (f *fakeAssetStore) GetByIDs(_ context.Context, ids []string) (map[string]*portaldomain.Asset, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]*portaldomain.Asset, len(ids))
	for _, id := range ids {
		if a, ok := f.assets[id]; ok {
			out[id] = a
		}
	}
	return out, nil
}

type fakeCollectionStore struct {
	portaldomain.CollectionStore
	colls map[string]*portaldomain.Collection
}

func (f *fakeCollectionStore) Get(_ context.Context, id string) (*portaldomain.Collection, error) {
	c, ok := f.colls[id]
	if !ok {
		return nil, errNotFound
	}
	return c, nil
}

type fakeShareStore struct {
	portaldomain.ShareStore
	byAsset          map[string][]portaldomain.Share
	byAssetErr       error
	viaCollection    map[string]portaldomain.SharePermission
	collectionPerm   map[string]portaldomain.SharePermission
	collectionErr    error
	sharedPrompts    []portaldomain.SharedPromptRef
	sharedPromptsErr error
}

func (f *fakeShareStore) ListByAsset(_ context.Context, assetID string) ([]portaldomain.Share, error) {
	if f.byAssetErr != nil {
		return nil, f.byAssetErr
	}
	return f.byAsset[assetID], nil
}

func (f *fakeShareStore) GetUserAssetPermissionViaCollection(_ context.Context, assetID, _, _ string) (portaldomain.SharePermission, error) {
	return f.viaCollection[assetID], nil
}

func (f *fakeShareStore) GetUserCollectionPermission(_ context.Context, collectionID, _, _ string) (portaldomain.SharePermission, error) {
	if f.collectionErr != nil {
		return "", f.collectionErr
	}
	return f.collectionPerm[collectionID], nil
}

func (f *fakeShareStore) ListSharedPromptsWithUser(_ context.Context, _, _ string) ([]portaldomain.SharedPromptRef, error) {
	if f.sharedPromptsErr != nil {
		return nil, f.sharedPromptsErr
	}
	return f.sharedPrompts, nil
}

type fakePromptStore struct {
	prompt.Store
	prompts map[string]*prompt.Prompt
	err     error
}

// GetByID models the prompt store's contract: a missing prompt is (nil, nil).
func (f *fakePromptStore) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.prompts[id], nil
}

func owner() *User {
	return &User{UserID: "u-owner", Email: "owner@example.com"}
}

func viewer() *User {
	return &User{UserID: "u-view", Email: "view@example.com"}
}

func admin() *User {
	return &User{UserID: "u-admin", Email: "admin@example.com", Roles: []string{"dp_admin"}}
}

func share(perm portaldomain.SharePermission, userID, email string) portaldomain.Share {
	return portaldomain.Share{Permission: perm, SharedWithUserID: userID, SharedWithEmail: email}
}

func TestHasAnyRole(t *testing.T) {
	tests := []struct {
		name        string
		userRoles   []string
		targetRoles []string
		want        bool
	}{
		{"match", []string{"dp_admin", "dp_analyst"}, []string{"dp_admin"}, true},
		{"no match", []string{"dp_analyst"}, []string{"dp_admin"}, false},
		{"empty user roles", nil, []string{"dp_admin"}, false},
		{"empty target roles", []string{"dp_admin"}, nil, false},
		{"both empty", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, HasAnyRole(tt.userRoles, tt.targetRoles))
		})
	}
}

func TestIsShareActive(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	assert.True(t, IsShareActive(portaldomain.Share{Revoked: false}))
	assert.False(t, IsShareActive(portaldomain.Share{Revoked: true}))
	assert.False(t, IsShareActive(portaldomain.Share{Revoked: false, ExpiresAt: &past}))
	assert.True(t, IsShareActive(portaldomain.Share{Revoked: false, ExpiresAt: &future}))
}

func TestIsAdmin(t *testing.T) {
	c := New(Config{AdminRoles: []string{"dp_admin"}})
	assert.False(t, c.IsAdmin(nil), "a nil caller is never admin")
	assert.False(t, c.IsAdmin(viewer()))
	assert.True(t, c.IsAdmin(admin()))
}

// TestHasTool pins the capability check across every caller state: no resolver,
// a resolver that grants, a resolver that does not, and the admin arm that
// widens access when the tool is not registered at all.
func TestHasTool(t *testing.T) {
	tests := []struct {
		name    string
		user    *User
		resolve func(roles []string) []string
		want    bool
	}{
		{"nil user", nil, func([]string) []string { return []string{"apply_knowledge"} }, false},
		{"no resolver, no admin role", viewer(), nil, false},
		{"no resolver, admin role", admin(), nil, true},
		{"persona grants", viewer(), func([]string) []string { return []string{"apply_knowledge"} }, true},
		{"persona denies, not admin", viewer(), func([]string) []string { return []string{"search"} }, false},
		{"persona denies, admin", admin(), func([]string) []string { return []string{"search"} }, true},
		{"resolver returns nothing", viewer(), func([]string) []string { return nil }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(Config{AdminRoles: []string{"dp_admin"}, PersonaTools: tt.resolve})
			assert.Equal(t, tt.want, c.HasTool(tt.user, ApplyKnowledgeTool))
			assert.Equal(t, tt.want, c.HasApplyKnowledge(tt.user))
		})
	}
}

func TestAssetSharePermission(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	tests := []struct {
		name   string
		shares []portaldomain.Share
		want   portaldomain.SharePermission
	}{
		{"no shares", nil, ""},
		{"share for someone else", []portaldomain.Share{share(portaldomain.PermissionEditor, "u-other", "")}, ""},
		{"viewer share by user id", []portaldomain.Share{share(portaldomain.PermissionViewer, "u-view", "")}, portaldomain.PermissionViewer},
		{"viewer share by email, case-insensitive", []portaldomain.Share{share(portaldomain.PermissionViewer, "", "VIEW@example.com")}, portaldomain.PermissionViewer},
		{"editor wins over viewer", []portaldomain.Share{
			share(portaldomain.PermissionViewer, "u-view", ""),
			share(portaldomain.PermissionEditor, "u-view", ""),
		}, portaldomain.PermissionEditor},
		{"revoked share does not grant", []portaldomain.Share{
			{Permission: portaldomain.PermissionEditor, SharedWithUserID: "u-view", Revoked: true},
		}, ""},
		{"expired share does not grant", []portaldomain.Share{
			{Permission: portaldomain.PermissionEditor, SharedWithUserID: "u-view", ExpiresAt: &past},
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(Config{Shares: &fakeShareStore{byAsset: map[string][]portaldomain.Share{"a1": tt.shares}}})
			got, err := c.AssetSharePermission(context.Background(), "a1", viewer())
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("store error surfaces", func(t *testing.T) {
		c := New(Config{Shares: &fakeShareStore{byAssetErr: errStore}})
		_, err := c.AssetSharePermission(context.Background(), "a1", viewer())
		require.Error(t, err)
	})
}

// TestResolveAssetPermission covers the cascade: the highest of the direct and
// the collection grant wins, and a direct editor short-circuits the collection
// lookup because editor is the ceiling.
func TestResolveAssetPermission(t *testing.T) {
	tests := []struct {
		name       string
		direct     []portaldomain.Share
		collection portaldomain.SharePermission
		want       portaldomain.SharePermission
	}{
		{"neither", nil, "", ""},
		{"direct viewer only", []portaldomain.Share{share(portaldomain.PermissionViewer, "u-view", "")}, "", portaldomain.PermissionViewer},
		{"collection viewer only", nil, portaldomain.PermissionViewer, portaldomain.PermissionViewer},
		{"collection editor beats direct viewer", []portaldomain.Share{share(portaldomain.PermissionViewer, "u-view", "")}, portaldomain.PermissionEditor, portaldomain.PermissionEditor},
		{"direct editor is the ceiling", []portaldomain.Share{share(portaldomain.PermissionEditor, "u-view", "")}, portaldomain.PermissionViewer, portaldomain.PermissionEditor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(Config{Shares: &fakeShareStore{
				byAsset:       map[string][]portaldomain.Share{"a1": tt.direct},
				viaCollection: map[string]portaldomain.SharePermission{"a1": tt.collection},
			}})
			got, err := c.ResolveAssetPermission(context.Background(), "a1", viewer())
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	// A direct-share failure is reported, but a collection grant still wins the
	// value: the caller decides whether the error is fatal.
	t.Run("direct error with collection grant", func(t *testing.T) {
		c := New(Config{Shares: &fakeShareStore{
			byAssetErr:    errStore,
			viaCollection: map[string]portaldomain.SharePermission{"a1": portaldomain.PermissionEditor},
		}})
		got, err := c.ResolveAssetPermission(context.Background(), "a1", viewer())
		require.Error(t, err)
		assert.Equal(t, portaldomain.PermissionEditor, got)
	})
}

func TestCanViewAssetAndViewGrant(t *testing.T) {
	assetOwned := &portaldomain.Asset{ID: "a1", OwnerID: "u-owner"}
	tests := []struct {
		name         string
		user         *User
		direct       []portaldomain.Share
		collection   portaldomain.SharePermission
		directErr    error
		want         bool
		wantGrantErr bool
	}{
		{name: "owner", user: owner(), want: true},
		{name: "no grant", user: viewer(), want: false},
		{name: "direct viewer", user: viewer(), direct: []portaldomain.Share{share(portaldomain.PermissionViewer, "u-view", "")}, want: true},
		{name: "collection grant only", user: viewer(), collection: portaldomain.PermissionViewer, want: true},
		// The silent form tolerates a direct-share failure and falls through to
		// the collection grant; the reporting form surfaces the failure instead.
		{name: "direct error, collection grants", user: viewer(), directErr: errStore, collection: portaldomain.PermissionViewer, want: true, wantGrantErr: true},
		{name: "direct error, no collection grant", user: viewer(), directErr: errStore, want: false, wantGrantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(Config{Shares: &fakeShareStore{
				byAsset:       map[string][]portaldomain.Share{"a1": tt.direct},
				byAssetErr:    tt.directErr,
				viaCollection: map[string]portaldomain.SharePermission{"a1": tt.collection},
			}})
			assert.Equal(t, tt.want, c.CanViewAsset(context.Background(), "a1", assetOwned, tt.user))

			granted, err := c.AssetViewGrant(context.Background(), "a1", assetOwned, tt.user)
			if tt.wantGrantErr {
				require.Error(t, err, "a lookup failure must be reportable, not silently a denial")
				assert.False(t, granted)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, granted)
		})
	}
}

func TestCanEditAssetSilent(t *testing.T) {
	deleted := time.Now()
	tests := []struct {
		name   string
		assets map[string]*portaldomain.Asset
		user   *User
		direct []portaldomain.Share
		want   bool
	}{
		{"missing asset", nil, owner(), nil, false},
		{"owner", map[string]*portaldomain.Asset{"a1": {ID: "a1", OwnerID: "u-owner"}}, owner(), nil, true},
		{"soft-deleted asset denies the owner", map[string]*portaldomain.Asset{"a1": {ID: "a1", OwnerID: "u-owner", DeletedAt: &deleted}}, owner(), nil, false},
		{"viewer share is not enough", map[string]*portaldomain.Asset{"a1": {ID: "a1", OwnerID: "u-owner"}}, viewer(), []portaldomain.Share{share(portaldomain.PermissionViewer, "u-view", "")}, false},
		{"editor share", map[string]*portaldomain.Asset{"a1": {ID: "a1", OwnerID: "u-owner"}}, viewer(), []portaldomain.Share{share(portaldomain.PermissionEditor, "u-view", "")}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(Config{
				Assets: &fakeAssetStore{assets: tt.assets},
				Shares: &fakeShareStore{byAsset: map[string][]portaldomain.Share{"a1": tt.direct}},
			})
			assert.Equal(t, tt.want, c.CanEditAssetSilent(context.Background(), "a1", tt.user))
		})
	}
}

func TestCollectionAccess(t *testing.T) {
	deleted := time.Now()
	colls := map[string]*portaldomain.Collection{
		"c1": {ID: "c1", OwnerID: "u-owner"},
		"c2": {ID: "c2", OwnerID: "u-owner", DeletedAt: &deleted},
	}

	t.Run("share permission tolerates a store error", func(t *testing.T) {
		c := New(Config{Shares: &fakeShareStore{collectionErr: errStore}})
		assert.Empty(t, c.CollectionSharePermission(context.Background(), "c1", viewer()))
	})

	viewTests := []struct {
		name string
		user *User
		perm portaldomain.SharePermission
		want bool
	}{
		{"owner", owner(), "", true},
		{"no share", viewer(), "", false},
		{"viewer share", viewer(), portaldomain.PermissionViewer, true},
		{"editor share", viewer(), portaldomain.PermissionEditor, true},
	}
	for _, tt := range viewTests {
		t.Run("view/"+tt.name, func(t *testing.T) {
			c := New(Config{Shares: &fakeShareStore{collectionPerm: map[string]portaldomain.SharePermission{"c1": tt.perm}}})
			assert.Equal(t, tt.want, c.CanViewCollection(context.Background(), colls["c1"], tt.user))
		})
	}

	editTests := []struct {
		name  string
		store portaldomain.CollectionStore
		id    string
		user  *User
		perm  portaldomain.SharePermission
		want  bool
	}{
		{name: "no collection store", store: nil, id: "c1", user: owner(), want: false},
		{name: "owner", store: &fakeCollectionStore{colls: colls}, id: "c1", user: owner(), want: true},
		{name: "soft-deleted denies the owner", store: &fakeCollectionStore{colls: colls}, id: "c2", user: owner(), want: false},
		{name: "missing collection", store: &fakeCollectionStore{colls: colls}, id: "nope", user: owner(), want: false},
		{name: "viewer share is not enough", store: &fakeCollectionStore{colls: colls}, id: "c1", user: viewer(), perm: portaldomain.PermissionViewer, want: false},
		{name: "editor share", store: &fakeCollectionStore{colls: colls}, id: "c1", user: viewer(), perm: portaldomain.PermissionEditor, want: true},
	}
	for _, tt := range editTests {
		t.Run("edit/"+tt.name, func(t *testing.T) {
			c := New(Config{
				Collections: tt.store,
				Shares:      &fakeShareStore{collectionPerm: map[string]portaldomain.SharePermission{tt.id: tt.perm}},
			})
			assert.Equal(t, tt.want, c.CanEditCollectionSilent(context.Background(), tt.id, tt.user))
		})
	}
}

func TestCanViewPrompt(t *testing.T) {
	personal := &prompt.Prompt{ID: "p1", Scope: prompt.ScopePersonal, OwnerEmail: "owner@example.com"}
	global := &prompt.Prompt{ID: "p2", Scope: prompt.ScopeGlobal}

	tests := []struct {
		name   string
		pr     *prompt.Prompt
		user   *User
		shared []portaldomain.SharedPromptRef
		shrErr error
		want   bool
	}{
		{name: "global prompt is visible to anyone", pr: global, user: viewer(), want: true},
		{name: "personal prompt, owner by email (case-insensitive)", pr: personal, user: &User{Email: "OWNER@example.com"}, want: true},
		{name: "personal prompt, stranger", pr: personal, user: viewer(), want: false},
		{name: "personal prompt, admin", pr: personal, user: admin(), want: true},
		{name: "personal prompt, share grantee", pr: personal, user: viewer(), shared: []portaldomain.SharedPromptRef{{PromptID: "p1"}}, want: true},
		{name: "personal prompt, share for another prompt", pr: personal, user: viewer(), shared: []portaldomain.SharedPromptRef{{PromptID: "other"}}, want: false},
		{name: "personal prompt, share lookup fails", pr: personal, user: viewer(), shrErr: errStore, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(Config{
				AdminRoles: []string{"dp_admin"},
				Shares:     &fakeShareStore{sharedPrompts: tt.shared, sharedPromptsErr: tt.shrErr},
			})
			assert.Equal(t, tt.want, c.CanViewPrompt(context.Background(), tt.user, tt.pr))
		})
	}
}

func TestOwnsPersonalPrompt(t *testing.T) {
	prompts := map[string]*prompt.Prompt{
		"p1": {ID: "p1", Scope: prompt.ScopePersonal, OwnerEmail: "owner@example.com"},
		"p2": {ID: "p2", Scope: prompt.ScopeGlobal, OwnerEmail: "owner@example.com"},
		"p3": {ID: "p3", Scope: prompt.ScopePersonal},
	}
	tests := []struct {
		name  string
		store prompt.Store
		id    string
		user  *User
		want  bool
	}{
		{"no prompt store", nil, "p1", owner(), false},
		{"owner of a personal prompt", &fakePromptStore{prompts: prompts}, "p1", owner(), true},
		{"a global prompt is never personally owned", &fakePromptStore{prompts: prompts}, "p2", owner(), false},
		{"a prompt with no owner email", &fakePromptStore{prompts: prompts}, "p3", owner(), false},
		{"another user", &fakePromptStore{prompts: prompts}, "p1", viewer(), false},
		{"missing prompt is (nil, nil), not a panic", &fakePromptStore{prompts: prompts}, "nope", owner(), false},
		{"store error", &fakePromptStore{err: errStore}, "p1", owner(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(Config{Prompts: tt.store})
			assert.Equal(t, tt.want, c.OwnsPersonalPrompt(context.Background(), tt.id, tt.user))
		})
	}
}

func TestOwnedTargetIDs(t *testing.T) {
	deleted := time.Now()
	assets := &fakeAssetStore{assets: map[string]*portaldomain.Asset{
		"a1": {ID: "a1", OwnerID: "u-owner"},
		"a2": {ID: "a2", OwnerID: "u-other"},
		"a3": {ID: "a3", OwnerID: "u-owner", DeletedAt: &deleted},
	}}
	colls := &fakeCollectionStore{colls: map[string]*portaldomain.Collection{
		"c1": {ID: "c1", OwnerID: "u-owner"},
		"c2": {ID: "c2", OwnerID: "u-other"},
		"c3": {ID: "c3", OwnerID: "u-owner", DeletedAt: &deleted},
	}}
	c := New(Config{Assets: assets, Collections: colls})
	ctx := context.Background()

	assert.Equal(t, []string{"a1"}, c.OwnedTargetIDs(ctx, portaldomain.TargetTypeAsset, []string{"a1", "a2", "a3", "missing"}, owner()))
	assert.Equal(t, []string{"c1"}, c.OwnedTargetIDs(ctx, portaldomain.TargetTypeCollection, []string{"c1", "c2", "c3", "missing"}, owner()))
	assert.Nil(t, c.OwnedTargetIDs(ctx, portaldomain.TargetTypePrompt, []string{"p1"}, owner()),
		"an unsupported target type owns nothing")

	empty := New(Config{})
	assert.Nil(t, empty.OwnedTargetIDs(ctx, portaldomain.TargetTypeAsset, []string{"a1"}, owner()))
	assert.Nil(t, empty.OwnedTargetIDs(ctx, portaldomain.TargetTypeCollection, []string{"c1"}, owner()))

	failing := New(Config{Assets: &fakeAssetStore{err: errStore}})
	assert.Nil(t, failing.OwnedTargetIDs(ctx, portaldomain.TargetTypeAsset, []string{"a1"}, owner()))
}

// TestCanModerateThread walks every target kind against every caller state.
// Moderation is the destructive arm of the feedback surface (status change and
// delete), so each row states who may and who may not.
func TestCanModerateThread(t *testing.T) {
	assets := &fakeAssetStore{assets: map[string]*portaldomain.Asset{"a1": {ID: "a1", OwnerID: "u-owner"}}}
	colls := &fakeCollectionStore{colls: map[string]*portaldomain.Collection{"c1": {ID: "c1", OwnerID: "u-owner"}}}
	prompts := &fakePromptStore{prompts: map[string]*prompt.Prompt{
		"p1": {ID: "p1", Scope: prompt.ScopePersonal, OwnerEmail: "owner@example.com"},
	}}

	tests := []struct {
		name        string
		thread      *threads.Thread
		user        *User
		assetShares []portaldomain.Share
		collPerm    portaldomain.SharePermission
		personaTool bool
		want        bool
	}{
		{name: "author of a standalone thread", thread: &threads.Thread{TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u-view"}, user: viewer(), want: true},
		{name: "stranger on a standalone thread", thread: &threads.Thread{TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u-other"}, user: viewer(), want: false},
		{name: "admin on a standalone thread", thread: &threads.Thread{TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u-other"}, user: admin(), want: true},

		{name: "asset owner", thread: &threads.Thread{TargetType: portaldomain.TargetTypeAsset, AssetID: "a1", AuthorID: "u-other"}, user: owner(), want: true},
		{name: "asset editor", thread: &threads.Thread{TargetType: portaldomain.TargetTypeAsset, AssetID: "a1", AuthorID: "u-other"}, user: viewer(), assetShares: []portaldomain.Share{share(portaldomain.PermissionEditor, "u-view", "")}, want: true},
		{name: "asset viewer may not moderate", thread: &threads.Thread{TargetType: portaldomain.TargetTypeAsset, AssetID: "a1", AuthorID: "u-other"}, user: viewer(), assetShares: []portaldomain.Share{share(portaldomain.PermissionViewer, "u-view", "")}, want: false},

		{name: "collection owner", thread: &threads.Thread{TargetType: portaldomain.TargetTypeCollection, CollectionID: "c1", AuthorID: "u-other"}, user: owner(), want: true},
		{name: "collection editor", thread: &threads.Thread{TargetType: portaldomain.TargetTypeCollection, CollectionID: "c1", AuthorID: "u-other"}, user: viewer(), collPerm: portaldomain.PermissionEditor, want: true},
		{name: "collection viewer may not moderate", thread: &threads.Thread{TargetType: portaldomain.TargetTypeCollection, CollectionID: "c1", AuthorID: "u-other"}, user: viewer(), collPerm: portaldomain.PermissionViewer, want: false},

		{name: "personal prompt owner", thread: &threads.Thread{TargetType: portaldomain.TargetTypePrompt, PromptID: "p1", AuthorID: "u-other"}, user: owner(), want: true},
		{name: "prompt stranger", thread: &threads.Thread{TargetType: portaldomain.TargetTypePrompt, PromptID: "p1", AuthorID: "u-other"}, user: viewer(), want: false},

		{name: "knowledge page needs apply_knowledge", thread: &threads.Thread{TargetType: portaldomain.TargetTypeKnowledgePage, KnowledgePageID: "k1", AuthorID: "u-other"}, user: viewer(), want: false},
		{name: "knowledge page with apply_knowledge", thread: &threads.Thread{TargetType: portaldomain.TargetTypeKnowledgePage, KnowledgePageID: "k1", AuthorID: "u-other"}, user: viewer(), personaTool: true, want: true},

		{name: "unknown target type", thread: &threads.Thread{TargetType: "galaxy", AuthorID: "u-other"}, user: viewer(), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Assets:      assets,
				Collections: colls,
				Prompts:     prompts,
				AdminRoles:  []string{"dp_admin"},
				Shares: &fakeShareStore{
					byAsset:        map[string][]portaldomain.Share{"a1": tt.assetShares},
					collectionPerm: map[string]portaldomain.SharePermission{"c1": tt.collPerm},
				},
			}
			if tt.personaTool {
				cfg.PersonaTools = func([]string) []string { return []string{ApplyKnowledgeTool} }
			}
			assert.Equal(t, tt.want, New(cfg).CanModerateThread(context.Background(), tt.user, tt.thread))
		})
	}
}
