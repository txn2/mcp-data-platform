package httpserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	trinotoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// TestAssetVisibleTo is the decision the table surfaces make before anything
// else: an asset belongs to one person, and registering publishes its contents
// into a shared schema, so this is owner authority the way sharing is.
func TestAssetVisibleTo(t *testing.T) {
	asset := portal.Asset{ID: "a1", OwnerID: "u1", OwnerEmail: "alice@example.com"}
	admins := []string{"admin"}

	assert.True(t, assetVisibleTo(asset,
		tableregister.Caller{UserID: "u1", Email: "alice@example.com"}, admins), "the owner")

	// An administrator is unrestricted by design, everywhere. Both the
	// resolved flag and the raw roles say so, because a caller may be built by
	// a surface that resolves only one of them.
	assert.True(t, assetVisibleTo(asset,
		tableregister.Caller{UserID: "u9", Email: "root@example.com", Roles: []string{"admin"}}, admins),
		"an administrator reaches every asset")
	assert.True(t, assetVisibleTo(asset,
		tableregister.Caller{UserID: "u9", IsAdmin: true}, admins),
		"an administrator whose surface already resolved the flag")

	assert.False(t, assetVisibleTo(asset,
		tableregister.Caller{UserID: "u2", Email: "bob@example.com"}, admins), "another person")

	assert.False(t, assetVisibleTo(asset, tableregister.Caller{}, admins), "no caller")
}

// TestAssetVisibleTo_MatchesOnlyOnANonEmptyIdentity. An unauthenticated
// request and an asset that recorded no owner are not the same person, and a
// match on two empty strings would hand every unattributed asset to anybody.
func TestAssetVisibleTo_MatchesOnlyOnANonEmptyIdentity(t *testing.T) {
	unowned := portal.Asset{ID: "a1"}
	assert.False(t, assetVisibleTo(unowned, tableregister.Caller{}, nil))
	assert.False(t, assetVisibleTo(unowned, tableregister.Caller{UserID: "u1"}, nil))

	// The address match holds only when both sides carry one.
	byEmail := portal.Asset{ID: "a1", OwnerEmail: "alice@example.com"}
	assert.False(t, assetVisibleTo(byEmail, tableregister.Caller{UserID: "u2"}, nil))
	assert.True(t, assetVisibleTo(byEmail, tableregister.Caller{Email: "ALICE@example.com"}, nil),
		"an address is matched case-insensitively, as it is everywhere else")
}

func TestHasAnyRoleIn(t *testing.T) {
	assert.True(t, hasAnyRoleIn([]string{"analyst", "admin"}, []string{"admin"}))
	assert.False(t, hasAnyRoleIn([]string{"analyst"}, []string{"admin"}))
	assert.False(t, hasAnyRoleIn(nil, []string{"admin"}))
	assert.False(t, hasAnyRoleIn([]string{"admin"}, nil),
		"a deployment with no admin persona makes nobody an administrator")
}

// TestNewRegistrationID mints an opaque id with the prefix every other id on
// the platform carries.
func TestNewRegistrationID(t *testing.T) {
	first, err := newRegistrationID()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(first, "reg_"))
	assert.Len(t, first, len("reg_")+newIDLength*2)

	second, err := newRegistrationID()
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
}

// --- objectReaderAdapter ---

// fakeBlobs stands in for the shared S3 adapter.
type fakeBlobs struct {
	body      []byte
	entries   []s3adapter.ObjectEntry
	truncated bool
	err       error
}

func (f *fakeBlobs) GetObject(context.Context, string, string) (body []byte, contentType string, err error) {
	if f.err != nil {
		return nil, "", f.err
	}
	return f.body, "text/csv", nil
}

func (f *fakeBlobs) ListDirectory(
	context.Context, string, string,
) (entries []s3adapter.ObjectEntry, truncated bool, err error) {
	return f.entries, f.truncated, f.err
}

