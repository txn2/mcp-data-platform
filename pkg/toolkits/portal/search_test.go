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

// searchableAssetStore adds the portal.AssetSearcher capability to the in-memory
// test store so the search action can be exercised.
type searchableAssetStore struct {
	*inMemoryAssetStore
	gotQuery portal.AssetSearchQuery
	result   []portal.ScoredAsset
	err      error
}

func (s *searchableAssetStore) SearchAssets(_ context.Context, q portal.AssetSearchQuery) ([]portal.ScoredAsset, error) {
	s.gotQuery = q
	return s.result, s.err
}

var _ portal.AssetSearcher = (*searchableAssetStore)(nil)

// searchTestEmail is the caller identity every search-action test runs as.
const searchTestEmail = "alice@example.com"

func searchCtx() context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u1", UserEmail: searchTestEmail,
	})
}

func searchResultMap(t *testing.T, r *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.NotEmpty(t, r.Content)
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

// errText returns the human-readable text content of an error result. Error
// results carry a self-describing message (and, via the error-contract
// middleware end to end, a structured envelope); the toolkit handler tests
// assert on the message substring here.
func errText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, r.Content)
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

func TestHandleSearch_Unavailable(t *testing.T) {
	// Plain in-memory store does not implement portal.AssetSearcher.
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Bucket: "b"})
	r, _, _ := tk.handleManageArtifact(searchCtx(), nil,
		manageArtifactInput{Action: actionSearch, Query: "x"})
	require.True(t, r.IsError)
	assert.Contains(t, errText(t, r), "unavailable")
}

func TestHandleSearch_MissingQuery(t *testing.T) {
	store := &searchableAssetStore{inMemoryAssetStore: newInMemoryAssetStore()}
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "b"})
	r, _, _ := tk.handleManageArtifact(searchCtx(), nil,
		manageArtifactInput{Action: actionSearch, Query: "   "})
	require.True(t, r.IsError)
	assert.Contains(t, errText(t, r), "required")
}

func TestHandleSearch_FailClosedAnonymous(t *testing.T) {
	store := &searchableAssetStore{inMemoryAssetStore: newInMemoryAssetStore()}
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "b"})
	// No PlatformContext at all -> resolveOwnerEmail returns "anonymous".
	r, _, _ := tk.handleManageArtifact(context.Background(), nil,
		manageArtifactInput{Action: actionSearch, Query: "sales"})
	require.True(t, r.IsError)
	assert.Contains(t, errText(t, r), "user identity")
}

func TestHandleSearch_FailClosedWhitespaceIdentity(t *testing.T) {
	store := &searchableAssetStore{inMemoryAssetStore: newInMemoryAssetStore()}
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "b"})
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "   ", UserEmail: searchTestEmail})
	r, _, _ := tk.handleManageArtifact(ctx, nil, manageArtifactInput{Action: actionSearch, Query: "sales"})
	require.True(t, r.IsError)
	assert.Contains(t, errText(t, r), "user identity")
}

func TestHandleSearch_Success(t *testing.T) {
	store := &searchableAssetStore{
		inMemoryAssetStore: newInMemoryAssetStore(),
		result:             []portal.ScoredAsset{{Asset: portal.Asset{ID: "a-1", Name: "Cohort"}, Score: 0.8}},
	}
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "b"})
	r, _, _ := tk.handleManageArtifact(searchCtx(), nil,
		manageArtifactInput{Action: actionSearch, Query: "cohort retention", Limit: 4})
	require.False(t, r.IsError)

	m := searchResultMap(t, r)
	// No embedder configured -> lexical ranking.
	assert.Equal(t, rankingLexical, m[fieldRanking])
	assert.EqualValues(t, 1, m[fieldTotal])

	// Owner scope (owner_id from PlatformContext.UserID) and query reach the store.
	assert.Equal(t, "u1", store.gotQuery.OwnerID)
	assert.Equal(t, "cohort retention", store.gotQuery.QueryText)
	assert.Equal(t, 4, store.gotQuery.Limit)
	assert.Nil(t, store.gotQuery.Embedding)
}

func TestHandleSearch_StoreError(t *testing.T) {
	store := &searchableAssetStore{inMemoryAssetStore: newInMemoryAssetStore(), err: assert.AnError}
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "b"})
	r, _, _ := tk.handleManageArtifact(searchCtx(), nil,
		manageArtifactInput{Action: actionSearch, Query: "x"})
	require.True(t, r.IsError)
	assert.Contains(t, searchResultMap(t, r)["error"], "failed to search assets")
}
