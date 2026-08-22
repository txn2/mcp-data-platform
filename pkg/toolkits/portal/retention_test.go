package portal

// manage_asset's half of the asset version retention cap (#1421): the update
// action carries max_versions, it reaches the same column the portal and admin
// routes write, and a negative value is refused where it was entered.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// sentMaxVersions builds the argument as a caller who named the field sends it:
// value for a number, nil for an explicit null. An absent field is the zero
// OptionalInt, which needs no helper.
func sentMaxVersions(value *int) httpjson.OptionalInt {
	return httpjson.OptionalInt{Present: true, Value: value}
}

func TestManageAsset_MaxVersionsReachesTheColumn(t *testing.T) {
	twenty := 20
	tests := []struct {
		name  string
		input httpjson.OptionalInt
		want  *int
	}{
		{"a value is written through", sentMaxVersions(&twenty), &twenty},
		{"null returns the asset to the deployment default", sentMaxVersions(nil), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assets := newInMemoryAssetStore()
			tk, ctx := newAllowlistToolkitOver(t, assets)
			existing := 5
			require.NoError(t, assets.Insert(context.Background(), portal.Asset{
				ID: "a1", OwnerID: "user1", Name: "Dashboard",
				ContentType: "text/html", MaxVersions: &existing,
			}))

			result, _, err := tk.handleUpdate(ctx, manageAssetInput{
				Action: "update", AssetID: "a1", MaxVersions: tc.input,
			})
			require.NoError(t, err)
			require.False(t, result.IsError, "a retention-only edit is a complete update: %s", errText(t, result))

			stored, getErr := assets.Get(context.Background(), "a1")
			require.NoError(t, getErr)
			if tc.want == nil {
				assert.Nil(t, stored.MaxVersions)
				return
			}
			require.NotNil(t, stored.MaxVersions)
			assert.Equal(t, *tc.want, *stored.MaxVersions)
		})
	}
}

func TestManageAsset_MaxVersionsRefusesANegativeValue(t *testing.T) {
	assets := newInMemoryAssetStore()
	tk, ctx := newAllowlistToolkitOver(t, assets)
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Dashboard", ContentType: "text/html",
	}))

	negative := -1
	result, _, err := tk.handleUpdate(ctx, manageAssetInput{
		Action: "update", AssetID: "a1", MaxVersions: sentMaxVersions(&negative),
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "max_versions")

	stored, getErr := assets.Get(context.Background(), "a1")
	require.NoError(t, getErr)
	assert.Nil(t, stored.MaxVersions, "a refused value never reaches the column")
}

// TestManageAsset_NewCapAppliesToTheVersionTheSameCallWrites pins the ordering:
// a call that lowers max_versions and replaces the content in one breath must
// write the metadata first, or the version it creates prunes against the cap the
// caller was replacing.
func TestManageAsset_NewCapAppliesToTheVersionTheSameCallWrites(t *testing.T) {
	assets := newInMemoryAssetStore()
	versions := newLinkedVersionStore(assets)
	tk := New(Config{
		Name: "test", AssetStore: assets, VersionStore: versions,
		S3Client: &mockS3Client{}, S3Bucket: "bucket", S3Prefix: "assets/",
	})
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", UserEmail: "user1@example.com",
	})
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Dashboard",
		ContentType: "text/html", S3Bucket: "bucket", S3Key: "assets/a1/content.html",
	}))
	_, err := versions.CreateVersion(context.Background(), portal.AssetVersion{
		ID: "v1", AssetID: "a1", S3Key: "assets/a1/content.html", S3Bucket: "bucket",
		ContentType: "text/html", ChangeSummary: "Initial version",
	})
	require.NoError(t, err)

	three := 3
	result, _, updateErr := tk.handleUpdate(ctx, manageAssetInput{
		Action: "update", AssetID: "a1",
		Content:     "<html><body>fresh</body></html>",
		MaxVersions: sentMaxVersions(&three),
	})
	err = updateErr
	require.NoError(t, err)
	require.False(t, result.IsError, "%s", errText(t, result))

	stored, getErr := assets.Get(context.Background(), "a1")
	require.NoError(t, getErr)
	require.NotNil(t, stored.MaxVersions)
	assert.Equal(t, 3, *stored.MaxVersions)
	assert.Equal(t, 2, stored.CurrentVersion,
		"the content write happened after the cap was in place, not instead of it")
}

// TestManageAsset_AbsentMaxVersionsIsNotAnUpdate pins that omitting the argument
// leaves the setting alone -- and, on its own, is still "no fields to update".
func TestManageAsset_AbsentMaxVersionsIsNotAnUpdate(t *testing.T) {
	update, present := metadataUpdate(manageAssetInput{Action: "update", AssetID: "a1"})
	assert.False(t, present, "an input naming no field is not a metadata update")
	assert.Nil(t, update.MaxVersions)
	assert.False(t, update.ClearMaxVersions)
}

// TestManageAssetSchema_MaxVersions pins the advertised argument: the tool's
// schema is hand-written, so a field the handler reads but the schema omits
// would be rejected before the handler ever saw it.
func TestManageAssetSchema_MaxVersions(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(manageAssetSchema, &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)

	prop, ok := props["max_versions"].(map[string]any)
	require.True(t, ok, "manage_asset must advertise max_versions")
	assert.Equal(t, []any{"integer", "null"}, prop["type"],
		"null is how a caller asks for the deployment default back")
	assert.Equal(t, float64(0), prop["minimum"])
}
