package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
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

func (m *mockS3Client) PutObjectStream(_ context.Context, _, _ string, body io.Reader, _ string) (int64, error) {
	n, _ := io.Copy(io.Discard, body)
	return n, m.putErr
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
	assert.Equal(t, []string{SaveToolName, ManageToolName, ManageTableToolName, ManageResourceToolName, feedbackToolName}, tk.Tools())
	assert.NoError(t, tk.Close())
}

func TestSaveAsset_Success(t *testing.T) {
	store := newInMemoryAssetStore()
	s3 := &mockS3Client{}
	tk := New(Config{
		Name: "test", AssetStore: store, S3Client: s3,
		S3Bucket: "my-bucket", S3Prefix: "assets/", BaseURL: "http://example.com",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID:    "user1",
		UserEmail: "user1@example.com",
		SessionID: "sess1",
	})

	input := saveAssetInput{
		Name:        "My Dashboard",
		Content:     "<div>Hello</div>",
		ContentType: "text/html",
		Description: "A test dashboard",
		Tags:        []string{"test"},
	}

	result, _, err := tk.handleSaveAsset(ctx, nil, input)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var output saveAssetOutput
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &output))
	assert.NotEmpty(t, output.AssetID)
	assert.Contains(t, output.PortalURL, output.AssetID)
	assert.Equal(t, "Asset saved successfully.", output.Message)

	// Verify asset was stored
	asset, getErr := store.Get(context.Background(), output.AssetID)
	require.NoError(t, getErr)
	assert.Equal(t, "user1", asset.OwnerID)
	assert.Equal(t, "user1@example.com", asset.OwnerEmail)
	assert.Equal(t, "My Dashboard", asset.Name)
	assert.Equal(t, int64(len("<div>Hello</div>")), asset.SizeBytes)
}

func TestSaveAsset_ValidationErrors(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	tests := []struct {
		name  string
		input saveAssetInput
		errIn string
	}{
		{"empty name", saveAssetInput{Content: "x", ContentType: "text/html"}, "name is required"},
		{"empty content", saveAssetInput{Name: "x", ContentType: "text/html"}, "content is required"},
		{"empty content_type", saveAssetInput{Name: "x", Content: "x"}, "content_type is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _, err := tk.handleSaveAsset(context.Background(), nil, tt.input)
			require.NoError(t, err)
			assert.True(t, result.IsError)
			tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
			require.True(t, ok)
			assert.Contains(t, tc.Text, tt.errIn)
		})
	}
}

func TestSaveAsset_WithProvenance(t *testing.T) {
	store := newInMemoryAssetStore()
	var captured portal.ProvenanceRequest
	tk := New(Config{
		Name: "test", AssetStore: store, S3Client: &mockS3Client{}, S3Bucket: "bucket",
		CaptureProvenance: func(_ context.Context, req portal.ProvenanceRequest) portal.ProvenanceCapture {
			captured = req
			return portal.ProvenanceCapture{
				Tool: req.Tool, SessionID: req.SessionID, Version: req.Version,
				EventIDs: []string{"e1", "e2"},
				Calls: []portal.ProvenanceCall{
					{EventID: "e1", Kind: portal.ProvenanceKindSQL, Tool: "trino_query", Statement: "SELECT 1"},
					{EventID: "e2", Kind: portal.ProvenanceKindAPI, Tool: "api_invoke_endpoint", Method: "GET"},
				},
			}
		},
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", SessionID: "sess1",
	})

	input := saveAssetInput{
		Name: "Chart", Content: "<svg/>", ContentType: "image/svg+xml",
		Sources: []string{"mcp:call:e1", "e2"},
	}

	result, _, err := tk.handleSaveAsset(ctx, nil, input)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	assert.Equal(t, SaveToolName, captured.Tool)
	assert.Equal(t, "sess1", captured.SessionID)
	assert.Equal(t, "user1", captured.UserID)
	assert.Equal(t, []string{"mcp:call:e1", "e2"}, captured.Sources,
		"the cited sources reach the capturer verbatim; it owns parsing them")
	assert.Equal(t, 1, captured.Version)

	var output saveAssetOutput
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &output))
	assert.True(t, output.ProvenanceCaptured)
	assert.Equal(t, 2, output.CallsRecorded)

	stored, err := store.Get(ctx, output.AssetID)
	require.NoError(t, err)
	require.Len(t, stored.Provenance.Captures, 1)
	assert.Equal(t, []string{"e1", "e2"}, stored.Provenance.Captures[0].EventIDs)
	assert.Equal(t, portal.ProvenanceKindSQL, stored.Provenance.Captures[0].Calls[0].Kind)
}

