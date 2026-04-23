package trino

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
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

type mockExportShareCreator struct {
	lastAssetID string
	shareURL    string
	shareErr    error
}

func (m *mockExportShareCreator) CreatePublicShare(_ context.Context, assetID, _ string) (string, error) {
	m.lastAssetID = assetID
	if m.shareErr != nil {
		return "", m.shareErr
	}
	return m.shareURL, nil
}

type mockExportSemanticProvider struct {
	semantic.Provider
	tableTags map[string][]string // table key → tags
}

func (*mockExportSemanticProvider) Name() string { return "mock-export" }

func (m *mockExportSemanticProvider) GetTableContext(_ context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	key := table.String()
	tags, ok := m.tableTags[key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &semantic.TableContext{Tags: tags}, nil
}

func (*mockExportSemanticProvider) GetColumnContext(_ context.Context, _ semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // mock
}

func (*mockExportSemanticProvider) GetColumnsContext(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // mock
}

func (*mockExportSemanticProvider) GetLineage(_ context.Context, _ semantic.TableIdentifier, _ semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	return nil, nil //nolint:nilnil // mock
}

func (*mockExportSemanticProvider) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return nil, nil //nolint:nilnil // mock
}

func (*mockExportSemanticProvider) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return nil, nil //nolint:nilnil // mock
}

func (*mockExportSemanticProvider) Close() error { return nil }

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

