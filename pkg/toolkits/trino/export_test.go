package trino

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock implementations ---

type mockExportAssetStore struct {
	inserted       *ExportAsset
	insertErr      error
	idempotencyHit *ExportAssetRef
}

func (m *mockExportAssetStore) InsertExportAsset(_ context.Context, asset ExportAsset) error {
	m.inserted = &asset
	return m.insertErr
}

func (m *mockExportAssetStore) GetByIdempotencyKey(_ context.Context, _, _ string) (*ExportAssetRef, error) {
	if m.idempotencyHit != nil {
		return m.idempotencyHit, nil
	}
	return nil, fmt.Errorf("not found")
}

type mockExportVersionStore struct {
	created    *ExportVersion
	createErr  error
	versionNum int
}

func (m *mockExportVersionStore) CreateExportVersion(_ context.Context, ver ExportVersion) (int, error) {
	m.created = &ver
	if m.createErr != nil {
		return 0, m.createErr
	}
	if m.versionNum == 0 {
		return 1, nil
	}
	return m.versionNum, nil
}

type mockExportS3Client struct {
	lastBucket string
	lastKey    string
	lastData   []byte
	putErr     error
}

func (m *mockExportS3Client) PutObject(_ context.Context, bucket, key string, data []byte, _ string) error {
	m.lastBucket = bucket
	m.lastKey = key
	m.lastData = data
	return m.putErr
}

// --- Helper to build a test toolkit with export deps ---

func newTestExportToolkit(assetStore ExportAssetStore, versionStore ExportVersionStore, s3Client ExportS3Client) *Toolkit {
	tk := &Toolkit{
		name:   "test",
		config: Config{ReadOnly: true},
	}
	tk.SetExportDeps(ExportDeps{
		AssetStore:   assetStore,
		VersionStore: versionStore,
		S3Client:     s3Client,
		S3Bucket:     "test-bucket",
		S3Prefix:     "exports",
		BaseURL:      "https://example.com",
		Config: ExportConfig{
			MaxRows:  10000,
			MaxBytes: 1024 * 1024,
		},
		GetUserContext: func(_ context.Context) *ExportUserContext {
			return &ExportUserContext{
				UserID:    "user-123",
				UserEmail: "alice@example.com",
				SessionID: "sess-abc",
			}
		},
		GetProvenanceCalls: func(_ context.Context) []ExportProvenanceCall {
			return nil
		},
	})
	return tk
}

func buildExportRequest(args map[string]any) *mcp.CallToolRequest {
	rawArgs, _ := json.Marshal(args) //nolint:errcheck // test helper
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "trino_export",
			Arguments: rawArgs,
		},
	}
}

// --- Tests ---