// A deployment with no audit log to read still saves the asset; it just
// records no calls.
func TestSaveAsset_WithoutCapturer(t *testing.T) {
	store := newInMemoryAssetStore()
	tk := New(Config{Name: "test", AssetStore: store, S3Client: &mockS3Client{}, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", SessionID: "sess1",
	})
	result, _, err := tk.handleSaveAsset(ctx, nil, saveAssetInput{
		Name: "Chart", Content: "<svg/>", ContentType: "image/svg+xml",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	var output saveAssetOutput
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &output))
	assert.False(t, output.ProvenanceCaptured)
	assert.Equal(t, 0, output.CallsRecorded)

	stored, err := store.Get(ctx, output.AssetID)
	require.NoError(t, err)
	assert.Empty(t, stored.Provenance.Captures)
	assert.Equal(t, "sess1", stored.Provenance.SessionID)
}

// A capture that resolved no calls records no capture at all: an empty capture
// would claim the asset was built from nothing.
func TestSaveAsset_EmptyCaptureRecordsNothing(t *testing.T) {
	store := newInMemoryAssetStore()
	tk := New(Config{
		Name: "test", AssetStore: store, S3Client: &mockS3Client{}, S3Bucket: "bucket",
		CaptureProvenance: func(_ context.Context, req portal.ProvenanceRequest) portal.ProvenanceCapture {
			return portal.ProvenanceCapture{Tool: req.Tool}
		},
	})
	result, _, err := tk.handleSaveAsset(context.Background(), nil, saveAssetInput{
		Name: "Chart", Content: "<svg/>", ContentType: "image/svg+xml",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	var output saveAssetOutput
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &output))
	assert.False(t, output.ProvenanceCaptured)
}

func TestManageAsset_List(t *testing.T) {
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

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "list"})
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

func TestManageAsset_Get(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Test", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: "get", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestManageAsset_GetMissing(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Bucket: "bucket"})

	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: "get", AssetID: "missing",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestManageAsset_Update(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Old", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1", Name: "New Name",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	asset, _ := store.Get(context.Background(), "a1")
	assert.Equal(t, "New Name", asset.Name)
}

func TestManageAsset_UpdateWithContent(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Old", ContentType: "text/html",
		Tags: []string{}, Provenance: portal.Provenance{},
	})

	versions := newInMemoryVersionStore()
	s3 := &mockS3Client{}
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: versions, S3Client: s3,
		S3Bucket: "bucket", S3Prefix: "assets/",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	// Content-only update: no metadata fields. The content write commits a new
	// version; the handler must not then run the empty metadata Update, which the
	// store rejects with "no fields to update", reporting failure on a write that
	// actually committed (#573).
	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1", Content: "<div>Updated</div>",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError, "content-only update must succeed")

	vv, _, _ := versions.ListByAsset(context.Background(), "a1", 0, 0)
	assert.Len(t, vv, 1, "content update must create exactly one version")
}

// Updating an asset's content is new work with its own sources, so it appends
// a capture rather than leaving the asset carrying only what its first version
// was built from (#1320).
func TestManageAsset_ContentWriteAppendsACapture(t *testing.T) {
	tests := []struct {
		name  string
		input manageAssetInput
	}{
		{
			name:  "update",
			input: manageAssetInput{Action: "update", AssetID: "a1", Content: "# Updated", Sources: []string{"mcp:call:e7"}},
		},
		{
			name: "patch",
			input: manageAssetInput{
				Action: "patch", AssetID: "a1", Sources: []string{"mcp:call:e7"},
				Edits: []textpatch.Edit{{Find: "Report", Replace: "Revised report"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newInMemoryAssetStore()
			_ = store.Insert(context.Background(), portal.Asset{
				ID: "a1", OwnerID: "user1", Name: "Old", ContentType: "text/markdown",
				S3Bucket: "bucket", S3Key: "assets/user1/a1/content.md",
				CurrentVersion: 1, Tags: []string{},
				Provenance: portal.Provenance{Captures: []portal.ProvenanceCapture{
					{Tool: SaveToolName, Version: 1, EventIDs: []string{"e1"}, Calls: []portal.ProvenanceCall{{EventID: "e1"}}},
				}},
			})

			var captured portal.ProvenanceRequest
			tk := New(Config{
				Name: "test", AssetStore: store, VersionStore: newLinkedVersionStore(store),
				S3Client: &mockS3Client{getBody: []byte("# Report"), getCT: "text/markdown"}, S3Bucket: "bucket", S3Prefix: "assets/",
				CaptureProvenance: func(_ context.Context, req portal.ProvenanceRequest) portal.ProvenanceCapture {
					captured = req
					return portal.ProvenanceCapture{
						Tool: req.Tool, Version: req.Version, Explicit: true,
						EventIDs: []string{"e7"},
						Calls:    []portal.ProvenanceCall{{EventID: "e7", Kind: portal.ProvenanceKindSQL, Tool: "trino_query"}},
					}
				},
			})

			ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
				UserID: "user1", SessionID: "sess1",
			})
			result, _, err := tk.handleManageAsset(ctx, nil, tt.input)
			require.NoError(t, err)
			require.False(t, result.IsError, "content write must succeed")

			assert.Equal(t, ManageToolName, captured.Tool)
			assert.Equal(t, []string{"mcp:call:e7"}, captured.Sources)
			assert.Equal(t, "sess1", captured.SessionID)
			assert.Equal(t, "user1", captured.UserID)
			assert.Equal(t, 1, captured.Version,
				"the capture names the version the write produced (the fixture's first recorded version)")

			stored, err := store.Get(ctx, "a1")
			require.NoError(t, err)
			require.Len(t, stored.Provenance.Captures, 2, "the first version's capture is kept")
			assert.Equal(t, []string{"e1"}, stored.Provenance.Captures[0].EventIDs)
			assert.Equal(t, []string{"e7"}, stored.Provenance.Captures[1].EventIDs)
			assert.Equal(t, captured.Version, stored.Provenance.Captures[1].Version)
		})
	}
}