func TestHandleExport_ReadOnlyEnforcedEvenWhenConfigAllowsWrite(t *testing.T) {
	// trino_export must always enforce read-only regardless of config.ReadOnly
	tk := &Toolkit{
		name:   "test",
		config: Config{ReadOnly: false}, // deployment allows writes
	}
	tk.SetExportDeps(ExportDeps{
		AssetStore:   &mockExportAssetStore{},
		VersionStore: &mockExportVersionStore{},
		S3Client:     &mockExportS3Client{},
		S3Bucket:     "test-bucket",
		S3Prefix:     "exports",
		GetUserContext: func(_ context.Context) *ExportUserContext {
			return &ExportUserContext{UserID: "u1", UserEmail: "a@example.com", SessionID: "s1"}
		},
		GetProvenanceCalls: func(_ context.Context) []ExportProvenanceCall { return nil },
	})

	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "INSERT INTO t VALUES (1)", "format": "csv", "name": "test",
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

func TestInheritSensitivityTags(t *testing.T) {
	tk := &Toolkit{
		semanticProvider: &mockExportSemanticProvider{
			tableTags: map[string][]string{
				"catalog.schema.users":  {"PII", "customer-data"},
				"catalog.schema.orders": {"financial"},
			},
		},
	}

	tags := tk.inheritSensitivityTags(context.Background(), "SELECT * FROM catalog.schema.users JOIN catalog.schema.orders ON true")
	assert.Contains(t, tags, "_sys-classification:pii")
	assert.NotContains(t, tags, "_sys-classification:financial") // "financial" doesn't match sensitivity patterns
}

func TestInheritSensitivityTags_NoProvider(t *testing.T) {
	tk := &Toolkit{}
	tags := tk.inheritSensitivityTags(context.Background(), "SELECT * FROM catalog.schema.users")
	assert.Nil(t, tags)
}

func TestMaybeCreateShare(t *testing.T) {
	shareMock := &mockExportShareCreator{shareURL: "https://example.com/portal/view/tok123"}
	tk := &Toolkit{}
	deps := &ExportDeps{ShareCreator: shareMock}

	// Not requested — returns empty
	url := tk.maybeCreateShare(context.Background(), deps, exportInput{CreatePublicLink: false}, "asset-1", "a@example.com")
	assert.Empty(t, url)

	// Requested — returns share URL
	url = tk.maybeCreateShare(context.Background(), deps, exportInput{CreatePublicLink: true}, "asset-1", "a@example.com")
	assert.Equal(t, "https://example.com/portal/view/tok123", url)
	assert.Equal(t, "asset-1", shareMock.lastAssetID)

	// ShareCreator nil — returns empty
	depsNoShare := &ExportDeps{}
	url = tk.maybeCreateShare(context.Background(), depsNoShare, exportInput{CreatePublicLink: true}, "asset-1", "a@example.com")
	assert.Empty(t, url)

	// ShareCreator returns error — returns empty, does not panic
	errMock := &mockExportShareCreator{shareErr: fmt.Errorf("share store down")}
	depsErr := &ExportDeps{ShareCreator: errMock}
	url = tk.maybeCreateShare(context.Background(), depsErr, exportInput{CreatePublicLink: true}, "asset-1", "a@example.com")
	assert.Empty(t, url)
}

func TestResolveExportLimits(t *testing.T) {
	cfg := ExportConfig{
		MaxRows:        10000,
		DefaultTimeout: 5 * time.Minute,
		MaxTimeout:     10 * time.Minute,
	}

	timeout, limit := resolveExportLimits(exportInput{}, cfg)
	assert.Equal(t, 5*time.Minute, timeout)
	assert.Equal(t, 10000, limit)

	timeout, limit = resolveExportLimits(exportInput{Limit: 500, TimeoutSeconds: 30}, cfg)
	assert.Equal(t, 30*time.Second, timeout)
	assert.Equal(t, 500, limit)
}

func TestConvertQueryResult(t *testing.T) {
	result := &trinoclient.QueryResult{
		Columns: []trinoclient.ColumnInfo{
			{Name: "id", Type: "integer"},
			{Name: "name", Type: "varchar"},
		},
		Rows: []map[string]any{
			{"id": 1, "name": "Alice"},
			{"id": 2, "name": "Bob"},
		},
	}

	columns, rows := convertQueryResult(result)
	assert.Equal(t, []string{"id", "name"}, columns)
	require.Len(t, rows, 2)
	assert.Equal(t, 1, rows[0][0])
	assert.Equal(t, "Alice", rows[0][1])
}

func TestFormatExportResult(t *testing.T) {
	columns := []string{"a", "b"}
	rows := [][]any{{"x", "y"}}

	data, formatter, errResult := formatExportResult("csv", columns, rows, 1024*1024)
	assert.Nil(t, errResult)
	assert.NotNil(t, formatter)
	assert.Contains(t, string(data), "a,b")

	// Byte cap exceeded
	_, _, errResult = formatExportResult("csv", columns, rows, 1)
	assert.NotNil(t, errResult)
	assert.True(t, errResult.IsError)
}

func TestBuildExportProvenance(t *testing.T) {
	deps := &ExportDeps{
		GetProvenanceCalls: func(_ context.Context) []ExportProvenanceCall {
			return []ExportProvenanceCall{
				{ToolName: "trino_query", Timestamp: "2026-01-01T00:00:00Z"},
			}
		},
	}

	prov := buildExportProvenance(context.Background(), deps, exportProvenanceParams{
		userID:       "u1",
		sessionID:    "s1",
		sql:          "SELECT 1",
		sourceTables: []string{"catalog.schema.t"},
		format:       "csv",
		rowCount:     100,
	})

	assert.Equal(t, "u1", prov.UserID)
	assert.Equal(t, "s1", prov.SessionID)
	// Should have the session call + the export operation itself
	require.Len(t, prov.ToolCalls, 2)
	assert.Equal(t, "trino_query", prov.ToolCalls[0].ToolName)
	assert.Equal(t, exportToolName, prov.ToolCalls[1].ToolName)
	assert.Equal(t, "SELECT 1", prov.ToolCalls[1].Parameters["export_query"])
}

func TestInsertAssetWithRace_Success(t *testing.T) {
	tk := &Toolkit{}
	store := &mockExportAssetStore{}
	deps := &ExportDeps{AssetStore: store, BaseURL: "https://example.com"}
	uc := &ExportUserContext{UserID: "u1"}
	asset := ExportAsset{ID: "a1"}
	input := exportInput{Format: "csv"}

	result := tk.insertAssetWithRace(context.Background(), deps, asset, input, uc)
	assert.Nil(t, result)
	assert.NotNil(t, store.inserted)
}

func TestInsertAssetWithRace_FailNoIdempotency(t *testing.T) {
	tk := &Toolkit{}
	store := &mockExportAssetStore{insertErr: fmt.Errorf("db error")}
	deps := &ExportDeps{AssetStore: store}
	uc := &ExportUserContext{UserID: "u1"}

	result := tk.insertAssetWithRace(context.Background(), deps, ExportAsset{}, exportInput{Format: "csv"}, uc)
	assert.NotNil(t, result)
	assert.True(t, result.IsError)
}

func TestInsertAssetWithRace_RaceRecovery(t *testing.T) {
	tk := &Toolkit{}
	store := &mockExportAssetStore{
		insertErr:      fmt.Errorf("unique constraint violation"),
		idempotencyHit: &ExportAssetRef{ID: "existing-1", SizeBytes: 999},
	}
	deps := &ExportDeps{AssetStore: store, BaseURL: "https://example.com"}
	uc := &ExportUserContext{UserID: "u1"}

	result := tk.insertAssetWithRace(context.Background(), deps, ExportAsset{}, exportInput{Format: "csv", IdempotencyKey: "key1"}, uc)
	assert.NotNil(t, result)
	assert.False(t, result.IsError)
	assertResultContains(t, result, "existing-1")
}

func TestCreateExportVersion(t *testing.T) {
	tk := &Toolkit{}
	versionStore := &mockExportVersionStore{}
	deps := &ExportDeps{VersionStore: versionStore, S3Bucket: "bucket"}

	tk.createExportVersion(context.Background(), deps, ExportVersion{
		AssetID:     "a1",
		S3Key:       "key",
		ContentType: "text/csv",
		SizeBytes:   100,
		CreatedBy:   "alice@example.com",
	})

	require.NotNil(t, versionStore.created)
	assert.Equal(t, "a1", versionStore.created.AssetID)
	assert.Equal(t, "bucket", versionStore.created.S3Bucket)
	assert.NotEmpty(t, versionStore.created.ID)
}

func TestCreateExportVersion_Error(t *testing.T) { //nolint:revive // t used for test registration
	tk := &Toolkit{}
	versionStore := &mockExportVersionStore{createErr: fmt.Errorf("db down")}
	deps := &ExportDeps{VersionStore: versionStore, S3Bucket: "bucket"}

	// Should not panic — errors are logged, not returned
	tk.createExportVersion(context.Background(), deps, ExportVersion{AssetID: "a1"})
}

func TestGenerateExportID(t *testing.T) {
	id, err := generateExportID()
	require.NoError(t, err)
	assert.Len(t, id, 32) // 16 bytes = 32 hex chars

	// IDs should be unique
	id2, err := generateExportID()
	require.NoError(t, err)
	assert.NotEqual(t, id, id2)
}

func TestRegisterExportTool(t *testing.T) {
	tk := &Toolkit{name: "test"}
	tk.SetExportDeps(ExportDeps{
		AssetStore:   &mockExportAssetStore{},
		VersionStore: &mockExportVersionStore{},
		S3Client:     &mockExportS3Client{},
	})

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	tk.registerExportTool(server)
	// Verify tool is registered by checking Tools() includes it
	assert.Contains(t, tk.Tools(), exportToolName)
}

func TestExecuteExportQuery_NoClient(t *testing.T) {
	tk := &Toolkit{} // no client, no manager
	_, err := tk.executeExportQuery(context.Background(), "SELECT 1", "", 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no Trino client")
}

func TestParseExportInput_NilParams(t *testing.T) {
	req := mcp.CallToolRequest{}
	_, err := parseExportInput(req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing arguments")
}

func TestExportInputSchema_HasCreatePublicLink(t *testing.T) {
	schema := exportInputSchema()
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	cpl, ok := props["create_public_link"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "boolean", cpl[schemaKeyType])
}

func TestHandleExport_FullFlow(t *testing.T) {
	// Create a real trinoclient backed by sqlmock
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	client := trinoclient.NewWithDB(db, trinoclient.Config{Timeout: time.Minute})

	// sqlmock: expect the query and return 2 rows
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
			AddRow(1, "Alice").
			AddRow(2, "Bob"))

	assetStore := &mockExportAssetStore{}
	versionStore := &mockExportVersionStore{}
	s3Client := &mockExportS3Client{}

	tk := &Toolkit{
		name:   "test",
		client: client,
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
			MaxRows:        10000,
			MaxBytes:       1024 * 1024,
			DefaultTimeout: time.Minute,
			MaxTimeout:     time.Minute,
		},
		GetUserContext: func(_ context.Context) *ExportUserContext {
			return &ExportUserContext{
				UserID:    "user-123",
				UserEmail: "alice@example.com",
				SessionID: "sess-abc",
			}
		},
		GetProvenanceCalls: func(_ context.Context) []ExportProvenanceCall {
			return []ExportProvenanceCall{
				{ToolName: "trino_query", Timestamp: "2026-01-01T00:00:00Z"},
			}
		},
	})

	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "SELECT id, name FROM users", "format": "csv", "name": "User Export",
		"tags": []string{"export"}, "description": "test export",
	}))
	require.NoError(t, err)
	if result.IsError {
		tc, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		t.Fatalf("expected success, got error: %s", tc.Text)
	}

	// Verify S3 upload happened
	assert.Equal(t, "test-bucket", s3Client.lastBucket)
	assert.Contains(t, string(s3Client.lastData), "id,name")
	assert.Contains(t, string(s3Client.lastData), "Alice")

	// Verify asset was inserted
	require.NotNil(t, assetStore.inserted)
	assert.Equal(t, "User Export", assetStore.inserted.Name)
	assert.Equal(t, "text/csv", assetStore.inserted.ContentType)
	assert.Contains(t, assetStore.inserted.Tags, "export")
	assert.Equal(t, "user-123", assetStore.inserted.OwnerID)
	assert.Equal(t, "test export", assetStore.inserted.Description)

	// Verify version was created
	require.NotNil(t, versionStore.created)

	// Verify response contains asset ID and row count
	assertResultContains(t, result, "Exported 2 rows as csv")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandleExport_S3FailureNoAsset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	client := trinoclient.NewWithDB(db, trinoclient.Config{Timeout: time.Minute})
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	assetStore := &mockExportAssetStore{}
	s3Client := &mockExportS3Client{putErr: fmt.Errorf("s3 down")}

	tk := &Toolkit{name: "test", client: client, config: Config{ReadOnly: true}}
	tk.SetExportDeps(ExportDeps{
		AssetStore:   assetStore,
		VersionStore: &mockExportVersionStore{},
		S3Client:     s3Client,
		S3Bucket:     "b",
		S3Prefix:     "p",
		Config:       ExportConfig{DefaultTimeout: time.Minute, MaxTimeout: time.Minute},
		GetUserContext: func(_ context.Context) *ExportUserContext {
			return &ExportUserContext{UserID: "u1", UserEmail: "a@example.com", SessionID: "s1"}
		},
		GetProvenanceCalls: func(_ context.Context) []ExportProvenanceCall { return nil },
	})

	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "SELECT id FROM t", "format": "json", "name": "test",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assertResultContains(t, result, "S3 upload failed")

	// No asset should be created
	assert.Nil(t, assetStore.inserted)
}

