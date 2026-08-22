package tableregister

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// The adapter is what stands between manage_asset and the registrar. What is
// asserted here is that it takes its identity from the context the middleware
// chain built rather than from anything the tool call could say, and that what
// it hands back is what the tool reports.

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
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID:      "u1",
		UserEmail:   email,
		PersonaName: persona,
		Roles:       roles,
	})
}

func newAdapterHarness(t *testing.T) (*AssetAdapter, *harness) {
	t.Helper()
	h := newHarness(t)
	adapter := NewAssetAdapter(h.reg, []string{"admin"})
	require.NotNil(t, adapter)
	return adapter, h
}

func TestAssetAdapter_RegisterReportsWhatTheToolShows(t *testing.T) {
	adapter, h := newAdapterHarness(t)

	got, err := adapter.RegisterAsset(
		callerContext("alice@example.com", "analyst"), adapterAsset(), "scratch", "vendor keys")
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

// TestAssetAdapter_AnonymousCallRegistersNothing: the adapter reads identity
// from the context, so a call that carries none registers nothing rather than
// registering under an empty owner.
func TestAssetAdapter_AnonymousCallRegistersNothing(t *testing.T) {
	adapter, h := newAdapterHarness(t)

	_, err := adapter.RegisterAsset(context.Background(), adapterAsset(), "scratch", "")
	assert.ErrorIs(t, err, ErrNoIdentity)
	assert.Empty(t, h.trino.statements)
}

// TestAssetAdapter_AdminIsResolvedFromRoles: an administrator is unrestricted
// by design, and which roles say so is the deployment's admin persona.
func TestAssetAdapter_AdminIsResolvedFromRoles(t *testing.T) {
	adapter, h := newAdapterHarness(t)
	require.NoError(t, h.store.Insert(context.Background(), Registration{
		ID: "reg_held", SourceKind: KindAsset, SourceID: "asset_9",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "root_content", RegisteredBy: "bob@example.com",
	}))

	// A non-admin cannot take the name.
	_, err := adapter.RegisterAsset(
		callerContext("carol@example.com", "root"), adapterAsset(), "scratch", "content")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bob@example.com")

	// The same call from someone holding the admin role replaces it.
	_, err = adapter.RegisterAsset(
		callerContext("carol@example.com", "root", "admin"), adapterAsset(), "scratch", "content")
	require.NoError(t, err)
}

func TestAssetAdapter_ListAndDrop(t *testing.T) {
	adapter, _ := newAdapterHarness(t)
	ctx := callerContext("alice@example.com", "analyst")

	reg, err := adapter.RegisterAsset(ctx, adapterAsset(), "scratch", "")
	require.NoError(t, err)

	listed, err := adapter.AssetTables(ctx, adapterAsset())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, reg.RegistrationID, listed[0].RegistrationID)

	require.NoError(t, adapter.UnregisterAsset(ctx, reg.RegistrationID))
	listed, err = adapter.AssetTables(ctx, adapterAsset())
	require.NoError(t, err)
	assert.Empty(t, listed)
}

// TestAssetAdapter_DropAssetTables is what a delete calls. An asset delete is
// soft, so the file survives it; a table still pointing at that file would go
// on serving its rows out of a schema the owner can no longer see.
func TestAssetAdapter_DropAssetTables(t *testing.T) {
	adapter, h := newAdapterHarness(t)
	ctx := callerContext("alice@example.com", "analyst")

	_, err := adapter.RegisterAsset(ctx, adapterAsset(), "scratch", "")
	require.NoError(t, err)

	adapter.DropAssetTables(ctx, "asset_1")

	regs, err := h.store.BySource(ctx, KindAsset, "asset_1")
	require.NoError(t, err)
	assert.Empty(t, regs)
}

// TestAssetAdapter_ReportsStaleAgainstTheAssetItWasGiven: a new version moves
// the head key, and the table keeps serving the one it was registered against.
func TestAssetAdapter_ReportsStale(t *testing.T) {
	adapter, _ := newAdapterHarness(t)
	ctx := callerContext("alice@example.com", "analyst")

	_, err := adapter.RegisterAsset(ctx, adapterAsset(), "scratch", "")
	require.NoError(t, err)

	moved := adapterAsset()
	moved.S3Key = "artifacts/u1/asset_1/v2/content.csv"
	listed, err := adapter.AssetTables(ctx, moved)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.True(t, listed[0].Stale)
}

// TestNewAssetAdapter_UnwiredYieldsNil so the toolkit renders "this deployment
// cannot register tables" rather than holding an adapter that always fails.
func TestNewAssetAdapter_UnwiredYieldsNil(t *testing.T) {
	assert.Nil(t, NewAssetAdapter(New(Deps{}), nil))
	assert.Nil(t, NewAssetAdapter(nil, nil))
}

func TestSourceConstructorsCarryTheirKind(t *testing.T) {
	rec := Record{ID: "r1", Name: "R", Bucket: "b", Key: "d/f.csv", ContentType: "text/csv", OwnerID: "u1"}

	res := SourceFromResource(rec)
	assert.Equal(t, KindResource, res.Kind)
	assert.Equal(t, "d/f.csv", res.HeadKey)

	asset := SourceFromAssetRecord(rec)
	assert.Equal(t, KindAsset, asset.Kind)

	// The asset path the adapter itself uses agrees with the exported one.
	assert.Equal(t, KindAsset, sourceFromAsset(adapterAsset()).Kind)
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
