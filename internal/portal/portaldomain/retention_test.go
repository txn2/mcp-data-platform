package portaldomain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateMaxVersions(t *testing.T) {
	require.NoError(t, ValidateMaxVersions(MaxVersionsUnlimited), "0 asks for unlimited history")
	require.NoError(t, ValidateMaxVersions(1), "a cap of one is a legitimate ask")
	require.NoError(t, ValidateMaxVersions(DefaultMaxVersions))

	err := ValidateMaxVersions(-1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_versions", "the message names the field the caller sent")
}

func TestEffectiveMaxVersions(t *testing.T) {
	zero, five, hundred := 0, 5, 100
	tests := []struct {
		name            string
		asset, platform *int
		want            int
	}{
		{"neither set falls back to the platform default", nil, nil, DefaultMaxVersions},
		{"the deployment default applies with no override", nil, &five, 5},
		{"the asset override wins over the deployment default", &five, &hundred, 5},
		{"an asset asking for unlimited wins over a capped deployment", &zero, &hundred, MaxVersionsUnlimited},
		{"a deployment asking for unlimited applies with no override", nil, &zero, MaxVersionsUnlimited},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, EffectiveMaxVersions(tc.asset, tc.platform))
		})
	}
}

// TestEffectiveMaxVersions_NegativeReadsAsUnlimited covers the value no surface
// can enter and the column's CHECK refuses. Reaching one means something wrote
// past both, and the prune must not delete history on the strength of it.
func TestEffectiveMaxVersions_NegativeReadsAsUnlimited(t *testing.T) {
	neg := -7
	assert.Equal(t, MaxVersionsUnlimited, EffectiveMaxVersions(&neg, nil))
	assert.Equal(t, MaxVersionsUnlimited, EffectiveMaxVersions(nil, &neg))
}

// TestNormalizeThumbnailVariant covers what the thumbnail endpoints accept.
// Unlike DeriveThumbnailKeyVariant, which resolves an unknown variant to the
// light filename, the request parser refuses one: a caller asking for a variant
// this platform does not have should be told, not handed the light image and
// left to believe it received what it asked for.
func TestNormalizeThumbnailVariant(t *testing.T) {
	for _, in := range []string{"", ThumbnailVariantLight} {
		got, ok := NormalizeThumbnailVariant(in)
		assert.True(t, ok, in)
		assert.Equal(t, ThumbnailVariantLight, got, in)
	}

	got, ok := NormalizeThumbnailVariant(ThumbnailVariantDark)
	assert.True(t, ok)
	assert.Equal(t, ThumbnailVariantDark, got)

	for _, in := range []string{"sepia", "Dark", "DARK", " dark"} {
		got, ok := NormalizeThumbnailVariant(in)
		assert.False(t, ok, in)
		assert.Empty(t, got, in)
	}
}

// TestDeriveThumbnailKeyVariant pins the hidden filenames a capture writes. The
// leading dot is what keeps a thumbnail out of a table registered over the
// asset's directory: Hive parses every non-hidden object under an external
// location as CSV (#1327).
func TestDeriveThumbnailKeyVariant(t *testing.T) {
	const content = "artifacts/owner/asset_1/v/rev2/content.html"
	assert.Equal(t, "artifacts/owner/asset_1/v/rev2/.thumbnail.png",
		DeriveThumbnailKeyVariant(content, ThumbnailVariantLight))
	assert.Equal(t, "artifacts/owner/asset_1/v/rev2/.thumbnail_dark.png",
		DeriveThumbnailKeyVariant(content, ThumbnailVariantDark))
	assert.Equal(t, "artifacts/owner/asset_1/v/rev2/.thumbnail.png",
		DeriveThumbnailKeyVariant(content, "sideways"), "an unknown variant takes the light filename")
	assert.Equal(t, ".thumbnail.png", DeriveThumbnailKeyVariant("content.html", ""),
		"a key with no directory yields the bare filename")
}

// TestAssetVersionObjectKeys pins what a retention prune deletes: the version's
// content and the thumbnails beside it, all inside that version's own directory.
// Both spellings are covered, because a version captured before the hidden
// names still has PNGs in the bucket under the old ones.
func TestAssetVersionObjectKeys(t *testing.T) {
	v := AssetVersion{S3Key: "artifacts/owner/asset_1/v/rev2/content.html"}
	assert.Equal(t, []string{
		"artifacts/owner/asset_1/v/rev2/content.html",
		"artifacts/owner/asset_1/v/rev2/.thumbnail.png",
		"artifacts/owner/asset_1/v/rev2/.thumbnail_dark.png",
		"artifacts/owner/asset_1/v/rev2/thumbnail.png",
		"artifacts/owner/asset_1/v/rev2/thumbnail_dark.png",
	}, v.ObjectKeys())
}

func TestAssetStoredThumbnailKey(t *testing.T) {
	both := Asset{ThumbnailS3Key: "light.png", ThumbnailDarkS3Key: "dark.png"}
	assert.Equal(t, "dark.png", both.StoredThumbnailKey(ThumbnailVariantDark))
	assert.Equal(t, "light.png", both.StoredThumbnailKey(ThumbnailVariantLight))

	lightOnly := Asset{ThumbnailS3Key: "light.png"}
	assert.Equal(t, "light.png", lightOnly.StoredThumbnailKey(ThumbnailVariantDark),
		"a type with a built-in theme stores one thumbnail and serves it in both modes")

	assert.Empty(t, Asset{}.StoredThumbnailKey(ThumbnailVariantLight),
		"an asset whose thumbnail has not been generated yet has no key to serve")
}
