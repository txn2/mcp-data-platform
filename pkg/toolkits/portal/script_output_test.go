package portal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// scriptOutputAsset is what a managed script's portal output looks like on
// disk: the script principal as its owner id, and the address of the person who
// owns the script as owner_email.
func scriptOutputAsset() portal.Asset {
	return portal.Asset{
		ID: "a-script", OwnerID: "script:weekly-revenue", OwnerEmail: "Alice@Example.com",
		Name: "Weekly revenue", ContentType: "text/csv", S3Bucket: "b", S3Key: "k",
		Tags: []string{"script", "weekly-revenue"}, CurrentVersion: 1,
	}
}

// scriptOwnerCtx is the script owner calling as themselves.
func scriptOwnerCtx() context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u-alice", UserEmail: "alice@example.com",
	})
}

// scriptStrangerCtx is a second authenticated person who is neither the owner nor an
// administrator.
func scriptStrangerCtx() context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u-bob", UserEmail: "bob@example.com",
	})
}

func scriptOutputToolkit(t *testing.T) (*Toolkit, *inMemoryAssetStore) {
	t.Helper()
	store := newInMemoryAssetStore()
	require.NoError(t, store.Insert(context.Background(), scriptOutputAsset()))
	return New(Config{Name: "test", AssetStore: store, S3Bucket: "b"}), store
}

// A run's output is in its owner's listing, beside the assets they saved
// themselves (#1551).
func TestManageAsset_ListIncludesTheOwnersScriptOutput(t *testing.T) {
	tk, store := scriptOutputToolkit(t)
	require.NoError(t, store.Insert(context.Background(), portal.Asset{
		ID: "a-own", OwnerID: "u-alice", OwnerEmail: "alice@example.com", Name: "Saved by hand",
	}))

	r, _, err := tk.handleManageAsset(scriptOwnerCtx(), nil, manageAssetInput{Action: actionList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var out struct {
		Assets []portal.Asset `json:"assets"`
		Total  int            `json:"total"`
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	assert.Equal(t, 2, out.Total)

	ids := make([]string, 0, len(out.Assets))
	for _, a := range out.Assets {
		ids = append(ids, a.ID)
	}
	assert.ElementsMatch(t, []string{"a-script", "a-own"}, ids)
}

// The listing is still one person's: a second authenticated non-admin sees
// nothing of it.
func TestManageAsset_ListExcludesAnotherPersonsScriptOutput(t *testing.T) {
	tk, _ := scriptOutputToolkit(t)

	r, _, err := tk.handleManageAsset(scriptStrangerCtx(), nil, manageAssetInput{Action: actionList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var out struct {
		Total int `json:"total"`
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	assert.Zero(t, out.Total)
}

// Reading and changing it needs no administrator, and a stranger is refused.
func TestManageAsset_OwnerReadsAndChangesTheScriptOutput(t *testing.T) {
	tk, _ := scriptOutputToolkit(t)

	got, _, err := tk.handleManageAsset(scriptOwnerCtx(), nil,
		manageAssetInput{Action: actionGet, AssetID: "a-script"})
	require.NoError(t, err)
	assert.False(t, got.IsError)

	renamed := "Weekly revenue (Q3)"
	up, _, err := tk.handleManageAsset(scriptOwnerCtx(), nil, manageAssetInput{
		Action: actionUpdate, AssetID: "a-script", Name: renamed,
	})
	require.NoError(t, err)
	assert.False(t, up.IsError)

	denied, _, err := tk.handleManageAsset(scriptStrangerCtx(), nil, manageAssetInput{
		Action: actionUpdate, AssetID: "a-script", Name: renamed,
	})
	require.NoError(t, err)
	assert.True(t, denied.IsError)
}

// A run still reaches the assets of the person it acts for, and is scoped to
// that person rather than to the script's owner: after a transfer the two are
// different people, and the run presents the author's authority.
func TestCallerAssetOwner_UnattendedCallerActsForItsAuthor(t *testing.T) {
	run := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "script:weekly-revenue", UserEmail: "alice@example.com",
		OnBehalfOfEmail: "author@example.com",
	})
	owner := callerAssetOwner(run)
	assert.Equal(t, "script:weekly-revenue", owner.UserID)
	assert.Equal(t, "author@example.com", owner.Email)

	person := callerAssetOwner(scriptOwnerCtx())
	assert.Equal(t, "u-alice", person.UserID)
	assert.Equal(t, "alice@example.com", person.Email)

	assert.False(t, callerAssetOwner(context.Background()).Identified())
}

// An enumeration is scoped to the caller's own library, which for a run is the
// outputs it produced. The address a run carries is its script owner's, and
// listing on it would hand every run of a script that person's whole library --
// a different rule from the one that lets a run act on a named asset its author
// owns.
func TestCallerAssetScope_AnUnattendedListingStaysTheScriptsOwn(t *testing.T) {
	run := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "script:weekly-revenue", UserEmail: "alice@example.com",
		OnBehalfOfEmail: "author@example.com", Source: middleware.SourceScript,
	})
	scope := callerAssetScope(run)
	assert.Equal(t, "script:weekly-revenue", scope.UserID)
	assert.Empty(t, scope.Email)

	person := callerAssetScope(scriptOwnerCtx())
	assert.Equal(t, "u-alice", person.UserID)
	assert.Equal(t, "alice@example.com", person.Email)

	assert.False(t, callerAssetScope(context.Background()).Identified())
}

// The listing a run gets is its own outputs, not the library of the person it
// acts for, exercised through the tool rather than through the scope helper.
func TestManageAsset_ARunsListingIsItsOwnOutputs(t *testing.T) {
	store := newInMemoryAssetStore()
	require.NoError(t, store.Insert(context.Background(), scriptOutputAsset()))
	require.NoError(t, store.Insert(context.Background(), portal.Asset{
		ID: "a-own", OwnerID: "u-alice", OwnerEmail: "alice@example.com", Name: "Saved by hand",
	}))
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "b"})

	run := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "script:weekly-revenue", UserEmail: "Alice@Example.com",
		OnBehalfOfEmail: "alice@example.com", Source: middleware.SourceScript,
	})
	r, _, err := tk.handleManageAsset(run, nil, manageAssetInput{Action: actionList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var out struct {
		Assets []portal.Asset `json:"assets"`
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	require.Len(t, out.Assets, 1)
	assert.Equal(t, "a-script", out.Assets[0].ID)

	// Naming the person's own asset is still the widened path.
	assert.True(t, ownsResource(run, "u-alice", "alice@example.com"))
}
