package tablesource

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// TestAssetVisibleTo is the decision the table surfaces make before anything
// else: an asset belongs to one person, and registering publishes its contents
// into a shared schema, so this is owner authority the way sharing is.
func TestAssetVisibleTo(t *testing.T) {
	asset := portal.Asset{ID: "a1", OwnerID: "u1", OwnerEmail: "alice@example.com"}
	admins := []string{"admin"}

	assert.True(t, AssetVisibleTo(asset,
		tableregister.Caller{UserID: "u1", Email: "alice@example.com"}, admins), "the owner")

	// An administrator is unrestricted by design, everywhere. Both the
	// resolved flag and the raw roles say so, because a caller may be built by
	// a surface that resolves only one of them.
	assert.True(t, AssetVisibleTo(asset,
		tableregister.Caller{UserID: "u9", Email: "root@example.com", Roles: []string{"admin"}}, admins),
		"an administrator reaches every asset")
	assert.True(t, AssetVisibleTo(asset,
		tableregister.Caller{UserID: "u9", IsAdmin: true}, admins),
		"an administrator whose surface already resolved the flag")

	assert.False(t, AssetVisibleTo(asset,
		tableregister.Caller{UserID: "u2", Email: "bob@example.com"}, admins), "another person")

	assert.False(t, AssetVisibleTo(asset, tableregister.Caller{}, admins), "no caller")
}

// TestAssetVisibleTo_MatchesOnlyOnANonEmptyIdentity. An unauthenticated
// request and an asset that recorded no owner are not the same person, and a
// match on two empty strings would hand every unattributed asset to anybody.
func TestAssetVisibleTo_MatchesOnlyOnANonEmptyIdentity(t *testing.T) {
	unowned := portal.Asset{ID: "a1"}
	assert.False(t, AssetVisibleTo(unowned, tableregister.Caller{}, nil))
	assert.False(t, AssetVisibleTo(unowned, tableregister.Caller{UserID: "u1"}, nil))

	// The address match holds only when both sides carry one.
	byEmail := portal.Asset{ID: "a1", OwnerEmail: "alice@example.com"}
	assert.False(t, AssetVisibleTo(byEmail, tableregister.Caller{UserID: "u2"}, nil))
	assert.True(t, AssetVisibleTo(byEmail, tableregister.Caller{Email: "ALICE@example.com"}, nil),
		"an address is matched case-insensitively, as it is everywhere else")
}

func TestHasAnyRole(t *testing.T) {
	assert.True(t, HasAnyRole([]string{"analyst", "admin"}, []string{"admin"}))
	assert.False(t, HasAnyRole([]string{"analyst"}, []string{"admin"}))
	assert.False(t, HasAnyRole(nil, []string{"admin"}))
	assert.False(t, HasAnyRole([]string{"admin"}, nil),
		"a deployment with no admin persona makes nobody an administrator")
}

// --- the resolvers behind both table surfaces ---

// stubResourceStore answers Get with one resource and nothing else. The rest of
// the contract is embedded rather than implemented, so a method this resolver
// starts calling fails loudly instead of returning a zero value.
type stubResourceStore struct {
	resource.Store
	res *resource.Resource
	err error
}

func (s stubResourceStore) Get(context.Context, string) (*resource.Resource, error) {
	return s.res, s.err
}

// stubAssetStore answers Get with one asset and nothing else.
type stubAssetStore struct {
	portal.AssetStore
	asset *portal.Asset
	err   error
}

func (s stubAssetStore) Get(context.Context, string) (*portal.Asset, error) {
	return s.asset, s.err
}