// TestObjectReaderAdapter is the whole of the impedance between the shared S3
// adapter's listing shape and the registrar's, including the truncation flag,
// which must never be read as "nothing else is there".
func TestObjectReaderAdapter(t *testing.T) {
	blobs := &fakeBlobs{
		body: []byte("a,b\n"),
		entries: []s3adapter.ObjectEntry{
			{Key: "d/content.csv", Size: 128},
			{Key: "d/notes.txt", Size: 12},
		},
	}
	adapter := objectReaderAdapter{client: blobs}

	body, ct, err := adapter.GetObject(context.Background(), "b", "d/content.csv")
	require.NoError(t, err)
	assert.Equal(t, "a,b\n", string(body))
	assert.Equal(t, "text/csv", ct)

	got, truncated, err := adapter.ListDirectory(context.Background(), "b", "d/")
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, []tableregister.ObjectEntry{
		{Key: "d/content.csv", Size: 128},
		{Key: "d/notes.txt", Size: 12},
	}, got)

	blobs.truncated = true
	_, truncated, err = adapter.ListDirectory(context.Background(), "b", "d/")
	require.NoError(t, err)
	assert.True(t, truncated, "a page boundary is reported, never read as the end")
}

func TestObjectReaderAdapter_Errors(t *testing.T) {
	boom := errors.New("s3 unreachable")
	adapter := objectReaderAdapter{client: &fakeBlobs{err: boom}}

	_, _, err := adapter.GetObject(context.Background(), "b", "k")
	assert.ErrorIs(t, err, boom)

	_, _, err = adapter.ListDirectory(context.Background(), "b", "d/")
	assert.ErrorIs(t, err, boom)
}

// TestTableCleanupHooks_UnwiredIsNil so a delete on a deployment that cannot
// register calls nothing rather than a hook that panics.
func TestTableCleanupHooks_UnwiredIsNil(t *testing.T) {
	hooks := tableCleanupHooks(nil)
	assert.Nil(t, hooks.AssetDeleted)
	assert.Nil(t, hooks.ResourceDeleted)
}

// TestBuildTableRegistrar_NoPlatformIsUnavailable: every gate the composition
// root applies before offering the action at all.
func TestBuildTableRegistrar_NoPlatformIsUnavailable(t *testing.T) {
	assert.Nil(t, buildTableRegistrar(nil))
	assert.False(t, buildTableRegistrar(nil).Available())
}

// TestWireTableLookup_NoRouterIsANoop covers the stdio shape, where there is
// no search federation to hand a lookup to.
func TestWireTableLookup_NoRouterIsANoop(t *testing.T) {
	assert.NotPanics(t, func() { wireTableLookup(nil, nil) })
}

// TestMountTableAPI_UnwiredMountsNothing: the routes are absent rather than
// present and always refusing, which is what lets the portal hide the action.
func TestMountTableAPI_UnwiredMountsNothing(t *testing.T) {
	assert.NotPanics(t, func() { mountTableAPI(nil, nil, nil, nil) })
	assert.NotPanics(t, func() { wireTableToolRegistrar(nil, nil) })
}

// TestErrorsIsUsableAcrossThePackages pins that a refusal keeps its identity
// through the wiring layer, which is what the HTTP status mapping reads.
func TestRefusalIdentitySurvivesWrapping(t *testing.T) {
	wrapped := errors.Join(tableregister.ErrRefused, errors.New("context"))
	assert.ErrorIs(t, wrapped, tableregister.ErrRefused)
}

// --- the connection picker ---

// pickerTrino answers which connections carry a scratch target and which of
// them accept the statement that creates a table there.
type pickerTrino struct {
	targets map[string]trinotoolkit.ScratchConfig
	// readOnly names the connections that refuse write SQL. Absent means the
	// connection accepts writes, which is the case every other test here wants.
	readOnly map[string]bool
}

func (pickerTrino) Exec(context.Context, string, string) error { return nil }

func (p pickerTrino) ScratchTarget(name string) (trinotoolkit.ScratchConfig, bool) {
	t, ok := p.targets[name]
	return t, ok
}

func (p pickerTrino) AcceptsWrites(name string) bool { return !p.readOnly[name] }

