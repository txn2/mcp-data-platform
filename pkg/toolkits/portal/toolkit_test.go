package portal

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// mockS3Client implements portal.S3Client for testing.
type mockS3Client struct {
	putErr    error
	getBody   []byte
	getCT     string
	getErr    error
	deleteErr error
}

func (m *mockS3Client) PutObject(_ context.Context, _, _ string, _ []byte, _ string) error {
	return m.putErr
}

func (m *mockS3Client) GetObject(_ context.Context, _, _ string) (body []byte, ct string, err error) {
	return m.getBody, m.getCT, m.getErr
}

func (m *mockS3Client) DeleteObject(_ context.Context, _, _ string) error {
	return m.deleteErr
}

func (*mockS3Client) Close() error { return nil }

var _ portal.S3Client = (*mockS3Client)(nil)

type notFoundError struct{}

func (notFoundError) Error() string { return "not found" }

func TestNew(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket", S3Prefix: "prefix/", BaseURL: "http://localhost"})
	assert.Equal(t, "portal", tk.Kind())
	assert.Equal(t, "test", tk.Name())
	assert.Equal(t, "", tk.Connection())
	assert.Equal(t, []string{saveToolName, manageToolName}, tk.Tools())
	assert.NoError(t, tk.Close())
}

func TestSaveArtifact_Success(t *testing.T) {
	store := newInMemoryAssetStore()
	s3 := &mockS3Client{}
	tk := New(Config{
		Name: "test", AssetStore: store, S3Client: s3,
		S3Bucket: "my-bucket", S3Prefix: "assets/", BaseURL: "http://example.com",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID:    "user1",
		SessionID: "sess1",
	})

	input := saveArtifactInput{
		Name:        "My Dashboard",
		Content:     "<div>Hello</div>",
		ContentType: "text/html",
		Description: "A test dashboard",
		Tags:        []string{"test"},
	}

	result, _, err := tk.handleSaveArtifact(ctx, nil, input)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var output saveArtifactOutput
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &output))
	assert.NotEmpty(t, output.AssetID)
	assert.Contains(t, output.PortalURL, output.AssetID)
	assert.Equal(t, "Artifact saved successfully.", output.Message)

	// Verify asset was stored
	asset, getErr := store.Get(context.Background(), output.AssetID)
	require.NoError(t, getErr)
	assert.Equal(t, "user1", asset.OwnerID)
	assert.Equal(t, "My Dashboard", asset.Name)
	assert.Equal(t, int64(len("<div>Hello</div>")), asset.SizeBytes)
}

func TestSaveArtifact_ValidationErrors(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	tests := []struct {
		name  string
		input saveArtifactInput
		errIn string
	}{
		{"empty name", saveArtifactInput{Content: "x", ContentType: "text/html"}, "name is required"},
		{"empty content", saveArtifactInput{Name: "x", ContentType: "text/html"}, "content is required"},
		{"empty content_type", saveArtifactInput{Name: "x", Content: "x"}, "content_type is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _, err := tk.handleSaveArtifact(context.Background(), nil, tt.input)
			require.NoError(t, err)
			assert.True(t, result.IsError)
			tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
			require.True(t, ok)
			assert.Contains(t, tc.Text, tt.errIn)
		})
	}
}

func TestSaveArtifact_WithProvenance(t *testing.T) {
	store := newInMemoryAssetStore()
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	provCalls := []middleware.ProvenanceToolCall{
		{ToolName: "trino_query", Timestamp: "2024-01-01T00:00:00Z", Summary: "SELECT 1"},
		{ToolName: "datahub_search", Timestamp: "2024-01-01T00:01:00Z"},
	}

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", SessionID: "sess1",
	})
	ctx = middleware.WithProvenanceToolCalls(ctx, provCalls)

	input := saveArtifactInput{
		Name: "Chart", Content: "<svg/>", ContentType: "image/svg+xml",
	}

	result, _, err := tk.handleSaveArtifact(ctx, nil, input)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var output saveArtifactOutput
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &output))
	assert.True(t, output.ProvenanceCaptured)
	assert.Equal(t, 2, output.ToolCallsRecorded)
}

func TestManageArtifact_List(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Asset 1", Tags: []string{},
		Provenance: portal.Provenance{},
	})
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a2", OwnerID: "user2", Name: "Asset 2", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{Action: "list"})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp map[string]any
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &resp))
	assets, ok := resp["assets"].([]any) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Len(t, assets, 1) // Only user1's asset
}