// TestResourceSubject_RequiresAuthorityToChangeTheFile is the decision this
// resolver makes. Registering publishes a resource's contents into a schema
// everyone granted the connection can read, and resource scopes are not
// carried into Trino, so being able to READ the file is not enough: the rule is
// the one that governs updating and deleting it.
func TestResourceSubject_RequiresAuthorityToChangeTheFile(t *testing.T) {
	res := &resource.Resource{
		ID: "res_1", DisplayName: "Vendor rebates", Scope: resource.ScopePersona, ScopeID: "finance",
		S3Key: "resources/res_1/rebates.csv", MIMEType: "text/csv", UploaderSub: "u1",
	}
	subject := ResourceSubject(stubResourceStore{res: res}, "resource-bucket")

	src, ok := subject(context.Background(), "res_1", tableregister.Caller{UserID: "u1"})
	require.True(t, ok, "the uploader")
	assert.Equal(t, tableregister.KindResource, src.Kind)
	assert.Equal(t, "resource-bucket", src.Bucket)
	assert.Equal(t, "resources/res_1/rebates.csv", src.HeadKey)
	assert.Equal(t, "u1", src.OwnerID)

	// A member of the persona the resource is scoped to can READ it, which is
	// deliberately not enough to publish it to everyone with the connection.
	_, ok = subject(context.Background(), "res_1", tableregister.Caller{UserID: "u2", Persona: "finance"})
	assert.False(t, ok, "a reader of the scope is not an owner of the file")

	// Authority over the scope is: a platform administrator, and the
	// administrator of the persona it lives in.
	_, ok = subject(context.Background(), "res_1", tableregister.Caller{UserID: "u9", IsAdmin: true})
	assert.True(t, ok, "a platform administrator")
	_, ok = subject(context.Background(), "res_1",
		tableregister.Caller{UserID: "u9", Roles: []string{"dp_persona-admin:finance"}})
	assert.True(t, ok, "the administrator of the persona the resource belongs to")
	_, ok = subject(context.Background(), "res_1",
		tableregister.Caller{UserID: "u9", Roles: []string{"dp_persona-admin:sales"}})
	assert.False(t, ok, "the administrator of some other persona")
}

// TestResourceSubject_AGlobalResourceIsStillItsUploadersToRegister: a global
// resource is readable by every authenticated user, and the rule does not
// soften for it -- one sentence describes who may register either kind.
func TestResourceSubject_AGlobalResourceIsStillItsUploadersToRegister(t *testing.T) {
	subject := ResourceSubject(stubResourceStore{res: &resource.Resource{
		ID: "res_1", Scope: resource.ScopeGlobal, S3Key: "resources/res_1/x.csv", UploaderSub: "u1",
	}}, "b")

	_, ok := subject(context.Background(), "res_1", tableregister.Caller{UserID: "u2"})
	assert.False(t, ok)
	_, ok = subject(context.Background(), "res_1", tableregister.Caller{UserID: "u1"})
	assert.True(t, ok)
}

// TestResourceSubject_AbsentOrFailedReadsAreNotFound. A store that could not
// answer is reported the same way a missing row is, because the surfaces above
// turn ok=false into "no such file" and there is nothing else to say.
func TestResourceSubject_AbsentOrFailedReadsAreNotFound(t *testing.T) {
	_, ok := ResourceSubject(stubResourceStore{}, "b")(
		context.Background(), "res_1", tableregister.Caller{UserID: "u1"})
	assert.False(t, ok, "no row")

	_, ok = ResourceSubject(stubResourceStore{err: errors.New("connection refused")}, "b")(
		context.Background(), "res_1", tableregister.Caller{UserID: "u1"})
	assert.False(t, ok, "a failed read")
}

// TestAssetSubject resolves the asset and applies the owner rule.
func TestAssetSubject(t *testing.T) {
	asset := &portal.Asset{
		ID: "a1", Name: "Vendor keys", OwnerID: "u1", OwnerEmail: "alice@example.com",
		S3Bucket: "portal-assets", S3Key: "artifacts/u1/a1/content.csv", ContentType: "text/csv",
	}
	subject := AssetSubject(stubAssetStore{asset: asset}, []string{"admin"})

	src, ok := subject(context.Background(), "a1", tableregister.Caller{UserID: "u1"})
	require.True(t, ok)
	assert.Equal(t, tableregister.KindAsset, src.Kind)
	assert.Equal(t, "portal-assets", src.Bucket)
	assert.Equal(t, "artifacts/u1/a1/content.csv", src.HeadKey)

	_, ok = subject(context.Background(), "a1", tableregister.Caller{UserID: "u2"})
	assert.False(t, ok, "somebody else's asset")
}