// The content is already written when the capture is appended, so a store that
// refuses the append must not turn a committed edit into a reported failure.
func TestManageAsset_ContentWriteSurvivesAFailedCapture(t *testing.T) {
	store := &captureErrorAssetStore{inMemoryAssetStore: newInMemoryAssetStore()}
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Old", ContentType: "text/markdown",
		CurrentVersion: 1, Tags: []string{}, Provenance: portal.Provenance{},
	})
	versions := newInMemoryVersionStore()
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: versions,
		S3Client: &mockS3Client{}, S3Bucket: "bucket", S3Prefix: "assets/",
		CaptureProvenance: func(_ context.Context, req portal.ProvenanceRequest) portal.ProvenanceCapture {
			return portal.ProvenanceCapture{Tool: req.Tool, Calls: []portal.ProvenanceCall{{EventID: "e1"}}}
		},
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})
	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1", Content: "# Updated",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	vv, _, _ := versions.ListByAsset(context.Background(), "a1", 0, 0)
	assert.Len(t, vv, 1, "the new version stands even though its provenance could not be recorded")
}

// captureErrorAssetStore refuses to record a provenance capture.
type captureErrorAssetStore struct {
	*inMemoryAssetStore
}

func (*captureErrorAssetStore) AppendProvenanceCapture(context.Context, string, portal.ProvenanceCapture) error {
	return errors.New("provenance column locked")
}

func TestManageAsset_UpdateNoFields(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Old", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	versions := newInMemoryVersionStore()
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: versions, S3Bucket: "bucket",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	// Neither content nor metadata: a genuine no-op must report an error and
	// create no version.
	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError, "empty update must report an error")

	vv, _, _ := versions.ListByAsset(context.Background(), "a1", 0, 0)
	assert.Empty(t, vv, "empty update must not create a version")
}

func TestManageAsset_UpdateContentAndMetadata(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Old", ContentType: "text/html",
		Tags: []string{}, Provenance: portal.Provenance{},
	})

	versions := newInMemoryVersionStore()
	s3 := &mockS3Client{}
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: versions, S3Client: s3,
		S3Bucket: "bucket", S3Prefix: "assets/",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1", Content: "<div>v2</div>", Name: "New Name",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	asset, _ := store.Get(context.Background(), "a1")
	assert.Equal(t, "New Name", asset.Name, "metadata must be applied alongside content")
	vv, _, _ := versions.ListByAsset(context.Background(), "a1", 0, 0)
	assert.Len(t, vv, 1, "content+metadata update must create one version")
}

func TestManageAsset_UpdateWrongOwner(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Mine", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user2"})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1", Name: "Hijacked",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// TestManageAsset_UpdateAdminAnyOwner is the #1042 asset-side regression for the
// update verb: an admin updates an asset they do not own.
func TestManageAsset_UpdateAdminAnyOwner(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Mine", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(),
		&middleware.PlatformContext{UserID: "operator", IsAdmin: true})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1", Name: "Renamed by admin",
	})
	require.NoError(t, err)
	require.False(t, result.IsError, errorText(t, result))

	got, getErr := store.Get(context.Background(), "a1")
	require.NoError(t, getErr)
	assert.Equal(t, "Renamed by admin", got.Name)
}

func TestManageAsset_Delete(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "To Delete", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "delete", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	asset, getErr := store.Get(context.Background(), "a1")
	require.NoError(t, getErr)
	assert.NotNil(t, asset.DeletedAt)
}

