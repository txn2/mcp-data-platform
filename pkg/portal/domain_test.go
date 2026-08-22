package portal

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The portal's domain types, validators and stores live in
// internal/portal/portaldomain, portalstore, portalversions and portalnoop; this
// package re-exports them so callers keep their spelling. These tests pin the
// re-export itself: that each name resolves, and that a forwarder hands back the
// underlying answer unchanged rather than a rewritten or swallowed one.

func TestValidatorForwardersReturnTheUnderlyingVerdict(t *testing.T) {
	tests := []struct {
		name    string
		call    func() error
		wantErr string
	}{
		{"asset name accepted", func() error { return ValidateAssetName("Q4 Dashboard") }, ""},
		{"asset name required", func() error { return ValidateAssetName("") }, "name is required"},
		{"content type accepted", func() error { return ValidateContentType("text/html") }, ""},
		{"content type rejected", func() error { return ValidateContentType("application/x-shockwave-flash") }, "unsupported content_type"},
		{"content type unchanged is kept", func() error { return ValidateContentTypeChange("application/x-legacy", "application/x-legacy") }, ""},
		{"content type changed is re-checked", func() error { return ValidateContentTypeChange("text/html", "application/x-legacy") }, "unsupported content_type"},
		{"tags accepted", func() error { return ValidateTags([]string{"finance"}) }, ""},
		{"too many tags", func() error { return ValidateTags(make([]string, 21)) }, "too many tags"},
		{"description accepted", func() error { return ValidateDescription("short") }, ""},
		{"description too long", func() error { return ValidateDescription(strings.Repeat("x", 2001)) }, "description exceeds"},
		{"notice text too long", func() error { return ValidateNoticeText(strings.Repeat("x", 501)) }, "notice_text exceeds"},
		{"change summary too long", func() error { return ValidateChangeSummary(strings.Repeat("x", MaxChangeSummaryLength+1)) }, "change_summary exceeds"},
		{"collection name required", func() error { return ValidateCollectionName("") }, "name is required"},
		{"collection description too long", func() error { return ValidateCollectionDescription(strings.Repeat("x", 50001)) }, "description exceeds"},
		{"section title too long", func() error { return ValidateSectionTitle(strings.Repeat("x", 256)) }, "title exceeds"},
		{"section description too long", func() error { return ValidateSectionDescription(strings.Repeat("x", 10001)) }, "description exceeds"},
		{"sections accepted", func() error { return ValidateSections([]CollectionSection{{Title: "Overview"}}) }, ""},
		{"too many sections", func() error { return ValidateSections(make([]CollectionSection, 51)) }, "too many sections"},
		{"email accepted", func() error { return ValidateEmail("alice@example.com") }, ""},
		{"email rejected", func() error { return ValidateEmail("alice") }, "invalid email address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr,
				"the forwarder must hand back the validator's own message, not a wrapped one")
		})
	}
}

func TestIndexTextForwarders(t *testing.T) {
	assert.Equal(t, "Revenue\nBy region\nfinance sales",
		AssetIndexText("Revenue", "By region", []string{"finance", "sales"}))
	assert.Equal(t, "Q4\nExec summary\nOverview intro",
		CollectionIndexText("Q4", "Exec summary", "Overview intro"))
	assert.Equal(t, "Overview intro",
		SectionsText([]CollectionSection{{Title: "Overview", Description: "intro"}}))
}

func TestContentTypeForwarders(t *testing.T) {
	assert.Equal(t, ".html", ExtensionForContentType("text/html"))
	assert.Equal(t, "text/markdown", ResolveContentType("text/markdown", []byte("# heading")))
	assert.True(t, ValidSharePermission("editor"))
	assert.False(t, ValidSharePermission("owner"))
}

// TestStoreConstructorsReturnTheRightImplementation pins the wiring the
// composition root depends on: the Postgres constructors build a store over the
// handle they are given, and the no-database ones answer without a handle at
// all. A constructor that returned the wrong kind would only surface at runtime
// on a deployment with no database.
func TestStoreConstructorsReturnTheRightImplementation(t *testing.T) {
	db := &sql.DB{}

	require.NotNil(t, NewPostgresAssetStore(db))
	require.NotNil(t, NewPostgresShareStore(db))
	require.NotNil(t, NewPostgresVersionStore(db, nil, nil))
	// The version store also takes the blob client its retention prune deletes
	// through, and the deployment's default cap (#1421).
	require.NotNil(t, NewPostgresVersionStore(db, &mockS3Client{}, nil))
	require.NotNil(t, NewPostgresCollectionStore(db))

	// The no-database stores must answer, not panic, with no handle.
	assets, shares := NewNoopAssetStore(), NewNoopShareStore()
	versions, colls := NewNoopVersionStore(), NewNoopCollectionStore()

	ctx := t.Context()
	list, total, err := assets.List(ctx, AssetFilter{})
	require.NoError(t, err)
	assert.Empty(t, list)
	assert.Equal(t, 0, total)

	found, err := shares.ListByAsset(ctx, "a1")
	require.NoError(t, err)
	assert.Empty(t, found)

	versionNum, err := versions.CreateVersion(ctx, AssetVersion{})
	require.NoError(t, err)
	assert.Equal(t, 0, versionNum)

	collList, collTotal, err := colls.List(ctx, CollectionFilter{})
	require.NoError(t, err)
	assert.Empty(t, collList)
	assert.Equal(t, 0, collTotal)
}

// TestShareKeepForwarders pins the two share-scope predicates: KeepEditorShares
// is what the manage_feedback MCP toolkit passes to TargetGatherer, KeepAnyShare
// the broader view scope. Both must survive the split with the same verdicts.
func TestShareKeepForwarders(t *testing.T) {
	assert.True(t, KeepEditorShares(PermissionEditor))
	assert.False(t, KeepEditorShares(PermissionViewer))
	assert.False(t, KeepEditorShares(""))

	assert.True(t, KeepAnyShare(PermissionEditor))
	assert.True(t, KeepAnyShare(PermissionViewer))
	assert.True(t, KeepAnyShare(""))
}

// TestAliasedTypesKeepTheirMethods guards the one thing a type alias could
// silently drop for callers: the methods declared on the underlying type. A
// caller that ranges a portal.AssetFilter or builds a portal.TargetGatherer
// calls these by their portal spelling.
func TestAliasedTypesKeepTheirMethods(t *testing.T) {
	assert.Equal(t, 25, (&AssetFilter{Limit: 25}).EffectiveLimit())
	assert.Equal(t, 25, (&CollectionFilter{Limit: 25}).EffectiveLimit())
	assert.Equal(t, 5, AssetSearchQuery{Limit: 5}.EffectiveLimit())
	assert.Equal(t, 5, CollectionSearchQuery{Limit: 5}.EffectiveLimit())

	g := TargetGatherer{UserID: "u1", Email: "u1@example.com"}
	ids, err := g.AssetIDs(t.Context(), KeepAnyShare)
	require.NoError(t, err)
	assert.Empty(t, ids, "no stores wired means nothing to gather")

	collIDs, err := g.CollectionIDs(t.Context(), KeepEditorShares)
	require.NoError(t, err)
	assert.Empty(t, collIDs)
}