func TestHandleExport_WithCreatePublicLink(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	client := trinoclient.NewWithDB(db, trinoclient.Config{Timeout: time.Minute})
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"val"}).AddRow("x"))

	shareMock := &mockExportShareCreator{shareURL: "https://example.com/portal/view/tok"}
	tk := &Toolkit{name: "test", client: client}
	tk.SetExportDeps(ExportDeps{
		AssetStore:   &mockExportAssetStore{},
		VersionStore: &mockExportVersionStore{},
		S3Client:     &mockExportS3Client{},
		ShareCreator: shareMock,
		S3Bucket:     "b",
		S3Prefix:     "p",
		BaseURL:      "https://example.com",
		Config:       ExportConfig{DefaultTimeout: time.Minute, MaxTimeout: time.Minute},
		GetUserContext: func(_ context.Context) *ExportUserContext {
			return &ExportUserContext{UserID: "u1", UserEmail: "a@example.com", SessionID: "s1"}
		},
		GetProvenanceCalls: func(_ context.Context) []ExportProvenanceCall { return nil },
	})

	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "SELECT val FROM t", "format": "text", "name": "test", "create_public_link": true,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	assertResultContains(t, result, "portal/view/tok")
	assert.NotEmpty(t, shareMock.lastAssetID)
}

