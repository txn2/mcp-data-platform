package tableregister

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	portaltoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// The adapter's follow path is what a content write reaches (#1536): it
// resolves the file without deciding authority, because the write already
// did, and reports what the follow did as the sentences the write carries.

// locatorFor resolves the one source these tests move, at whatever head it
// is at now.
func locatorFor(src *Source) Locator {
	return func(_ context.Context, kind, id string) (Source, bool) {
		if kind != src.Kind || id != src.ID {
			return Source{}, false
		}
		return *src, true
	}
}

func TestToolAdapter_RegisterCarriesTheFollowChoice(t *testing.T) {
	adapter, h := newAdapterHarness(t)
	ctx := callerContext("alice@example.com", "analyst")

	got, err := adapter.Register(ctx, assetRef, "scratch", "", portaltoolkit.RegisterOptions{Follow: true})
	require.NoError(t, err)
	assert.True(t, got.Follow)
	assert.Empty(t, got.FollowError)

	stored, err := h.store.Get(context.Background(), got.RegistrationID)
	require.NoError(t, err)
	assert.True(t, stored.Follow)

	// The list view carries both halves of the follow state.
	require.NoError(t, h.store.RecordFollowFailure(context.Background(), got.RegistrationID, "coordinator down"))
	listed, err := adapter.Tables(ctx, assetRef)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.True(t, listed[0].Follow)
	assert.Equal(t, "coordinator down", listed[0].FollowError)
}

func TestToolAdapter_FollowAssetTablesMovesTheTableAndReports(t *testing.T) {
	h := newHarness(t)
	src := testSource()
	adapter := NewToolAdapter(h.reg, []string{"admin"}, map[string]Subject{
		KindAsset: assetSubjectFor(adapterAsset(), nil),
	}, locatorFor(&src))
	ctx := callerContext("alice@example.com", "analyst")
	_, err := adapter.Register(ctx, assetRef, "scratch", "", portaltoolkit.RegisterOptions{Follow: true})
	require.NoError(t, err)

	// The write moved the head; the follow runs with no caller at all.
	src = h.moveHead(newHeadCSV)
	lines := adapter.FollowAssetTables(context.Background(), "asset_1", 2)

	require.Len(t, lines, 1)
	assert.Equal(t, "scratch.uploads.analyst_content on scratch now reads version 2. Its columns changed with the file.",
		lines[0])
	assert.Contains(t, h.trino.statements[2], "/v2/")
}

func TestToolAdapter_FollowResourceTablesUsesTheResourceKind(t *testing.T) {
	h := newHarness(t)
	rec := Record{
		ID: "res_1", Name: "Glossary", Bucket: "managed-resources",
		Key: "resources/global/reference/v/rev-1/glossary.csv", ContentType: "text/csv", OwnerID: "u1",
	}
	src := SourceFromResource(rec)
	h.objects.entries = []ObjectEntry{{Key: src.HeadKey}}
	adapter := NewToolAdapter(h.reg, nil, map[string]Subject{
		KindResource: resourceSubjectFor(rec),
	}, locatorFor(&src))
	_, err := adapter.Register(callerContext("alice@example.com", "analyst"), resourceRef, "scratch", "",
		portaltoolkit.RegisterOptions{Follow: true})
	require.NoError(t, err)

	src.HeadKey = "resources/global/reference/v/rev-2/glossary.csv"
	h.objects.entries = append(h.objects.entries, ObjectEntry{Key: src.HeadKey})
	lines := adapter.FollowResourceTables(context.Background(), "res_1", 2)

	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "now reads version 2")
	assert.Empty(t, adapter.FollowAssetTables(context.Background(), "res_1", 2),
		"the kind is part of the identity: the resource is not an asset")
}

// TestToolAdapter_FollowWithNothingToResolve: no locator, and a record the
// locator does not know, both report nothing -- the write happened, and there
// is no table to say anything about.
func TestToolAdapter_FollowWithNothingToResolve(t *testing.T) {
	h := newHarness(t)
	unlocatable := NewToolAdapter(h.reg, nil, map[string]Subject{KindAsset: assetSubjectFor(adapterAsset(), nil)}, nil)
	assert.Nil(t, unlocatable.FollowAssetTables(context.Background(), "asset_1", 2))

	src := testSource()
	located := NewToolAdapter(h.reg, nil, map[string]Subject{KindAsset: assetSubjectFor(adapterAsset(), nil)},
		locatorFor(&src))
	assert.Nil(t, located.FollowAssetTables(context.Background(), "asset_9", 2))
	assert.Nil(t, located.FollowAssetTables(context.Background(), "asset_1", 2), "nothing registered over it")
}