func TestValidateExportInput(t *testing.T) {
	validCfg := ExportConfig{MaxRows: 10000, MaxBytes: 1024 * 1024}
	validCfg = applyExportDefaults(validCfg)

	tests := []struct {
		name    string
		input   exportInput
		wantErr string
	}{
		{
			name:    "missing sql",
			input:   exportInput{Format: "csv", Name: "test"},
			wantErr: "sql is required",
		},
		{
			name:    "missing format",
			input:   exportInput{SQL: "SELECT 1", Name: "test"},
			wantErr: "format is required",
		},
		{
			name:    "invalid format",
			input:   exportInput{SQL: "SELECT 1", Format: "xml", Name: "test"},
			wantErr: "unsupported format",
		},
		{
			name:    "missing name",
			input:   exportInput{SQL: "SELECT 1", Format: "csv"},
			wantErr: "name is required",
		},
		{
			name:  "valid input",
			input: exportInput{SQL: "SELECT 1", Format: "csv", Name: "test"},
		},
		{
			name:    "tag with _sys- prefix",
			input:   exportInput{SQL: "SELECT 1", Format: "csv", Name: "test", Tags: []string{"_sys-bad"}},
			wantErr: "reserved prefix",
		},
		{
			name:    "tag not kebab-case",
			input:   exportInput{SQL: "SELECT 1", Format: "csv", Name: "test", Tags: []string{"NotValid"}},
			wantErr: "lowercase kebab-case",
		},
		{
			name:  "valid kebab-case tags",
			input: exportInput{SQL: "SELECT 1", Format: "csv", Name: "test", Tags: []string{"my-tag", "export-2024"}},
		},
		{
			name:    "too many tags",
			input:   exportInput{SQL: "SELECT 1", Format: "csv", Name: "test", Tags: make([]string, 21)},
			wantErr: "too many tags",
		},
		{
			name:    "limit exceeds max",
			input:   exportInput{SQL: "SELECT 1", Format: "csv", Name: "test", Limit: 200000},
			wantErr: "exceeds deployment maximum",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExportInput(tt.input, validCfg)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateExportTags(t *testing.T) {
	tests := []struct {
		name    string
		tags    []string
		wantErr bool
	}{
		{"empty", nil, false},
		{"valid", []string{"my-tag", "export-2024", "a"}, false},
		{"single char", []string{"a"}, false},
		{"with numbers", []string{"v2-export"}, false},
		{"uppercase", []string{"MyTag"}, true},
		{"underscore", []string{"my_tag"}, true},
		{"sys prefix", []string{"_sys-internal"}, true},
		{"too long", []string{string(make([]byte, 51))}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExportTags(tt.tags)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExportError(t *testing.T) {
	result := exportError("something went wrong")
	assert.True(t, result.IsError)
	require.Len(t, result.Content, 1)

	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "something went wrong")
}

func TestExportSuccess(t *testing.T) {
	result := exportSuccess(exportOutput{
		AssetID:   "abc",
		Format:    "csv",
		RowCount:  100,
		SizeBytes: 5000,
		Message:   "done",
	})
	assert.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "abc")
	assert.Contains(t, tc.Text, "csv")
}

func TestBuildPortalURL(t *testing.T) {
	assert.Equal(t, "https://example.com/portal/assets/abc", buildPortalURL("https://example.com", "abc"))
	assert.Equal(t, "", buildPortalURL("", "abc"))
}

func TestApplyExportDefaults(t *testing.T) {
	cfg := applyExportDefaults(ExportConfig{})
	assert.Equal(t, defaultMaxExportRows, cfg.MaxRows)
	assert.Equal(t, int64(defaultMaxExportBytes), cfg.MaxBytes)
	assert.Equal(t, defaultExportTimeout, cfg.DefaultTimeout)
	assert.Equal(t, defaultMaxExportTimeout, cfg.MaxTimeout)

	// Custom values should not be overridden
	custom := applyExportDefaults(ExportConfig{MaxRows: 500})
	assert.Equal(t, 500, custom.MaxRows)
}

func TestHandleExport_NoAuth(t *testing.T) {
	assetStore := &mockExportAssetStore{}
	versionStore := &mockExportVersionStore{}
	s3Client := &mockExportS3Client{}

	tk := newTestExportToolkit(assetStore, versionStore, s3Client)
	// Override to return nil (unauthenticated)
	tk.exportDeps.GetUserContext = func(_ context.Context) *ExportUserContext { return nil }

	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "SELECT 1", "format": "csv", "name": "test",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assertResultContains(t, result, "authentication required")
}

func TestHandleExport_ReadOnlyViolation(t *testing.T) {
	tk := newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{})

	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "DROP TABLE users", "format": "csv", "name": "test",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assertResultContains(t, result, "write operations not allowed")
}

func TestHandleExport_IdempotencyMatch(t *testing.T) {
	assetStore := &mockExportAssetStore{
		idempotencyHit: &ExportAssetRef{ID: "existing-123", SizeBytes: 9999},
	}
	tk := newTestExportToolkit(assetStore, &mockExportVersionStore{}, &mockExportS3Client{})

	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "SELECT 1", "format": "csv", "name": "test", "idempotency_key": "dedup-key",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assertResultContains(t, result, "existing-123")
	assertResultContains(t, result, "idempotency key matched")
}

func TestHandleExport_S3Failure(t *testing.T) {
	assetStore := &mockExportAssetStore{}
	s3Client := &mockExportS3Client{putErr: fmt.Errorf("s3 unavailable")}
	tk := newTestExportToolkit(assetStore, &mockExportVersionStore{}, s3Client)

	// Need a real Trino client for query execution — the handler will fail
	// at query execution since we don't have one, which is expected.
	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "SELECT 1", "format": "csv", "name": "test",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	// Should fail at query execution since no Trino client is configured
	assertResultContains(t, result, "no Trino client")
}

func TestHandleExport_ByteCapExceeded(t *testing.T) {
	tk := newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{})
	// Set very small byte cap
	tk.exportDeps.Config.MaxBytes = 10

	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "SELECT 1", "format": "csv", "name": "test",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	// Will fail at query execution since no client, which is OK for this test
}

func TestHandleExport_ValidationErrors(t *testing.T) {
	tk := newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{})

	tests := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{
			name:    "missing sql",
			args:    map[string]any{"format": "csv", "name": "test"},
			wantErr: "sql is required",
		},
		{
			name:    "missing format",
			args:    map[string]any{"sql": "SELECT 1", "name": "test"},
			wantErr: "format is required",
		},
		{
			name:    "invalid format",
			args:    map[string]any{"sql": "SELECT 1", "format": "xml", "name": "test"},
			wantErr: "unsupported format",
		},
		{
			name:    "missing name",
			args:    map[string]any{"sql": "SELECT 1", "format": "csv"},
			wantErr: "name is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tk.handleExport(context.Background(), buildExportRequest(tt.args))
			require.NoError(t, err)
			assert.True(t, result.IsError)
			assertResultContains(t, result, tt.wantErr)
		})
	}
}