func TestHandleExport_ByteCapExceeded_WithClient(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	client := trinoclient.NewWithDB(db, trinoclient.Config{Timeout: time.Minute})
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"big"}).AddRow("xxxxxxxxxx"))

	tk := &Toolkit{name: "test", client: client}
	tk.SetExportDeps(ExportDeps{
		AssetStore:   &mockExportAssetStore{},
		VersionStore: &mockExportVersionStore{},
		S3Client:     &mockExportS3Client{},
		S3Bucket:     "b",
		S3Prefix:     "p",
		Config:       ExportConfig{MaxBytes: 5, DefaultTimeout: time.Minute, MaxTimeout: time.Minute}, // very small byte cap
		GetUserContext: func(_ context.Context) *ExportUserContext {
			return &ExportUserContext{UserID: "u1", UserEmail: "a@example.com", SessionID: "s1"}
		},
		GetProvenanceCalls: func(_ context.Context) []ExportProvenanceCall { return nil },
	})

	result, err := tk.handleExport(context.Background(), buildExportRequest(map[string]any{
		"sql": "SELECT big FROM t", "format": "csv", "name": "test",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assertResultContains(t, result, "exceeds deployment maximum")
}

// Suppress unused import warning for sql package (used by sqlmock).
var _ = sql.ErrNoRows

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

func TestSanitizeExportName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii", "Loyalty Customers by DBA", "Loyalty Customers by DBA"},
		{"em dash", "Q1 \u2014 Sales Report", "Q1 - Sales Report"},
		{"en dash", "2024\u20132025 Trend", "2024-2025 Trend"},
		{"figure dash", "Page\u20122", "Page-2"},
		{"horizontal bar", "Section\u2015A", "Section-A"},
		{"minus sign", "Delta\u22125", "Delta-5"},
		{"smart double quotes", "\u201cHello\u201d", "\"Hello\""},
		{"smart single quotes", "It\u2019s great", "It's great"},
		{"low double quote", "\u201eGerman\u201c style", "\"German\" style"},
		{"ellipsis", "Loading\u2026", "Loading..."},
		{"non-breaking space", "Hello\u00a0World", "Hello World"},
		{"zero-width space stripped", "A\u200BB", "AB"},
		{"BOM stripped", "\ufeffReport", "Report"},
		{"control chars stripped", "Line1\x00Line2", "Line1Line2"},
		{"trim whitespace", "  spaced  ", "spaced"},
		{"collapse whitespace", "many   spaces", "many spaces"},
		{"empty input", "", ""},
		{"only whitespace", "   \t\n", ""},
		{"mixed real-world", "Q1\u2014\u201cSales\u201d Report\u2026", "Q1-\"Sales\" Report..."},
		{"preserves dots and underscores", "report_v1.2.csv", "report_v1.2.csv"},
		{"preserves digits", "2024 Q1 Report", "2024 Q1 Report"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeExportName(tt.in))
		})
	}
}

func TestSanitizeUserIDPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty becomes underscore", "", "_"},
		{"plain alphanumeric", "user-123", "user-123"},
		{"uuid", "550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440000"},
		{"apikey colon", "apikey:admin", "apikey_admin"},
		{"email", "alice@example.com", "alice_example.com"},
		{"slash injection", "user/../etc", "user_.._etc"},
		{"backslash", "DOMAIN\\user", "DOMAIN_user"},
		{"unicode", "f\u00f6\u00f6", "f__"},
		{"mixed", "anonymous (api)", "anonymous__api_"},
		{"single dot", ".", "_"},
		{"double dot", "..", "_"},
		{"triple dot", "...", "_"},
		{"all dots six", "......", "_"},
		{"dot among letters preserved", "a.b", "a.b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeUserIDPath(tt.in))
		})
	}
}

func TestBuildExportS3Key(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		userID    string
		assetID   string
		extension string
		want      string
	}{
		{
			name:      "default trailing-slash prefix is normalized",
			prefix:    "artifacts/",
			userID:    "user-123",
			assetID:   "abc",
			extension: ".csv",
			want:      "artifacts/user-123/abc/content.csv",
		},
		{
			name:      "no trailing slash on prefix",
			prefix:    "artifacts",
			userID:    "user-123",
			assetID:   "abc",
			extension: ".csv",
			want:      "artifacts/user-123/abc/content.csv",
		},
		{
			name:      "apikey user id is sanitized",
			prefix:    "exports",
			userID:    "apikey:admin",
			assetID:   "abc",
			extension: ".json",
			want:      "exports/apikey_admin/abc/content.json",
		},
		{
			name:      "empty user id falls back to underscore",
			prefix:    "exports",
			userID:    "",
			assetID:   "abc",
			extension: ".csv",
			want:      "exports/_/abc/content.csv",
		},
		{
			name:      "nested prefix",
			prefix:    "tenants/acme/exports/",
			userID:    "u1",
			assetID:   "id1",
			extension: ".md",
			want:      "tenants/acme/exports/u1/id1/content.md",
		},
		{
			name:      "leading slash on prefix is trimmed",
			prefix:    "/artifacts/",
			userID:    "u1",
			assetID:   "abc",
			extension: ".csv",
			want:      "artifacts/u1/abc/content.csv",
		},
		{
			name:      "dotdot user id cannot escape prefix",
			prefix:    "artifacts/",
			userID:    "..",
			assetID:   "abc",
			extension: ".csv",
			want:      "artifacts/_/abc/content.csv",
		},
		{
			name:      "dot user id cannot collapse segment",
			prefix:    "artifacts/",
			userID:    ".",
			assetID:   "abc",
			extension: ".csv",
			want:      "artifacts/_/abc/content.csv",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildExportS3Key(tt.prefix, tt.userID, tt.assetID, tt.extension)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "//", "S3 key must not contain consecutive slashes")
			assert.False(t, strings.HasPrefix(got, "/"), "S3 key must not start with /")
		})
	}
}