func TestManageAsset_InvalidAction(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{Action: "invalid"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestManageAsset_MissingAssetID(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	for _, action := range []string{"get", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{Action: action})
			require.NoError(t, err)
			assert.True(t, result.IsError)
		})
	}
}

func TestBuildSaveOutput(t *testing.T) {
	tk := New(Config{Name: "test", BaseURL: "https://example.com", S3Bucket: "bucket"})
	out := tk.buildSaveOutput("abc123", portal.Provenance{
		Captures: []portal.ProvenanceCapture{{Calls: []portal.ProvenanceCall{{Tool: "trino_query"}}}},
	}, 0)

	assert.Equal(t, "abc123", out.AssetID)
	assert.Equal(t, "https://example.com/portal/assets/abc123", out.PortalURL)
	assert.True(t, out.ProvenanceCaptured)
	assert.Equal(t, 1, out.CallsRecorded)
}

func TestBuildSaveOutputNoBaseURL(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})
	out := tk.buildSaveOutput("abc123", portal.Provenance{}, 0)

	assert.Empty(t, out.PortalURL)
	assert.False(t, out.ProvenanceCaptured)
}

// A cited source that named no call of the caller's is not silently dropped:
// the agent is told how many of the ids it gave were recorded.
func TestBuildSaveOutputReportsUnresolvedSources(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})
	out := tk.buildSaveOutput("abc123", portal.Provenance{
		Captures: []portal.ProvenanceCapture{{Calls: []portal.ProvenanceCall{{Tool: "trino_query"}}}},
	}, 3)

	assert.Equal(t, 1, out.CallsRecorded)
	assert.Contains(t, out.Message, "1 of the 3 cited sources were recorded")
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
			assert.Equal(t, tt.ext, portal.ExtensionForContentType(tt.ct))
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
	if !ok || a.DeletedAt != nil {
		return notFoundError{}
	}
	// Mirror the real store's applyUpdateFields guard: an update with no fields
	// set is rejected. Without this the mock rubber-stamps an empty update that
	// real Postgres rejects, hiding the content-only update bug (#573).
	if updates.Name == nil && updates.Description == nil && updates.Tags == nil &&
		updates.ContentType == "" && updates.S3Key == "" && !updates.HasContent &&
		updates.ThumbnailS3Key == nil && updates.MaxVersions == nil && !updates.ClearMaxVersions {
		return fmt.Errorf("no fields to update")
	}
	if updates.Name != nil {
		a.Name = *updates.Name
	}
	if updates.Description != nil {
		a.Description = *updates.Description
	}
	if updates.Tags != nil {
		a.Tags = updates.Tags
	}
	// Mirror applyScalarUpdates' retention arm, clear winning over set, so a
	// retention-only edit is a complete update here as it is in Postgres.
	switch {
	case updates.ClearMaxVersions:
		a.MaxVersions = nil
	case updates.MaxVersions != nil:
		a.MaxVersions = updates.MaxVersions
	}
	// Mirror applyScalarUpdates: the real store writes content_type. Dropping
	// it here would hide a content-type move that the viewer depends on.
	if updates.ContentType != "" {
		a.ContentType = updates.ContentType
	}
	if updates.S3Key != "" {
		a.S3Key = updates.S3Key
	}
	s.assets[id] = a
	return nil
}

func (s *inMemoryAssetStore) AppendProvenanceCapture(_ context.Context, id string, capture portal.ProvenanceCapture) error {
	a, ok := s.assets[id]
	if !ok || a.DeletedAt != nil {
		return notFoundError{}
	}
	a.Provenance.Captures = append(a.Provenance.Captures, capture)
	s.assets[id] = a
	return nil
}

func (s *inMemoryAssetStore) SoftDelete(_ context.Context, id string) error {
	a, ok := s.assets[id]
	if !ok || a.DeletedAt != nil {
		return notFoundError{}
	}
	now := time.Now()
	a.DeletedAt = &now
	s.assets[id] = a
	return nil
}

func (s *inMemoryAssetStore) GetByIDs(_ context.Context, ids []string) (map[string]*portal.Asset, error) {
	result := make(map[string]*portal.Asset, len(ids))
	for _, id := range ids {
		if a, ok := s.assets[id]; ok && a.DeletedAt == nil {
			asset := a
			result[id] = &asset
		}
	}
	return result, nil
}

func (s *inMemoryAssetStore) GetByIdempotencyKey(_ context.Context, ownerID, key string) (*portal.Asset, error) {
	for _, a := range s.assets {
		if a.OwnerID == ownerID && a.IdempotencyKey == key && a.DeletedAt == nil {
			asset := a
			return &asset, nil
		}
	}
	return nil, notFoundError{}
}

