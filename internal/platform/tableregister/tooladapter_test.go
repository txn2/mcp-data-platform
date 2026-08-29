package tableregister

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	portaltoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// The adapter is what stands between manage_table and the registrar. What is
// asserted here is that it resolves the file from the reference the caller
// passed, takes its identity from the context the middleware chain built
// rather than from anything the tool call could say, and hands back what the
// tool reports.

const (
	assetRef    = "mcp:asset:asset_1"
	resourceRef = "mcp:resource:res_1"
)

func adapterAsset() portal.Asset {
	return portal.Asset{
		ID:          "asset_1",
		Name:        "Vendor keys",
		OwnerID:     "u1",
		OwnerEmail:  "alice@example.com",
		S3Bucket:    "portal-assets",
		S3Key:       "artifacts/u1/asset_1/content.csv",
		ContentType: "text/csv",
	}
}

// callerContext builds the context an authenticated tool call carries.
func callerContext(email, persona string, roles ...string) context.Context {
	return callerContextFor("u1", email, persona, roles...)
}

// callerContextFor is callerContext for somebody other than the owner.
func callerContextFor(userID, email, persona string, roles ...string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID:      userID,
		UserEmail:   email,
		PersonaName: persona,
		Roles:       roles,
	})
}

// assetSubjectFor resolves the one asset this harness knows about, for the
// owner or an administrator, recording the caller it was asked about so a test
// can assert the identity the adapter passed down.
func assetSubjectFor(asset portal.Asset, seen *Caller) Subject {
	return func(_ context.Context, id string, caller Caller) (Source, bool) {
		if seen != nil {
			*seen = caller
		}
		if id != asset.ID {
			return Source{}, false
		}
		if !caller.IsAdmin && caller.UserID != asset.OwnerID {
			return Source{}, false
		}
		return SourceFromAssetRecord(Record{
			ID: asset.ID, Name: asset.Name, Bucket: asset.S3Bucket,
			Key: asset.S3Key, ContentType: asset.ContentType, OwnerID: asset.OwnerID,
		}), true
	}
}

// resourceSubjectFor resolves one managed resource, readable only by its
// uploader.
func resourceSubjectFor(rec Record) Subject {
	return func(_ context.Context, id string, caller Caller) (Source, bool) {
		if id != rec.ID || (!caller.IsAdmin && caller.UserID != rec.OwnerID) {
			return Source{}, false
		}
		return SourceFromResource(rec), true
	}
}

func newAdapterHarness(t *testing.T) (*ToolAdapter, *harness) {
	t.Helper()
	h := newHarness(t)
	adapter := NewToolAdapter(h.reg, []string{"admin"}, map[string]Subject{
		KindAsset: assetSubjectFor(adapterAsset(), nil),
	}, nil)
	require.NotNil(t, adapter)
	return adapter, h
}

func TestToolAdapter_RegisterReportsWhatTheToolShows(t *testing.T) {
	adapter, h := newAdapterHarness(t)

	got, err := adapter.Register(
		callerContext("alice@example.com", "analyst"), assetRef, "scratch", "vendor keys", portaltoolkit.RegisterOptions{})
	require.NoError(t, err)

	assert.Equal(t, "scratch", got.Connection)
	assert.Equal(t, "scratch.uploads.analyst_vendor_keys", got.QueryTable)
	assert.Equal(t, []string{"store_id", "vendor_code", "rebate_pct"}, got.Columns)
	assert.Equal(t, "alice@example.com", got.RegisteredBy)
	assert.False(t, got.Stale)
	assert.Contains(t, got.SampleSQL, "CAST")

	// The registration went through the real registrar, so the DDL ran.
	assert.Len(t, h.trino.statements, 2)
}