// TestAssetSubject_ADeletedAssetIsGone. An asset delete is soft, so the row
// survives it; registering over one would build a table on a file its owner
// can no longer see.
func TestAssetSubject_ADeletedAssetIsGone(t *testing.T) {
	deletedAt := time.Now()
	subject := AssetSubject(stubAssetStore{asset: &portal.Asset{
		ID: "a1", OwnerID: "u1", DeletedAt: &deletedAt,
	}}, nil)

	_, ok := subject(context.Background(), "a1", tableregister.Caller{UserID: "u1"})
	assert.False(t, ok)

	_, ok = AssetSubject(stubAssetStore{err: errors.New("connection refused")}, nil)(
		context.Background(), "a1", tableregister.Caller{UserID: "u1"})
	assert.False(t, ok, "a failed read")
}

// TestSubjects_OmitsAKindWithNoStore, so a deployment holding one kind
// does not advertise the other on either surface.
func TestSubjects_OmitsAKindWithNoStore(t *testing.T) {
	both := Subjects(stubResourceStore{}, "b", stubAssetStore{}, nil)
	assert.Len(t, both, 2)

	resourcesOnly := Subjects(stubResourceStore{}, "b", nil, nil)
	assert.Contains(t, resourcesOnly, tableregister.KindResource)
	assert.NotContains(t, resourcesOnly, tableregister.KindAsset)

	assetsOnly := Subjects(nil, "", stubAssetStore{}, nil)
	assert.Contains(t, assetsOnly, tableregister.KindAsset)
	assert.NotContains(t, assetsOnly, tableregister.KindResource)

	assert.Empty(t, Subjects(nil, "", nil, nil))
}

// --- the seams behind the cross-source listing (#1472) ---

// bulkResourceStore answers GetByIDs and nothing else, so a method this
// resolver starts calling fails loudly rather than returning a zero value.
type bulkResourceStore struct {
	resource.Store
	rows map[string]*resource.Resource
	err  error
}

func (s bulkResourceStore) GetByIDs(_ context.Context, ids []string) (map[string]*resource.Resource, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]*resource.Resource, len(ids))
	for _, id := range ids {
		if r, ok := s.rows[id]; ok {
			out[id] = r
		}
	}
	return out, nil
}

// bulkAssetStore answers GetByIDs and nothing else.
type bulkAssetStore struct {
	portal.AssetStore
	rows map[string]*portal.Asset
	err  error
}

func (s bulkAssetStore) GetByIDs(_ context.Context, ids []string) (map[string]*portal.Asset, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]*portal.Asset, len(ids))
	for _, id := range ids {
		if a, ok := s.rows[id]; ok {
			out[id] = a
		}
	}
	return out, nil
}

// TestResourceSourceRefs_CarriesTheHeadAndTheAuthoritySeparately is the shape
// the listing rests on: the row says which file it is and where that file's
// content sits NOW whoever is asking, and authority over the file is a
// separate field that decides only whether the unregister action is offered.
func TestResourceSourceRefs_CarriesTheHeadAndTheAuthoritySeparately(t *testing.T) {
	store := bulkResourceStore{rows: map[string]*resource.Resource{
		"res_1": {
			ID: "res_1", DisplayName: "Vendor rebates", Scope: resource.ScopePersona, ScopeID: "finance",
			S3Key: "resources/res_1/v2/rebates.csv", UploaderSub: "u1",
		},
	}}

	mine := resourceSourceRefs(context.Background(), store, "resource-bucket",
		[]string{"res_1"}, tableregister.Caller{UserID: "u1"})
	require.Contains(t, mine, "res_1")
	assert.Equal(t, "Vendor rebates", mine["res_1"].Name)
	assert.Equal(t, "resource-bucket", mine["res_1"].Bucket)
	assert.Equal(t, "resources/res_1/v2/rebates.csv", mine["res_1"].HeadKey)
	assert.True(t, mine["res_1"].CanModify)

	theirs := resourceSourceRefs(context.Background(), store, "resource-bucket",
		[]string{"res_1"}, tableregister.Caller{UserID: "u2", Persona: "finance"})
	require.Contains(t, theirs, "res_1", "a reader of the scope still sees which file the table is over")
	assert.Equal(t, "resources/res_1/v2/rebates.csv", theirs["res_1"].HeadKey,
		"and still gets an honest staleness verdict")
	assert.False(t, theirs["res_1"].CanModify, "without being offered the action")
}