func TestRegisterTools(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	// AddTool validates each tool's input schema, so a bad manageFeedbackSchema
	// would panic here.
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	tk.RegisterTools(server)

	// Verify tools are registered by checking Tools() returns them.
	tools := tk.Tools()
	assert.Contains(t, tools, SaveToolName)
	assert.Contains(t, tools, ManageToolName)
	assert.Contains(t, tools, feedbackToolName)
}

// TestToolsListVocabulary is the #1029 acceptance check on the advertised
// surface: a real tools/list response names the portal tools save_asset and
// manage_asset, and no portal tool's name, description, or input schema uses
// the word "artifact" anywhere the model reads it.
func TestToolsListVocabulary(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	tk.RegisterTools(server)

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		schemaJSON, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		surface := tool.Name + "\n" + tool.Title + "\n" + tool.Description + "\n" + string(schemaJSON)
		assert.NotContains(t, strings.ToLower(surface), "artifact",
			"advertised surface of %q must not use the word artifact", tool.Name)
	}
	assert.Contains(t, names, SaveToolName)
	assert.Contains(t, names, ManageToolName)
	assert.NotContains(t, names, "save_artifact")
	assert.NotContains(t, names, "manage_artifact")
}

func TestSetProviders(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	// SetSemanticProvider and SetQueryProvider should not panic.
	tk.SetSemanticProvider(nil)
	tk.SetQueryProvider(nil)

	// Close should return nil.
	assert.NoError(t, tk.Close())
}

func TestSaveAsset_S3Error(t *testing.T) {
	s3 := &mockS3Client{putErr: notFoundError{}}
	tk := New(Config{
		Name: "test", AssetStore: newInMemoryAssetStore(), S3Client: s3,
		S3Bucket: "bucket", S3Prefix: "assets/",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", SessionID: "sess1",
	})

	input := saveAssetInput{
		Name: "Test", Content: "<div/>", ContentType: "text/html",
	}

	result, _, err := tk.handleSaveAsset(ctx, nil, input)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to upload content")
}

func TestSaveAsset_NoContext(t *testing.T) {
	store := newInMemoryAssetStore()
	tk := New(Config{Name: "test", AssetStore: store, S3Client: &mockS3Client{}, S3Bucket: "bucket"})

	// Call without PlatformContext — should default to anonymous.
	input := saveAssetInput{
		Name: "Test", Content: "<div/>", ContentType: "text/html",
	}

	result, _, err := tk.handleSaveAsset(context.Background(), nil, input)
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestSaveAsset_NilS3Client(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Client: nil, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", SessionID: "sess1",
	})

	input := saveAssetInput{
		Name: "Test", Content: "<div/>", ContentType: "text/html",
	}

	result, _, err := tk.handleSaveAsset(ctx, nil, input)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "content storage not configured")
}

func TestManageAsset_DeleteWrongOwner(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Mine", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user2"})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "delete", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// TestManageAsset_DeleteAdminAnyOwner is the #1042 asset-side regression for the
// delete verb: an admin deletes an asset they do not own.
func TestManageAsset_DeleteAdminAnyOwner(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Mine", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(),
		&middleware.PlatformContext{UserID: "operator", IsAdmin: true})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "delete", AssetID: "a1",
	})
	require.NoError(t, err)
	require.False(t, result.IsError, errorText(t, result))

	got, getErr := store.Get(context.Background(), "a1")
	require.NoError(t, getErr)
	assert.NotNil(t, got.DeletedAt)
}

func TestManageAsset_DeleteNotFound(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Bucket: "bucket"})

	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: "delete", AssetID: "missing",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestManageAsset_GetDeletedAsset(t *testing.T) {
	store := newInMemoryAssetStore()
	now := time.Now()
	store.assets["a1"] = portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Deleted", Tags: []string{},
		Provenance: portal.Provenance{}, DeletedAt: &now,
	}

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: "get", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "deleted")
}