// TestToolAdapter_ResolvesTheKindFromTheReference: one action serves both
// kinds because the kind travels inside the reference. A resource reference
// registers a resource without any argument saying so.
func TestToolAdapter_ResolvesTheKindFromTheReference(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		// The uploaded file sits in its own directory, which is what a managed
		// resource's head key looks like.
		h.objects = &fakeObjects{
			body:    []byte(csvBody),
			bodyCT:  "text/csv",
			entries: []ObjectEntry{{Key: "resources/res_1/glossary.csv", Size: int64(len(csvBody))}},
		}
	})
	adapter := NewToolAdapter(h.reg, []string{"admin"}, map[string]Subject{
		KindAsset: assetSubjectFor(adapterAsset(), nil),
		KindResource: resourceSubjectFor(Record{
			ID: "res_1", Name: "Vendor glossary", Bucket: "resources",
			Key: "resources/res_1/glossary.csv", ContentType: "text/csv", OwnerID: "u1",
		}),
	}, nil)
	require.NotNil(t, adapter)
	ctx := callerContext("alice@example.com", "analyst")

	got, err := adapter.Register(ctx, resourceRef, "scratch", "", portaltoolkit.RegisterOptions{})
	require.NoError(t, err)
	assert.Equal(t, "scratch.uploads.analyst_glossary", got.QueryTable)

	// It landed under the resource kind, which is what a delete sweeps and
	// what a search lookup reads.
	regs, err := h.store.BySource(ctx, KindResource, "res_1")
	require.NoError(t, err)
	require.Len(t, regs, 1)
}

// TestToolAdapter_UnreachableReferenceIsNotFound: a reference naming a record
// that does not exist and one naming a record belonging to somebody else are
// answered identically, so the tool cannot be used to discover which files
// exist.
func TestToolAdapter_UnreachableReferenceIsNotFound(t *testing.T) {
	adapter, h := newAdapterHarness(t)
	stranger := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u2", UserEmail: "mallory@example.com", PersonaName: "analyst",
	})

	_, missingErr := adapter.Register(
		callerContext("alice@example.com", "analyst"), "mcp:asset:nope", "scratch", "", portaltoolkit.RegisterOptions{})
	_, strangerErr := adapter.Register(stranger, assetRef, "scratch", "", portaltoolkit.RegisterOptions{})

	require.ErrorIs(t, missingErr, ErrNoSuchFile)
	require.ErrorIs(t, strangerErr, ErrNoSuchFile)
	assert.Equal(t, missingErr.Error(), strangerErr.Error(),
		"a file that is not there and a file that is not yours must read the same")
	assert.Empty(t, h.trino.statements)
}

// TestToolAdapter_RefusesAReferenceThatIsNotAStoredFile: a well-formed
// reference to something that is not a file is named rather than reported as
// malformed, so the caller knows what to pass instead.
func TestToolAdapter_RefusesAReferenceThatIsNotAStoredFile(t *testing.T) {
	adapter, _ := newAdapterHarness(t)
	ctx := callerContext("alice@example.com", "analyst")

	_, err := adapter.Register(ctx, "mcp:knowledge_page:kp_1", "scratch", "", portaltoolkit.RegisterOptions{})
	require.ErrorIs(t, err, ErrBadReference)
	assert.Contains(t, err.Error(), "knowledge_page")

	_, err = adapter.Register(ctx, "not a reference", "scratch", "", portaltoolkit.RegisterOptions{})
	assert.ErrorIs(t, err, ErrBadReference)
}

// TestToolAdapter_KindWithNoStoreIsUnavailable: a deployment holding no
// managed resources says so, rather than answering a resource reference as if
// the file were missing.
func TestToolAdapter_KindWithNoStoreIsUnavailable(t *testing.T) {
	adapter, _ := newAdapterHarness(t)

	_, err := adapter.Register(callerContext("alice@example.com", "analyst"), resourceRef, "scratch", "", portaltoolkit.RegisterOptions{})
	assert.ErrorIs(t, err, ErrUnavailable)
}

// TestToolAdapter_AnonymousCallRegistersNothing: the adapter reads identity
// from the context, so a call that carries none registers nothing rather than
// registering under an empty owner.
func TestToolAdapter_AnonymousCallRegistersNothing(t *testing.T) {
	h := newHarness(t)
	// A subject that admits anyone, so what refuses the call is the registrar's
	// identity check rather than the resolver's.
	adapter := NewToolAdapter(h.reg, nil, map[string]Subject{
		KindAsset: func(_ context.Context, _ string, _ Caller) (Source, bool) {
			return SourceFromAssetRecord(Record{
				ID: "asset_1", Name: "Vendor keys", Bucket: "portal-assets",
				Key: "artifacts/u1/asset_1/content.csv", ContentType: "text/csv",
			}), true
		},
	}, nil)
	require.NotNil(t, adapter)

	_, err := adapter.Register(context.Background(), assetRef, "scratch", "", portaltoolkit.RegisterOptions{})
	assert.ErrorIs(t, err, ErrNoIdentity)
	assert.Empty(t, h.trino.statements)
}