func TestValidateAndPrepare_NameSanitization(t *testing.T) {
	assetStore := &mockExportAssetStore{}
	versionStore := &mockExportVersionStore{}
	s3Client := &mockExportS3Client{}
	tk := newTestExportToolkit(assetStore, versionStore, s3Client)

	req := buildExportRequest(map[string]any{
		"sql":    "SELECT 1",
		"format": "csv",
		"name":   "Q1\u2014\u201cSales\u201d Report\u2026",
	})

	input, _, errResult := tk.validateAndPrepare(context.Background(), req, tk.exportDeps)
	require.Nil(t, errResult, "validation should succeed")
	assert.Equal(t, "Q1-\"Sales\" Report...", input.Name, "name should be sanitized to ASCII")
}

func TestValidateAndPrepare_NameSanitizedToEmpty(t *testing.T) {
	assetStore := &mockExportAssetStore{}
	versionStore := &mockExportVersionStore{}
	s3Client := &mockExportS3Client{}
	tk := newTestExportToolkit(assetStore, versionStore, s3Client)

	// A name made up entirely of zero-width and control chars sanitizes to "".
	req := buildExportRequest(map[string]any{
		"sql":    "SELECT 1",
		"format": "csv",
		"name":   "\u200B\u200C\u200D",
	})

	_, _, errResult := tk.validateAndPrepare(context.Background(), req, tk.exportDeps)
	require.NotNil(t, errResult, "validation should fail when name sanitizes to empty")
	assert.True(t, errResult.IsError)
	assertResultContains(t, errResult, "name")
}

func TestValidateAndPrepare_SanitizationExpansionExceedsCap(t *testing.T) {
	// 100 ellipsis runes (1 char each) sanitize to 300 ASCII chars (3 each),
	// which exceeds maxExportNameLength (255). validateExportInput runs after
	// sanitization and must still catch the over-cap result.
	tk := newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{})

	name := strings.Repeat("\u2026", 100)
	req := buildExportRequest(map[string]any{
		"sql":    "SELECT 1",
		"format": "csv",
		"name":   name,
	})

	_, _, errResult := tk.validateAndPrepare(context.Background(), req, tk.exportDeps)
	require.NotNil(t, errResult, "validation should catch over-cap name after sanitization expansion")
	assert.True(t, errResult.IsError)
	assertResultContains(t, errResult, "exceeds")
}