func TestManageAsset_ListNoContext(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "anonymous", Name: "Anon Asset", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	// Call without PlatformContext — should default to "anonymous".
	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{Action: "list"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestSaveAsset_ValidationDescription(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	longDesc := make([]byte, 2001)
	for i := range longDesc {
		longDesc[i] = 'a'
	}

	result, _, err := tk.handleSaveAsset(context.Background(), nil, saveAssetInput{
		Name: "Test", Content: "x", ContentType: "text/html",
		Description: string(longDesc),
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestSaveAsset_ValidationTags(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})

	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = "tag"
	}

	result, _, err := tk.handleSaveAsset(context.Background(), nil, saveAssetInput{
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

func TestSaveAsset_StoreInsertError(t *testing.T) {
	store := &errorAssetStore{insertErr: notFoundError{}}
	store.assets = make(map[string]portal.Asset)
	tk := New(Config{Name: "test", AssetStore: store, S3Client: &mockS3Client{}, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", SessionID: "sess1",
	})

	input := saveAssetInput{
		Name: "Test", Content: "<div/>", ContentType: "text/html",
	}

	result, _, err := tk.handleSaveAsset(ctx, nil, input)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to save asset metadata")
}

func TestManageAsset_ListError(t *testing.T) {
	store := &errorAssetStore{listErr: notFoundError{}}
	store.assets = make(map[string]portal.Asset)
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "list"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to list assets")
}

func TestManageAsset_UpdateStoreError(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Test", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	errStore := &errorAssetStore{updateErr: notFoundError{}}
	errStore.assets = store.assets
	tk := New(Config{Name: "test", AssetStore: errStore, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1", Name: "New",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to update asset")
}

func TestManageAsset_UpdateNilS3Client(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Test", ContentType: "text/html",
		Tags: []string{}, Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Client: nil, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1", Content: "<div>Updated</div>",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "content storage not configured")
}

func TestManageAsset_UpdateWithContentError(t *testing.T) {
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

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1", Content: "<div>Updated</div>",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to upload new content")
}

func TestSaveAsset_ContentTooLarge(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket", MaxContentSize: 10})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", SessionID: "sess1",
	})

	input := saveAssetInput{
		Name: "Test", Content: "12345678901", ContentType: "text/html", // 11 bytes > 10
	}

	result, _, err := tk.handleSaveAsset(ctx, nil, input)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "exceeds maximum")
}

func TestManageAsset_UpdateContentTooLarge(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Test", ContentType: "text/html",
		Tags: []string{}, Provenance: portal.Provenance{},
	})

	tk := New(Config{
		Name: "test", AssetStore: store, S3Bucket: "bucket", MaxContentSize: 10,
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "update", AssetID: "a1", Content: "12345678901", // 11 bytes > 10
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "exceeds maximum")
}

func TestManageAsset_UpdateNoAuthDenied(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Mine", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	// No PlatformContext — resolveOwnerID returns "anonymous" which != "user1"
	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: "update", AssetID: "a1", Name: "Hijacked",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "you can only update your own assets")
}

func TestManageAsset_DeleteNoAuthDenied(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Mine", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	// No PlatformContext — resolveOwnerID returns "anonymous" which != "user1"
	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: "delete", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "you can only delete your own assets")
}

func TestResolveOwnerEmail(t *testing.T) {
	// With email in context
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserEmail: "test@example.com",
	})
	assert.Equal(t, "test@example.com", resolveOwnerEmail(ctx))

	// Empty email falls back to "anonymous"
	ctx = middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{})
	assert.Equal(t, "anonymous", resolveOwnerEmail(ctx))

	// No platform context falls back to "anonymous"
	assert.Equal(t, "anonymous", resolveOwnerEmail(context.Background()))
}

func TestManageAsset_SoftDeleteError(t *testing.T) {
	store := newInMemoryAssetStore()
	_ = store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Test", Tags: []string{},
		Provenance: portal.Provenance{},
	})

	errStore := &errorAssetStore{softDelErr: notFoundError{}}
	errStore.assets = store.assets
	tk := New(Config{Name: "test", AssetStore: errStore, S3Bucket: "bucket"})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: "delete", AssetID: "a1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	assert.Contains(t, tc.Text, "failed to delete asset")
}

// --- Prompt tests ---

func TestPromptInfos(t *testing.T) {
	tk := &Toolkit{}
	infos := tk.PromptInfos()
	require.Len(t, infos, 2)

	assert.Equal(t, saveAssetPromptName, infos[0].Name)
	assert.NotEmpty(t, infos[0].Description)
	assert.Equal(t, "toolkit", infos[0].Category)

	assert.Equal(t, showAssetsPromptName, infos[1].Name)
	assert.NotEmpty(t, infos[1].Description)
	assert.Equal(t, "toolkit", infos[1].Category)
}

func TestRegisterPrompts(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)

	tk := &Toolkit{}
	tk.registerPrompts(s)

	// Connect an in-memory client
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSess, err := s.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSess.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	clientSess, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = clientSess.Close() }()

	// List prompts
	listResp, err := clientSess.ListPrompts(ctx, &mcp.ListPromptsParams{})
	require.NoError(t, err)
	require.Len(t, listResp.Prompts, 2)

	names := make(map[string]bool)
	for _, p := range listResp.Prompts {
		names[p.Name] = true
	}
	assert.True(t, names[saveAssetPromptName])
	assert.True(t, names[showAssetsPromptName])

	// Get each prompt and verify content
	for _, name := range []string{saveAssetPromptName, showAssetsPromptName} {
		resp, err := clientSess.GetPrompt(ctx, &mcp.GetPromptParams{Name: name})
		require.NoError(t, err, "prompt %s", name)
		require.Len(t, resp.Messages, 1)
		textContent, ok := resp.Messages[0].Content.(*mcp.TextContent)
		require.True(t, ok)
		assert.NotEmpty(t, textContent.Text)
	}
}