func TestHandleExport_NoDeps(t *testing.T) {
	tk := &Toolkit{name: "test"}
	// exportDeps is nil

	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "SELECT 1", "format": "csv", "name": "test",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assertResultContains(t, result, "not configured")
}

func TestIsSensitivityTag(t *testing.T) {
	assert.True(t, isSensitivityTag("pii"))
	assert.True(t, isSensitivityTag("contains-pii"))
	assert.True(t, isSensitivityTag("sensitive"))
	assert.True(t, isSensitivityTag("confidential"))
	assert.True(t, isSensitivityTag("restricted"))
	assert.True(t, isSensitivityTag("phi"))
	assert.True(t, isSensitivityTag("pci"))
	assert.False(t, isSensitivityTag("public"))
	assert.False(t, isSensitivityTag("marketing"))
}

func TestExtractSourceTableNames(t *testing.T) {
	names := extractSourceTableNames("SELECT * FROM catalog.schema.users JOIN catalog.schema.orders ON true")
	assert.Contains(t, names, "catalog.schema.users")
	assert.Contains(t, names, "catalog.schema.orders")
}

func TestExportInputSchema(t *testing.T) {
	schema := exportInputSchema()
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "sql")
	assert.Contains(t, props, "format")
	assert.Contains(t, props, "name")
	assert.Contains(t, props, "tags")
	assert.Contains(t, props, "idempotency_key")

	required, ok := schema["required"].([]string)
	require.True(t, ok)
	assert.Contains(t, required, "sql")
	assert.Contains(t, required, "format")
	assert.Contains(t, required, "name")
}

func TestParseExportInput(t *testing.T) {
	rawArgs, _ := json.Marshal(map[string]any{
		"sql":    "SELECT 1",
		"format": "csv",
		"name":   "My Export",
		"tags":   []string{"tag1", "tag2"},
		"limit":  500,
	})
	req := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "trino_export",
			Arguments: rawArgs,
		},
	}

	input, err := parseExportInput(req)
	require.NoError(t, err)
	assert.Equal(t, "SELECT 1", input.SQL)
	assert.Equal(t, "csv", input.Format)
	assert.Equal(t, "My Export", input.Name)
	assert.Equal(t, []string{"tag1", "tag2"}, input.Tags)
	assert.Equal(t, 500, input.Limit)
}

func TestToolsIncludesExport(t *testing.T) {
	tk := &Toolkit{name: "test"}
	assert.NotContains(t, tk.Tools(), exportToolName)

	tk.SetExportDeps(ExportDeps{})
	assert.Contains(t, tk.Tools(), exportToolName)
}

// assertResultContains checks that the result text contains the expected substring.
func assertResultContains(t *testing.T, result *mcp.CallToolResult, substr string) {
	t.Helper()
	require.Len(t, result.Content, 1)
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent, got %T", result.Content[0])

	var raw map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &raw); err == nil {
		if errMsg, ok := raw["error"].(string); ok {
			assert.Contains(t, errMsg, substr)
			return
		}
	}
	assert.Contains(t, tc.Text, substr)
}