// TestResourceSourceRefs_AbsentAndFailedReads: an id with no row is left out,
// which the listing renders as a source that is gone; a store that could not
// answer degrades the whole page the same way rather than failing it.
func TestResourceSourceRefs_AbsentAndFailedReads(t *testing.T) {
	assert.NotContains(t,
		resourceSourceRefs(context.Background(), bulkResourceStore{}, "b",
			[]string{"res_gone"}, tableregister.Caller{UserID: "u1"}),
		"res_gone")

	// A key present with no record behind it is the same answer as a key that
	// is not there: a listing row over a source nothing describes.
	assert.NotContains(t,
		resourceSourceRefs(context.Background(),
			bulkResourceStore{rows: map[string]*resource.Resource{"res_1": nil}}, "b",
			[]string{"res_1"}, tableregister.Caller{UserID: "u1"}),
		"res_1")

	assert.Empty(t, resourceSourceRefs(context.Background(),
		bulkResourceStore{err: errors.New("connection refused")}, "b",
		[]string{"res_1"}, tableregister.Caller{UserID: "u1"}))

	assert.Nil(t, resourceSourceRefs(context.Background(), nil, "b",
		[]string{"res_1"}, tableregister.Caller{UserID: "u1"}),
		"a deployment with no resource store resolves no resource")
}

// TestAssetSourceRefs_AppliesTheOwnerRuleToTheActionOnly: every caller who
// reaches the connection learns what the table is over; only the owner and an
// administrator are offered the action that drops it.
func TestAssetSourceRefs_AppliesTheOwnerRuleToTheActionOnly(t *testing.T) {
	store := bulkAssetStore{rows: map[string]*portal.Asset{
		"a1": {
			ID: "a1", Name: "Vendor keys", OwnerID: "u1", OwnerEmail: "alice@example.com",
			S3Bucket: "portal-assets", S3Key: "artifacts/u1/a1/v3/content.csv",
		},
	}}

	mine := assetSourceRefs(context.Background(), store, []string{"admin"},
		[]string{"a1"}, tableregister.Caller{UserID: "u1"})
	require.Contains(t, mine, "a1")
	assert.Equal(t, "portal-assets", mine["a1"].Bucket)
	assert.Equal(t, "artifacts/u1/a1/v3/content.csv", mine["a1"].HeadKey)
	assert.True(t, mine["a1"].CanModify)

	theirs := assetSourceRefs(context.Background(), store, []string{"admin"},
		[]string{"a1"}, tableregister.Caller{UserID: "u2"})
	require.Contains(t, theirs, "a1")
	assert.False(t, theirs["a1"].CanModify)

	operator := assetSourceRefs(context.Background(), store, []string{"admin"},
		[]string{"a1"}, tableregister.Caller{UserID: "u9", Roles: []string{"admin"}})
	assert.True(t, operator["a1"].CanModify, "an administrator")
}

// TestAssetSourceRefs_ADeletedAssetIsGone. An asset delete is soft, so the row
// survives it; the listing has to report the source as gone rather than name a
// file its owner can no longer see.
func TestAssetSourceRefs_ADeletedAssetIsGone(t *testing.T) {
	deletedAt := time.Now()
	store := bulkAssetStore{rows: map[string]*portal.Asset{
		"a1": {ID: "a1", OwnerID: "u1", DeletedAt: &deletedAt},
	}}

	assert.NotContains(t,
		assetSourceRefs(context.Background(), store, nil, []string{"a1"},
			tableregister.Caller{UserID: "u1"}),
		"a1")

	assert.Empty(t, assetSourceRefs(context.Background(),
		bulkAssetStore{err: errors.New("connection refused")}, nil,
		[]string{"a1"}, tableregister.Caller{UserID: "u1"}))

	assert.Nil(t, assetSourceRefs(context.Background(), nil, nil,
		[]string{"a1"}, tableregister.Caller{UserID: "u1"}))
}