// --- In-memory VersionStore for testing ---

type inMemoryVersionStore struct {
	versions map[string][]portal.AssetVersion
	// assets, when set, receives the current-version pointer update the real
	// store performs in the same transaction as the version insert. Without it
	// the double reports success while leaving the asset row on its old
	// s3_key, content_type and size — hiding every bug that depends on the
	// pointer actually moving.
	assets *inMemoryAssetStore
}

func newInMemoryVersionStore() *inMemoryVersionStore {
	return &inMemoryVersionStore{versions: make(map[string][]portal.AssetVersion)}
}

// newLinkedVersionStore returns a version store that advances the given asset
// store's rows, mirroring postgresVersionStore.CreateVersion.
func newLinkedVersionStore(assets *inMemoryAssetStore) *inMemoryVersionStore {
	return &inMemoryVersionStore{versions: make(map[string][]portal.AssetVersion), assets: assets}
}

func (s *inMemoryVersionStore) CreateVersion(_ context.Context, v portal.AssetVersion) (int, error) {
	// Simulate auto-incrementing version number
	maxVer := 0
	for _, existing := range s.versions[v.AssetID] {
		if existing.Version > maxVer {
			maxVer = existing.Version
		}
	}
	nextVer := maxVer + 1
	v.Version = nextVer
	s.versions[v.AssetID] = append(s.versions[v.AssetID], v)

	if s.assets != nil {
		if a, ok := s.assets.assets[v.AssetID]; ok {
			a.CurrentVersion = nextVer
			a.S3Key = v.S3Key
			a.ContentType = v.ContentType
			a.SizeBytes = v.SizeBytes
			s.assets.assets[v.AssetID] = a
		}
	}
	return nextVer, nil
}

func (s *inMemoryVersionStore) ListByAsset(_ context.Context, assetID string, _, _ int) ([]portal.AssetVersion, int, error) {
	vv := s.versions[assetID]
	return vv, len(vv), nil
}

func (s *inMemoryVersionStore) GetByVersion(_ context.Context, assetID string, version int) (*portal.AssetVersion, error) {
	for _, v := range s.versions[assetID] {
		if v.Version == version {
			return &v, nil
		}
	}
	return nil, notFoundError{}
}

func (s *inMemoryVersionStore) GetLatest(_ context.Context, assetID string) (*portal.AssetVersion, error) {
	vv := s.versions[assetID]
	if len(vv) == 0 {
		return nil, notFoundError{}
	}
	return &vv[len(vv)-1], nil
}

// --- Version action tests ---

func TestHandleListVersions(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: vs,
		S3Client: &mockS3Client{}, S3Bucket: "bucket",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	// Insert an asset with a version.
	asset := portal.Asset{ID: "a1", OwnerID: "user1", CurrentVersion: 1}
	require.NoError(t, store.Insert(ctx, asset))
	_, cvErr := vs.CreateVersion(ctx, portal.AssetVersion{
		ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "bucket", ContentType: "text/html", SizeBytes: 10,
	})
	require.NoError(t, cvErr)

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "list_versions", AssetID: "a1"})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var parsed map[string]any
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &parsed))
	assert.Equal(t, float64(1), parsed["total"])
}

func TestHandleListVersionsMissingAssetID(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})
	ctx := context.Background()
	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "list_versions"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRevert(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	s3 := &mockS3Client{getBody: []byte("<html>v1</html>"), getCT: "text/html"}
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: vs,
		S3Client: s3, S3Bucket: "bucket", S3Prefix: "assets/",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})

	asset := portal.Asset{ID: "a1", OwnerID: "user1", CurrentVersion: 2}
	require.NoError(t, store.Insert(ctx, asset))
	_, cvErr := vs.CreateVersion(ctx, portal.AssetVersion{
		ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "bucket", ContentType: "text/html", SizeBytes: 10,
	})
	require.NoError(t, cvErr)

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "revert", AssetID: "a1", Version: 1})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var parsed map[string]any
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &parsed))
	assert.Equal(t, float64(2), parsed["version"])
}