func TestManageArtifact_Get(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Test", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	result, _, err := tk.handleManageArtifact(context.Background(), nil, manageArtifactInput{
		Action: "get", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestManageArtifact_GetMissing(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Bucket: "bucket"})

	result, _, err := tk.handleManageArtifact(context.Background(), nil, manageArtifactInput{
		Action: "get", AssetID: "missing",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestManageArtifact_Update(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Old", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update", AssetID: "a1", Name: "New Name",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	asset, _ := store.Get(context.Background(), "a1")
	assert.Equal(t, "New Name", asset.Name)
}

func TestManageArtifact_UpdateWithContent(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Old", ContentType: "text/html",
		Tags: []string{}, Provenance: portal.Provenance{},
	})

	s3 := &mockS3Client{}
	tk := New(Config{
		Name: "test", AssetStore: store, S3Client: s3,
		S3Bucket: "bucket", S3Prefix: "assets/",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update", AssetID: "a1", Content: "<div>Updated</div>",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestManageArtifact_UpdateWrongOwner(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Mine", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user2"})

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update", AssetID: "a1", Name: "Hijacked",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestManageArtifact_Delete(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "To Delete", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "delete", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	_, getErr := store.Get(context.Background(), "a1")
	assert.Error(t, getErr)
}

func TestManageArtifact_InvalidAction(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	result, _, err := tk.handleManageArtifact(context.Background(), nil, manageArtifactInput{Action: "invalid"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestManageArtifact_MissingAssetID(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	for _, action := range []string{"get", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			result, _, err := tk.handleManageArtifact(context.Background(), nil, manageArtifactInput{Action: action})
			require.NoError(t, err)
			assert.True(t, result.IsError)
		})
	}
}

func TestExtensionForContentType(t *testing.T) {
	tests := []struct {
		ct  string
		ext string
	}{
		{"text/html", ".html"},
		{"text/jsx", ".html"},
		{"image/svg+xml", ".svg"},
		{"text/markdown", ".md"},
		{"application/json", ".json"},
		{"text/csv", ".csv"},
		{"application/octet-stream", ".bin"},
	}
	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			assert.Equal(t, tt.ext, extensionForContentType(tt.ct))
		})
	}
}

func TestGenerateID(t *testing.T) {
	id, err := generateID()
	require.NoError(t, err)
	assert.Len(t, id, idLength*2) // hex encoding doubles the byte count
}

// inMemoryAssetStore is a simple in-memory implementation for tests.
type inMemoryAssetStore struct {
	assets map[string]portal.Asset
}

func newInMemoryAssetStore() *inMemoryAssetStore {
	return &inMemoryAssetStore{assets: make(map[string]portal.Asset)}
}

func (s *inMemoryAssetStore) Insert(_ context.Context, asset portal.Asset) error {
	s.assets[asset.ID] = asset
	return nil
}

func (s *inMemoryAssetStore) Get(_ context.Context, id string) (*portal.Asset, error) {
	a, ok := s.assets[id]
	if !ok {
		return nil, notFoundError{}
	}
	return &a, nil
}

func (s *inMemoryAssetStore) List(_ context.Context, filter portal.AssetFilter) ([]portal.Asset, int, error) {
	var result []portal.Asset
	for _, a := range s.assets {
		if a.DeletedAt != nil {
			continue
		}
		if filter.OwnerID != "" && a.OwnerID != filter.OwnerID {
			continue
		}
		result = append(result, a)
	}
	return result, len(result), nil
}

func (s *inMemoryAssetStore) Update(_ context.Context, id string, updates portal.AssetUpdate) error {
	a, ok := s.assets[id]
	if !ok {
		return notFoundError{}
	}
	if updates.Name != "" {
		a.Name = updates.Name
	}
	if updates.Description != "" {
		a.Description = updates.Description
	}
	if updates.Tags != nil {
		a.Tags = updates.Tags
	}
	s.assets[id] = a
	return nil
}

func (s *inMemoryAssetStore) SoftDelete(_ context.Context, id string) error {
	if _, ok := s.assets[id]; !ok {
		return notFoundError{}
	}
	delete(s.assets, id)
	return nil
}

func TestRegisterTools(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	tk.RegisterTools(server)

	// Verify tools are registered by checking Tools() returns them.
	tools := tk.Tools()
	assert.Contains(t, tools, saveToolName)
	assert.Contains(t, tools, manageToolName)
}

func TestSetProviders(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	// SetSemanticProvider and SetQueryProvider should not panic.
	tk.SetSemanticProvider(nil)
	tk.SetQueryProvider(nil)

	// Close should return nil.
	assert.NoError(t, tk.Close())
}

func TestSaveArtifact_S3Error(t *testing.T) {
	s3 := &mockS3Client{putErr: notFoundError{}}
	tk := New(Config{
		Name: "test", AssetStore: newInMemoryAssetStore(), S3Client: s3,
		S3Bucket: "bucket", S3Prefix: "assets/",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", SessionID: "sess1",
	})

	input := saveArtifactInput{
		Name: "Test", Content: "<div/>", ContentType: "text/html",
	}

	result, _, err := tk.handleSaveArtifact(ctx, nil, input)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to upload content")
}