// TestScratchConnectionChoices is the picker's whole rule: a choice it offers
// must be one the registrar accepts. A connection the caller reaches but that
// cannot hold a table is not a choice, and neither is one of another kind.
func TestScratchConnectionChoices(t *testing.T) {
	exec := pickerTrino{targets: map[string]trinotoolkit.ScratchConfig{
		"scratch": {Catalog: "scratch", Schema: "uploads"},
		// A half-configured target is not usable and must not be offered.
		"half": {Catalog: "scratch"},
	}}
	reachable := []connreach.Connection{
		{Name: "warehouse", Kind: "trino", Description: "Curated tables"},
		{Name: "scratch", Kind: "trino", Description: "Working schema"},
		{Name: "half", Kind: "trino"},
		{Name: "acme-s3", Kind: "s3"},
		// A connection of another kind that happens to share a name with a
		// configured target is still not a Trino connection.
		{Name: "scratch", Kind: "s3"},
	}

	got := scratchConnectionChoices(reachable, exec)
	require.Len(t, got, 1)
	assert.Equal(t, "scratch", got[0].Name)
	assert.Equal(t, "Working schema", got[0].Description)
	assert.Equal(t, "scratch", got[0].Catalog)
	assert.Equal(t, "uploads", got[0].Schema)
}

// A scratch target is a destination, not a permission. A read-only connection
// can name one and still refuse the CREATE TABLE, which is what produced a
// picker offering a connection and a 500 "the registration could not be
// completed" the moment it was chosen.
func TestScratchConnectionChoices_SkipsReadOnly(t *testing.T) {
	exec := pickerTrino{
		targets: map[string]trinotoolkit.ScratchConfig{
			"scratch":   {Catalog: "scratch", Schema: "uploads"},
			"warehouse": {Catalog: "scratch", Schema: "uploads"},
		},
		readOnly: map[string]bool{"warehouse": true},
	}
	reachable := []connreach.Connection{
		{Name: "warehouse", Kind: "trino", Description: "Read-only"},
		{Name: "scratch", Kind: "trino", Description: "Working schema"},
	}

	got := scratchConnectionChoices(reachable, exec)
	require.Len(t, got, 1)
	assert.Equal(t, "scratch", got[0].Name)
}

// Every connection read-only is the same answer as no connection at all: a
// form saying nothing here can hold a table, rather than a picker whose every
// choice fails.
func TestScratchConnectionChoices_AllReadOnlyIsEmpty(t *testing.T) {
	exec := pickerTrino{
		targets:  map[string]trinotoolkit.ScratchConfig{"scratch": {Catalog: "scratch", Schema: "uploads"}},
		readOnly: map[string]bool{"scratch": true},
	}

	assert.Empty(t, scratchConnectionChoices(
		[]connreach.Connection{{Name: "scratch", Kind: "trino"}}, exec))
}