// TestRefLookup_DispatchesOnKind, and answers nothing for a kind that is
// neither -- the two source kinds are the two ways a file reaches the platform.
func TestRefLookup_DispatchesOnKind(t *testing.T) {
	lookup := RefLookup(
		bulkResourceStore{rows: map[string]*resource.Resource{
			"res_1": {ID: "res_1", DisplayName: "Rebates", S3Key: "r/res_1/x.csv", UploaderSub: "u1"},
		}},
		"resource-bucket",
		bulkAssetStore{rows: map[string]*portal.Asset{
			"a1": {ID: "a1", Name: "Vendor keys", OwnerID: "u1", S3Bucket: "portal-assets", S3Key: "a/a1/x.csv"},
		}},
		[]string{"admin"},
	)
	caller := tableregister.Caller{UserID: "u1"}

	assert.Equal(t, "Rebates",
		lookup(context.Background(), tableregister.KindResource, []string{"res_1"}, caller)["res_1"].Name)
	assert.Equal(t, "Vendor keys",
		lookup(context.Background(), tableregister.KindAsset, []string{"a1"}, caller)["a1"].Name)
	assert.Nil(t, lookup(context.Background(), "dataset", []string{"x"}, caller))
}

// --- the caller-less resolver a follow reads through (#1536) ---

func TestLocator(t *testing.T) {
	res := &resource.Resource{
		ID: "res_1", DisplayName: "Stores", MIMEType: "text/csv",
		S3Key: "resources/global/reference/res_1/stores.csv", UploaderSub: "u1",
	}
	asset := &portal.Asset{
		ID: "a1", Name: "Sales", OwnerID: "u1", S3Bucket: "portal-assets",
		S3Key: "artifacts/u1/a1/content.csv", ContentType: "text/csv",
	}
	locate := Locator(stubResourceStore{res: res}, "managed-resources", stubAssetStore{asset: asset})

	src, ok := locate(context.Background(), tableregister.KindResource, "res_1")
	require.True(t, ok)
	assert.Equal(t, tableregister.Source{
		Kind: tableregister.KindResource, ID: "res_1", Name: "Stores",
		Bucket: "managed-resources", HeadKey: res.S3Key, ContentType: "text/csv", OwnerID: "u1",
	}, src)

	src, ok = locate(context.Background(), tableregister.KindAsset, "a1")
	require.True(t, ok)
	assert.Equal(t, tableregister.Source{
		Kind: tableregister.KindAsset, ID: "a1", Name: "Sales",
		Bucket: "portal-assets", HeadKey: asset.S3Key, ContentType: "text/csv", OwnerID: "u1",
	}, src)

	_, ok = locate(context.Background(), "prompt", "p1")
	assert.False(t, ok, "a kind no registration is built over")
}

// TestLocator_ResolvesWhatTheSubjectsResolve: the two resolvers read the same
// record into the same Source; only the authority check separates them.
func TestLocator_ResolvesWhatTheSubjectsResolve(t *testing.T) {
	asset := &portal.Asset{
		ID: "a1", Name: "Sales", OwnerID: "u1", S3Bucket: "portal-assets",
		S3Key: "artifacts/u1/a1/content.csv", ContentType: "text/csv",
	}
	store := stubAssetStore{asset: asset}

	located, ok := Locator(nil, "", store)(context.Background(), tableregister.KindAsset, "a1")
	require.True(t, ok)
	resolved, ok := AssetSubject(store, nil)(context.Background(), "a1", tableregister.Caller{UserID: "u1"})
	require.True(t, ok)
	assert.Equal(t, resolved, located)
}

func TestLocator_AbsentDeletedAndUnwired(t *testing.T) {
	gone := time.Now()
	cases := []struct {
		name   string
		locate tableregister.Locator
		kind   string
	}{
		{"no resource store", Locator(nil, "", stubAssetStore{}), tableregister.KindResource},
		{"resource read failed", Locator(stubResourceStore{err: errors.New("away")}, "b", nil), tableregister.KindResource},
		{"resource absent", Locator(stubResourceStore{}, "b", nil), tableregister.KindResource},
		{"no asset store", Locator(stubResourceStore{}, "b", nil), tableregister.KindAsset},
		{"asset read failed", Locator(nil, "", stubAssetStore{err: errors.New("away")}), tableregister.KindAsset},
		{"asset deleted", Locator(nil, "", stubAssetStore{asset: &portal.Asset{ID: "a1", DeletedAt: &gone}}), tableregister.KindAsset},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := tc.locate(context.Background(), tc.kind, "x")
			assert.False(t, ok)
		})
	}
}