func TestSaveArtifact_NoContext(t *testing.T) {
	store := newInMemoryAssetStore()
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	// Call without PlatformContext — should default to anonymous.
	input := saveArtifactInput{
		Name: "Test", Content: "<div/>", ContentType: "text/html",
	}

	result, _, err := tk.handleSaveArtifact(context.Background(), nil, input)
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestManageArtifact_DeleteWrongOwner(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Mine", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user2"})

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "delete", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestManageArtifact_DeleteNotFound(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Bucket: "bucket"})

	result, _, err := tk.handleManageArtifact(context.Background(), nil, manageArtifactInput{
		Action: "delete", AssetID: "missing",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestManageArtifact_GetDeletedAsset(t *testing.T) {
	store := newInMemoryAssetStore()
	now := time.Now()
	store.assets["a1"] = portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Deleted", Tags: []string{},
		Provenance: portal.Provenance{}, DeletedAt: &now,
	}

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	result, _, err := tk.handleManageArtifact(context.Background(), nil, manageArtifactInput{
		Action: "get", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "deleted")
}

func TestManageArtifact_ListNoContext(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "anonymous", Name: "Anon Asset", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	// Call without PlatformContext — should default to "anonymous".
	result, _, err := tk.handleManageArtifact(context.Background(), nil, manageArtifactInput{Action: "list"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestSaveArtifact_ValidationDescription(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	longDesc := make([]byte, 2001)
	for i := range longDesc {
		longDesc[i] = 'a'
	}

	result, _, err := tk.handleSaveArtifact(context.Background(), nil, saveArtifactInput{
		Name: "Test", Content: "x", ContentType: "text/html",
		Description: string(longDesc),
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestSaveArtifact_ValidationTags(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = "tag"
	}

	result, _, err := tk.handleSaveArtifact(context.Background(), nil, saveArtifactInput{
		Name: "Test", Content: "x", ContentType: "text/html",
		Tags: tooMany,
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// errorAssetStore is an in-memory asset store that returns errors for testing.
type errorAssetStore struct {
	insertErr  error
	listErr    error
	softDelErr error
	updateErr  error
	inMemoryAssetStore
}

func (s *errorAssetStore) Insert(_ context.Context, _ portal.Asset) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	return nil
}

func (s *errorAssetStore) List(_ context.Context, _ portal.AssetFilter) ([]portal.Asset, int, error) {
	if s.listErr != nil {
		return nil, 0, s.listErr
	}
	return nil, 0, nil
}

func (s *errorAssetStore) SoftDelete(_ context.Context, _ string) error {
	if s.softDelErr != nil {
		return s.softDelErr
	}
	return nil
}

func (s *errorAssetStore) Update(_ context.Context, _ string, _ portal.AssetUpdate) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	return nil
}

func TestSaveArtifact_StoreInsertError(t *testing.T) {
	store := &errorAssetStore{insertErr: notFoundError{}}
	store.assets = make(map[string]portal.Asset)
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", SessionID: "sess1",
	})

	input := saveArtifactInput{
		Name: "Test", Content: "<div/>", ContentType: "text/html",
	}

	result, _, err := tk.handleSaveArtifact(ctx, nil, input)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to save asset metadata")
}

func TestManageArtifact_ListError(t *testing.T) {
	store := &errorAssetStore{listErr: notFoundError{}}
	store.assets = make(map[string]portal.Asset)
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{Action: "list"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to list assets")
}

func TestManageArtifact_UpdateStoreError(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Test", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	errStore := &errorAssetStore{updateErr: notFoundError{}}
	errStore.assets = store.assets
	tk := New(Config{Name: "test", AssetStore: errStore, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update", AssetID: "a1", Name: "New",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to update asset")
}

func TestManageArtifact_UpdateWithContentError(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Test", ContentType: "text/html",
		Tags: []string{}, Provenance: portal.Provenance{},
	})

	s3 := &mockS3Client{putErr: notFoundError{}}
	tk := New(Config{
		Name: "test", AssetStore: store, S3Client: s3,
		S3Bucket: "bucket", S3Prefix: "assets/",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update", AssetID: "a1", Content: "<div>Updated</div>",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to upload new content")
}

func TestManageArtifact_SoftDeleteError(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Test", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	errStore := &errorAssetStore{softDelErr: notFoundError{}}
	errStore.assets = store.assets
	tk := New(Config{Name: "test", AssetStore: errStore, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "delete", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to delete asset")
}