// TestHandleRevertAdminAnyOwner is the #1042 asset-side regression for the
// revert verb: an admin reverts an asset they do not own.
func TestHandleRevertAdminAnyOwner(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	s3 := &mockS3Client{getBody: []byte("<html>v1</html>"), getCT: "text/html"}
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: vs,
		S3Client: s3, S3Bucket: "bucket", S3Prefix: "assets/",
	})

	setupCtx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})
	require.NoError(t, store.Insert(setupCtx, portal.Asset{ID: "a1", OwnerID: "user1", CurrentVersion: 2}))
	_, cvErr := vs.CreateVersion(setupCtx, portal.AssetVersion{
		ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "bucket", ContentType: "text/html", SizeBytes: 10,
	})
	require.NoError(t, cvErr)

	adminCtx := middleware.WithPlatformContext(context.Background(),
		&middleware.PlatformContext{UserID: "operator", IsAdmin: true})
	result, _, err := tk.handleManageAsset(adminCtx, nil, manageAssetInput{Action: "revert", AssetID: "a1", Version: 1})
	require.NoError(t, err)
	assert.False(t, result.IsError, errorText(t, result))
}

func TestHandleRevertMissingAssetID(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})
	ctx := context.Background()
	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "revert"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRevertMissingVersion(t *testing.T) {
	tk := New(Config{Name: "test", S3Bucket: "bucket"})
	ctx := context.Background()
	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "revert", AssetID: "a1"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRevertNotOwner(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: vs,
		S3Client: &mockS3Client{}, S3Bucket: "bucket",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user2"})
	require.NoError(t, store.Insert(ctx, portal.Asset{ID: "a1", OwnerID: "user1", CurrentVersion: 1}))

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "revert", AssetID: "a1", Version: 1})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRevertVersionNotFound(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: vs,
		S3Client: &mockS3Client{}, S3Bucket: "bucket",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})
	require.NoError(t, store.Insert(ctx, portal.Asset{ID: "a1", OwnerID: "user1", CurrentVersion: 1}))

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "revert", AssetID: "a1", Version: 99})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRevertNoS3Client(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: vs,
		S3Client: nil, S3Bucket: "bucket",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})
	require.NoError(t, store.Insert(ctx, portal.Asset{ID: "a1", OwnerID: "user1", CurrentVersion: 1}))
	_, cvErr := vs.CreateVersion(ctx, portal.AssetVersion{
		ID: "v1", AssetID: "a1", S3Key: "k1", S3Bucket: "bucket", ContentType: "text/html", SizeBytes: 10,
	})
	require.NoError(t, cvErr)

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "revert", AssetID: "a1", Version: 1})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRevertS3GetError(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	s3 := &mockS3Client{getErr: fmt.Errorf("s3 error")}
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: vs,
		S3Client: s3, S3Bucket: "bucket",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})
	require.NoError(t, store.Insert(ctx, portal.Asset{ID: "a1", OwnerID: "user1", CurrentVersion: 1}))
	_, cvErr := vs.CreateVersion(ctx, portal.AssetVersion{
		ID: "v1", AssetID: "a1", S3Key: "k1", S3Bucket: "bucket", ContentType: "text/html", SizeBytes: 10,
	})
	require.NoError(t, cvErr)

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "revert", AssetID: "a1", Version: 1})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRevertS3PutError(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	s3 := &mockS3Client{getBody: []byte("data"), putErr: fmt.Errorf("s3 error")}
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: vs,
		S3Client: s3, S3Bucket: "bucket", S3Prefix: "assets/",
	})

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})
	require.NoError(t, store.Insert(ctx, portal.Asset{ID: "a1", OwnerID: "user1", CurrentVersion: 1}))
	_, cvErr := vs.CreateVersion(ctx, portal.AssetVersion{
		ID: "v1", AssetID: "a1", S3Key: "k1", S3Bucket: "bucket", ContentType: "text/html", SizeBytes: 10,
	})
	require.NoError(t, cvErr)

	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "revert", AssetID: "a1", Version: 1})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestCleanupOrphanedS3NilClient(t *testing.T) { //nolint:revive // test function signature
	t.Parallel()
	tk := New(Config{Name: "test", S3Client: nil, S3Bucket: "bucket"})
	// Should not panic with nil S3 client
	tk.cleanupOrphanedS3(context.Background(), "bucket", "key")
}

func TestCleanupOrphanedS3DeleteError(t *testing.T) { //nolint:revive // test function signature
	t.Parallel()
	s3 := &mockS3Client{deleteErr: fmt.Errorf("delete error")}
	tk := New(Config{Name: "test", S3Client: s3, S3Bucket: "bucket"})
	// Should log but not panic
	tk.cleanupOrphanedS3(context.Background(), "bucket", "key")
}

func TestCleanupOrphanedS3Success(t *testing.T) { //nolint:revive // test function signature
	t.Parallel()
	s3 := &mockS3Client{}
	tk := New(Config{Name: "test", S3Client: s3, S3Bucket: "bucket"})
	tk.cleanupOrphanedS3(context.Background(), "bucket", "key")
}