// TestToolAdapter_AdminIsResolvedFromRoles: an administrator is unrestricted
// by design, and which roles say so is the deployment's admin persona.
func TestToolAdapter_AdminIsResolvedFromRoles(t *testing.T) {
	var seen Caller
	h := newHarness(t)
	adapter := NewToolAdapter(h.reg, []string{"admin"}, map[string]Subject{
		KindAsset: assetSubjectFor(adapterAsset(), &seen),
	}, nil)
	require.NotNil(t, adapter)
	require.NoError(t, h.store.Insert(context.Background(), Registration{
		ID: "reg_held", SourceKind: KindAsset, SourceID: "asset_9",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "root_content", RegisteredBy: "bob@example.com",
	}))

	// A non-admin cannot take the name.
	_, err := adapter.Register(
		callerContext("carol@example.com", "root"), assetRef, "scratch", "content", portaltoolkit.RegisterOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bob@example.com")
	assert.False(t, seen.IsAdmin)

	// The same call from someone holding the admin role replaces it.
	_, err = adapter.Register(
		callerContext("carol@example.com", "root", "admin"), assetRef, "scratch", "content", portaltoolkit.RegisterOptions{})
	require.NoError(t, err)
	assert.True(t, seen.IsAdmin, "the resolver decides authority from the caller the adapter built")
}

func TestToolAdapter_ListAndDrop(t *testing.T) {
	adapter, _ := newAdapterHarness(t)
	ctx := callerContext("alice@example.com", "analyst")

	reg, err := adapter.Register(ctx, assetRef, "scratch", "", portaltoolkit.RegisterOptions{})
	require.NoError(t, err)

	listed, err := adapter.Tables(ctx, assetRef)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, reg.RegistrationID, listed[0].RegistrationID)

	require.NoError(t, adapter.Unregister(ctx, reg.RegistrationID))
	listed, err = adapter.Tables(ctx, assetRef)
	require.NoError(t, err)
	assert.Empty(t, listed)
}

// TestToolAdapter_ListRefusesAnUnreachableReference: listing is a read of the
// file, so it meets the same boundary registering does.
func TestToolAdapter_ListRefusesAnUnreachableReference(t *testing.T) {
	adapter, _ := newAdapterHarness(t)
	stranger := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u2", UserEmail: "mallory@example.com", PersonaName: "analyst",
	})

	_, err := adapter.Tables(stranger, assetRef)
	assert.ErrorIs(t, err, ErrNoSuchFile)
}

// TestToolAdapter_DropAssetTables is what a delete calls. An asset delete is
// soft, so the file survives it; a table still pointing at that file would go
// on serving its rows out of a schema the owner can no longer see.
func TestToolAdapter_DropAssetTables(t *testing.T) {
	adapter, h := newAdapterHarness(t)
	ctx := callerContext("alice@example.com", "analyst")

	_, err := adapter.Register(ctx, assetRef, "scratch", "", portaltoolkit.RegisterOptions{})
	require.NoError(t, err)

	adapter.DropAssetTables(ctx, "asset_1")

	regs, err := h.store.BySource(ctx, KindAsset, "asset_1")
	require.NoError(t, err)
	assert.Empty(t, regs)
}

// TestToolAdapter_ReportsStale: a new version moves the head key, and the
// table keeps serving the one it was registered against. The resolver reads
// the record afresh, so the staleness the tool reports is against the file as
// it is now.
func TestToolAdapter_ReportsStale(t *testing.T) {
	h := newHarness(t)
	current := adapterAsset()
	adapter := NewToolAdapter(h.reg, nil, map[string]Subject{
		KindAsset: func(_ context.Context, _ string, _ Caller) (Source, bool) {
			return SourceFromAssetRecord(Record{
				ID: current.ID, Name: current.Name, Bucket: current.S3Bucket,
				Key: current.S3Key, ContentType: current.ContentType, OwnerID: current.OwnerID,
			}), true
		},
	}, nil)
	require.NotNil(t, adapter)
	ctx := callerContext("alice@example.com", "analyst")

	_, err := adapter.Register(ctx, assetRef, "scratch", "", portaltoolkit.RegisterOptions{})
	require.NoError(t, err)

	current.S3Key = "artifacts/u1/asset_1/v2/content.csv"
	listed, err := adapter.Tables(ctx, assetRef)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.True(t, listed[0].Stale)
}