// TestScratchConnectionChoices_NoneReachableIsEmpty, which a form renders as
// "no connection here can hold a table" rather than as a broken picker.
func TestScratchConnectionChoices_NoneReachable(t *testing.T) {
	exec := pickerTrino{targets: map[string]trinotoolkit.ScratchConfig{
		"scratch": {Catalog: "scratch", Schema: "uploads"},
	}}

	assert.Empty(t, scratchConnectionChoices(nil, exec))
	assert.Empty(t, scratchConnectionChoices(
		[]connreach.Connection{{Name: "warehouse", Kind: "trino"}}, exec),
		"a connection with no scratch target is not a choice")
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
	subject := resourceSubject(stubResourceStore{res: res}, "resource-bucket")

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
	subject := resourceSubject(stubResourceStore{res: &resource.Resource{
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
	_, ok := resourceSubject(stubResourceStore{}, "b")(
		context.Background(), "res_1", tableregister.Caller{UserID: "u1"})
	assert.False(t, ok, "no row")

	_, ok = resourceSubject(stubResourceStore{err: errors.New("connection refused")}, "b")(
		context.Background(), "res_1", tableregister.Caller{UserID: "u1"})
	assert.False(t, ok, "a failed read")
}

// TestAssetSubject resolves the asset and applies the owner rule.
func TestAssetSubject(t *testing.T) {
	asset := &portal.Asset{
		ID: "a1", Name: "Vendor keys", OwnerID: "u1", OwnerEmail: "alice@example.com",
		S3Bucket: "portal-assets", S3Key: "artifacts/u1/a1/content.csv", ContentType: "text/csv",
	}
	subject := assetSubject(stubAssetStore{asset: asset}, []string{"admin"})

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
	subject := assetSubject(stubAssetStore{asset: &portal.Asset{
		ID: "a1", OwnerID: "u1", DeletedAt: &deletedAt,
	}}, nil)

	_, ok := subject(context.Background(), "a1", tableregister.Caller{UserID: "u1"})
	assert.False(t, ok)

	_, ok = assetSubject(stubAssetStore{err: errors.New("connection refused")}, nil)(
		context.Background(), "a1", tableregister.Caller{UserID: "u1"})
	assert.False(t, ok, "a failed read")
}

// TestTableSubjects_OmitsAKindWithNoStore, so a deployment holding one kind
// does not advertise the other on either surface.
func TestTableSubjects_OmitsAKindWithNoStore(t *testing.T) {
	both := tableSubjects(stubResourceStore{}, "b", stubAssetStore{}, nil)
	assert.Len(t, both, 2)

	resourcesOnly := tableSubjects(stubResourceStore{}, "b", nil, nil)
	assert.Contains(t, resourcesOnly, tableregister.KindResource)
	assert.NotContains(t, resourcesOnly, tableregister.KindAsset)

	assetsOnly := tableSubjects(nil, "", stubAssetStore{}, nil)
	assert.Contains(t, assetsOnly, tableregister.KindAsset)
	assert.NotContains(t, assetsOnly, tableregister.KindResource)

	assert.Empty(t, tableSubjects(nil, "", nil, nil))
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

// TestConnectionVisibility_AppliesThePersonaBoundary. The listing shows a
// caller the registrations they could query, which is the same predicate a
// tool call meets.
func TestConnectionVisibility_AppliesThePersonaBoundary(t *testing.T) {
	tr, pr := enumeratorFixture(t)
	visible := connectionVisibility(connreach.New(connreach.Deps{Toolkits: tr, Personas: pr}))

	names, all := visible(context.Background(), tableregister.Caller{Persona: "analyst"})
	assert.False(t, all)
	assert.Equal(t, []string{"warehouse"}, names)
}

// TestConnectionVisibility_KeepsToConnectionsATableCanBeOn: a registration
// lives on a query engine, so an object-store connection the persona also
// reaches is not one of them.
func TestConnectionVisibility_KeepsToConnectionsATableCanBeOn(t *testing.T) {
	tr, pr := enumeratorFixture(t)
	require.NoError(t, pr.Register(&persona.Persona{
		Name: "everything", Roles: []string{"dp_everything"},
		Connections: persona.ConnectionRules{Allow: []string{"*"}},
	}))
	visible := connectionVisibility(connreach.New(connreach.Deps{Toolkits: tr, Personas: pr}))

	names, all := visible(context.Background(), tableregister.Caller{Persona: "everything"})

	assert.False(t, all)
	assert.Equal(t, []string{"warehouse"}, names, "the object-store connection cannot hold a table")
}

// TestConnectionVisibility_AnAdministratorIsUnrestricted, which is what the
// operator opens this page for.
func TestConnectionVisibility_AnAdministratorIsUnrestricted(t *testing.T) {
	tr, pr := enumeratorFixture(t)
	visible := connectionVisibility(connreach.New(connreach.Deps{Toolkits: tr, Personas: pr}))

	names, all := visible(context.Background(), tableregister.Caller{Persona: "analyst", IsAdmin: true})

	assert.True(t, all)
	assert.Empty(t, names, "an unrestricted listing carries no connection list to intersect with")
}

// TestConnectionVisibility_WithNothingToEnumerateShowsNothing is the
// fail-closed reading: a deployment that cannot say which connections a
// persona reaches must not answer "all of them".
func TestConnectionVisibility_WithNothingToEnumerateShowsNothing(t *testing.T) {
	visible := connectionVisibility(connreach.New(connreach.Deps{}))

	names, all := visible(context.Background(), tableregister.Caller{Persona: "analyst"})

	assert.False(t, all)
	assert.Empty(t, names)
}

// TestSourceRefLookup_DispatchesOnKind, and answers nothing for a kind that is
// neither -- the two source kinds are the two ways a file reaches the platform.
func TestSourceRefLookup_DispatchesOnKind(t *testing.T) {
	lookup := sourceRefLookup(
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