// TestNewToolAdapter_UnwiredYieldsNil so the toolkit renders "this deployment
// cannot register tables" rather than holding an adapter that always fails.
func TestNewToolAdapter_UnwiredYieldsNil(t *testing.T) {
	subjects := map[string]Subject{KindAsset: assetSubjectFor(adapterAsset(), nil)}
	assert.Nil(t, NewToolAdapter(New(Deps{}), nil, subjects, nil))
	assert.Nil(t, NewToolAdapter(nil, nil, subjects, nil))
	assert.Nil(t, NewToolAdapter(newHarness(t).reg, nil, nil, nil),
		"a registrar with no kind to resolve can register nothing through the tool")
}

func TestParseReference(t *testing.T) {
	kind, id, err := ParseReference("  mcp:asset:asset_1  ")
	require.NoError(t, err)
	assert.Equal(t, KindAsset, kind)
	assert.Equal(t, "asset_1", id)

	kind, id, err = ParseReference("mcp:resource:res_1")
	require.NoError(t, err)
	assert.Equal(t, KindResource, kind)
	assert.Equal(t, "res_1", id)

	for _, bad := range []string{"", "asset_1", "urn:li:dataset:(x,y,z)", "mcp:memory:m_1", "mcp:asset:"} {
		_, _, err = ParseReference(bad)
		assert.ErrorIs(t, err, ErrBadReference, "reference %q", bad)
	}
}

func TestSourceConstructorsCarryTheirKind(t *testing.T) {
	rec := Record{ID: "r1", Name: "R", Bucket: "b", Key: "d/f.csv", ContentType: "text/csv", OwnerID: "u1"}

	res := SourceFromResource(rec)
	assert.Equal(t, KindResource, res.Kind)
	assert.Equal(t, "d/f.csv", res.HeadKey)

	asset := SourceFromAssetRecord(rec)
	assert.Equal(t, KindAsset, asset.Kind)
}

func TestHasAnyRole(t *testing.T) {
	assert.True(t, hasAnyRole([]string{"analyst", "admin"}, []string{"admin"}))
	assert.False(t, hasAnyRole([]string{"analyst"}, []string{"admin"}))
	assert.False(t, hasAnyRole(nil, []string{"admin"}))
	assert.False(t, hasAnyRole([]string{"admin"}, nil),
		"a deployment with no admin persona makes nobody an administrator here")
}

// TestScratchTargetIsNotABoundary documents in a test what the config comment
// says in prose: the target names where a table goes, and Configured is the
// whole of what it decides.
func TestScratchConfigured(t *testing.T) {
	assert.True(t, trino.ScratchConfig{Catalog: "c", Schema: "s"}.Configured())
	assert.False(t, trino.ScratchConfig{Catalog: "c"}.Configured())
	assert.False(t, trino.ScratchConfig{Schema: "s"}.Configured())
	assert.False(t, trino.ScratchConfig{}.Configured())
}

// TestToolAdapter_RepairIsCarriedAndReported: the tool's ask reaches the
// registrar, and what the correction changed comes back on the registration so
// the agent can tell the person their file has a new version (#1441).
func TestToolAdapter_RepairIsCarriedAndReported(t *testing.T) {
	adapter, h := newAdapterHarness(t)
	h.objects.body = []byte("store_id,address\n101,\"12 Mill Rd\nSuite 4\"\n")

	ctx := callerContext("alice@example.com", "analyst")
	_, err := adapter.Register(ctx, assetRef, "scratch", "", portaltoolkit.RegisterOptions{})
	require.Error(t, err, "unasked, a file a table cannot read is refused")
	assert.ErrorIs(t, err, ErrNeedsRepair)

	got, err := adapter.Register(ctx, assetRef, "scratch", "", portaltoolkit.RegisterOptions{Repair: true})
	require.NoError(t, err)
	assert.Contains(t, got.Repaired, "put 1 row back onto one line")
	assert.False(t, got.Stale, "the registration points at the version the correction wrote")
	require.Len(t, h.reviser.saved, 1)
	assert.Contains(t, string(h.reviser.saved[0].content), "12 Mill Rd Suite 4")
}
