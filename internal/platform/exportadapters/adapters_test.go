package exportadapters

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// --- stubs ---

type stubAssetStore struct {
	portal.AssetStore
	inserted    *portal.Asset
	insertErr   error
	getByKey    *portal.Asset
	getByKeyErr error
}

func (s *stubAssetStore) Insert(_ context.Context, asset portal.Asset) error {
	s.inserted = &asset
	return s.insertErr
}

func (s *stubAssetStore) GetByIdempotencyKey(_ context.Context, _, _ string) (*portal.Asset, error) {
	if s.getByKey != nil {
		return s.getByKey, nil
	}
	return nil, s.getByKeyErr
}

type stubVersionStore struct {
	portal.VersionStore
	created   *portal.AssetVersion
	createErr error
}

func (s *stubVersionStore) CreateVersion(_ context.Context, v portal.AssetVersion) (int, error) {
	s.created = &v
	if s.createErr != nil {
		return 0, s.createErr
	}
	return 1, nil
}

type stubShareStore struct {
	portal.ShareStore
	inserted  *portal.Share
	insertErr error
}

func (s *stubShareStore) Insert(_ context.Context, share *portal.Share) error {
	s.inserted = share
	return s.insertErr
}

// --- trino adapter tests ---

func TestTrinoExporter_InsertAsset(t *testing.T) {
	store := &stubAssetStore{}
	adapter := NewTrinoExporter(store, nil, nil, "", nil)

	err := adapter.InsertExportAsset(context.Background(), trinokit.ExportAsset{
		ID:      "a1",
		OwnerID: "u1",
		Name:    "Test",
		Tags:    []string{"tag1"},
		Provenance: trinokit.ExportProvenance{
			UserID:    "u1",
			SessionID: "s1",
			ToolCalls: []trinokit.ExportProvenanceCall{
				{ToolName: "trino_query", Timestamp: "2026-01-01T00:00:00Z"},
			},
		},
		IdempotencyKey: "key1",
	})
	require.NoError(t, err)
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.ID)
	assert.Equal(t, "key1", store.inserted.IdempotencyKey)
	require.Len(t, store.inserted.Provenance.Captures, 1)
	require.Len(t, store.inserted.Provenance.Captures[0].Calls, 1)
	assert.Equal(t, portal.ProvenanceKindSQL, store.inserted.Provenance.Captures[0].Calls[0].Kind)
}

func TestTrinoExporter_InsertAssetError(t *testing.T) {
	store := &stubAssetStore{insertErr: fmt.Errorf("db error")}
	adapter := NewTrinoExporter(store, nil, nil, "", nil)

	err := adapter.InsertExportAsset(context.Background(), trinokit.ExportAsset{ID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting export asset")
}

func TestTrinoExporter_GetByIdempotencyKey(t *testing.T) {
	store := &stubAssetStore{getByKey: &portal.Asset{ID: "a1", SizeBytes: 999}}
	adapter := NewTrinoExporter(store, nil, nil, "", nil)

	ref, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	require.NoError(t, err)
	assert.Equal(t, "a1", ref.ID)
	assert.Equal(t, int64(999), ref.SizeBytes)
}

func TestTrinoExporter_GetByIdempotencyKeyNotFound(t *testing.T) {
	store := &stubAssetStore{getByKeyErr: fmt.Errorf("not found")}
	adapter := NewTrinoExporter(store, nil, nil, "", nil)

	_, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "looking up export idempotency key")
}

func TestTrinoExporter_CreateVersion(t *testing.T) {
	store := &stubVersionStore{}
	adapter := NewTrinoExporter(nil, store, nil, "", nil)

	n, err := adapter.CreateExportVersion(context.Background(), trinokit.ExportVersion{
		AssetID: "a1", S3Key: "key", S3Bucket: "b", ContentType: "text/csv",
		SizeBytes: 100, CreatedBy: "alice@example.com", ChangeSummary: "test",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, store.created)
	assert.Equal(t, "a1", store.created.AssetID)
}

func TestTrinoExporter_CreateVersionError(t *testing.T) {
	store := &stubVersionStore{createErr: fmt.Errorf("db error")}
	adapter := NewTrinoExporter(nil, store, nil, "", nil)

	_, err := adapter.CreateExportVersion(context.Background(), trinokit.ExportVersion{AssetID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating export version")
}

func TestTrinoExporter_CreatePublicShare(t *testing.T) {
	store := &stubShareStore{}
	adapter := NewTrinoExporter(nil, nil, store, "https://example.com", nil)

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	assert.Contains(t, url, "https://example.com/portal/view/")
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.AssetID)
	assert.Equal(t, "alice@example.com", store.inserted.CreatedBy)
	assert.NotEmpty(t, store.inserted.Token)
	assert.NotEmpty(t, store.inserted.NoticeText)
}

func TestTrinoExporter_CreatePublicShareNoBaseURL(t *testing.T) {
	store := &stubShareStore{}
	adapter := NewTrinoExporter(nil, nil, store, "", nil)

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	// Empty baseURL → empty share URL. The share row IS still inserted (token
	// exists in DB) so the caller can compute the URL later.
	assert.Empty(t, url)
	assert.NotNil(t, store.inserted)
}

func TestTrinoExporter_CreatePublicShareInsertError(t *testing.T) {
	store := &stubShareStore{insertErr: fmt.Errorf("db error")}
	adapter := NewTrinoExporter(nil, nil, store, "https://example.com", nil)

	_, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting export share")
}

func TestTrinoOwnCall(t *testing.T) {
	call := trinoOwnCall([]trinokit.ExportProvenanceCall{
		{ToolName: "trino_export", Timestamp: "2026-01-01T00:00:00Z", Parameters: map[string]any{
			"export_query": "SELECT 1", "connection": "warehouse",
		}},
	})
	require.NotNil(t, call)
	assert.Equal(t, portal.ProvenanceKindSQL, call.Kind)
	assert.Equal(t, "trino_export", call.Tool)
	assert.Equal(t, "SELECT 1", call.Statement)
	assert.Equal(t, "warehouse", call.Connection)
	assert.Equal(t, portal.ProvenanceOutcomeSuccess, call.Outcome)
	assert.Equal(t, "2026-01-01T00:00:00Z", call.Timestamp.Format(time.RFC3339))
}

func TestTrinoOwnCallEmpty(t *testing.T) {
	assert.Nil(t, trinoOwnCall(nil), "an export that recorded nothing about itself contributes no call")
}

func TestAPIOwnCall(t *testing.T) {
	call := apiOwnCall([]apigatewaykit.ExportProvenanceCall{
		{ToolName: "api_export", Timestamp: "2026-01-01T00:00:00Z", Parameters: map[string]any{
			"connection": "crm", "method": "GET", "path": "/v1/items", "upstream_status": 200,
		}},
	})
	require.NotNil(t, call)
	assert.Equal(t, portal.ProvenanceKindAPI, call.Kind)
	assert.Equal(t, "GET", call.Method)
	assert.Equal(t, "/v1/items", call.Path)
	assert.Equal(t, "crm", call.Connection)
	assert.Equal(t, "GET /v1/items", call.Request)
	assert.Equal(t, portal.ProvenanceOutcomeSuccess, call.Outcome)
}

// The export's record of itself reads the way the calls captured around it do:
// two exports of one endpoint differ in what they asked it for (#1423).
func TestAPIOwnCallRecordsWhatItAskedFor(t *testing.T) {
	call := apiOwnCall([]apigatewaykit.ExportProvenanceCall{
		{ToolName: "api_export", Timestamp: "2026-01-01T00:00:00Z", Parameters: map[string]any{
			"connection":   "crm",
			"method":       "POST",
			"path":         "/v1/reports",
			"query_params": map[string]any{"segment": "enterprise"},
			"body":         map[string]any{"quarter": "Q3"},
		}},
	})
	require.NotNil(t, call)
	assert.Equal(t, "POST /v1/reports?segment=enterprise\n{\"quarter\":\"Q3\"}", call.Request)
}

func TestAPIOwnCallUpstreamError(t *testing.T) {
	call := apiOwnCall([]apigatewaykit.ExportProvenanceCall{
		{ToolName: "api_export", Parameters: map[string]any{"upstream_status": 503}},
	})
	require.NotNil(t, call)
	assert.Equal(t, portal.ProvenanceOutcomeError, call.Outcome)
	assert.Equal(t, "upstream returned 503", call.Error)
}

// A deployment with no audit log still records what the export itself did.
func TestCapturedProvenanceWithoutCapturer(t *testing.T) {
	own := &portal.ProvenanceCall{Kind: portal.ProvenanceKindSQL, Tool: "trino_export"}
	prov := capturedProvenance(context.Background(), nil, provenanceInput{
		userID: "u1", sessionID: "s1", tool: trinoExportTool, own: own,
	})
	require.Len(t, prov.Captures, 1)
	assert.Equal(t, trinoExportTool, prov.Captures[0].Tool)
	assert.Equal(t, "s1", prov.Captures[0].SessionID)
	require.Len(t, prov.Captures[0].Calls, 1)
	assert.Equal(t, "trino_export", prov.Captures[0].Calls[0].Tool)
}

// A capture that found nothing records nothing: an empty captures list is not
// the same claim as a capture holding no calls.
func TestCapturedProvenanceEmptyCapture(t *testing.T) {
	prov := capturedProvenance(context.Background(), func(context.Context, portal.ProvenanceRequest) portal.ProvenanceCapture {
		return portal.ProvenanceCapture{Tool: trinoExportTool}
	}, provenanceInput{userID: "u1", sessionID: "s1", tool: trinoExportTool})
	assert.Empty(t, prov.Captures)
	assert.Equal(t, "u1", prov.UserID)
}

func TestCapturedProvenanceUsesCapturer(t *testing.T) {
	var got portal.ProvenanceRequest
	own := &portal.ProvenanceCall{Kind: portal.ProvenanceKindAPI, Tool: "api_export"}
	prov := capturedProvenance(context.Background(), func(_ context.Context, req portal.ProvenanceRequest) portal.ProvenanceCapture {
		got = req
		return portal.ProvenanceCapture{Tool: req.Tool, Calls: []portal.ProvenanceCall{
			{Kind: portal.ProvenanceKindSQL, Tool: "trino_query"}, *req.Own,
		}}
	}, provenanceInput{userID: "u1", sessionID: "s1", tool: apiExportTool, own: own, declaredContentType: "text/plain"})

	assert.Equal(t, apiExportTool, got.Tool)
	assert.Equal(t, "s1", got.SessionID)
	assert.Equal(t, "u1", got.UserID)
	require.NotNil(t, got.Own)
	require.Len(t, prov.Captures, 1)
	assert.Len(t, prov.Captures[0].Calls, 2)
	assert.Equal(t, "text/plain", prov.DeclaredContentType)
}

// --- api-gateway adapter tests ---

func TestAPIExporter_InsertAsset(t *testing.T) {
	store := &stubAssetStore{}
	adapter := NewAPIExporter(store, nil, nil, "", nil)

	err := adapter.InsertExportAsset(context.Background(), apigatewaykit.ExportAsset{
		ID:      "a1",
		OwnerID: "u1",
		Name:    "items dump",
		Tags:    []string{"crm", "weekly"},
		Provenance: apigatewaykit.ExportProvenance{
			UserID:    "u1",
			SessionID: "s1",
			ToolCalls: []apigatewaykit.ExportProvenanceCall{
				{ToolName: "api_export", Timestamp: "2026-01-01T00:00:00Z"},
			},
		},
		IdempotencyKey: "key1",
	})
	require.NoError(t, err)
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.ID)
	assert.Equal(t, "key1", store.inserted.IdempotencyKey)
	require.Len(t, store.inserted.Provenance.Captures, 1)
	require.Len(t, store.inserted.Provenance.Captures[0].Calls, 1)
	assert.Equal(t, portal.ProvenanceKindAPI, store.inserted.Provenance.Captures[0].Calls[0].Kind)
	assert.Equal(t, "api_export", store.inserted.Provenance.Captures[0].Calls[0].Tool)
	assert.Empty(t, store.inserted.Provenance.ToolCalls, "the pre-#1320 shape is no longer written")
}

func TestAPIExporter_InsertAssetError(t *testing.T) {
	store := &stubAssetStore{insertErr: fmt.Errorf("db down")}
	adapter := NewAPIExporter(store, nil, nil, "", nil)

	err := adapter.InsertExportAsset(context.Background(), apigatewaykit.ExportAsset{ID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting export asset")
}

func TestAPIExporter_GetByIdempotencyKey(t *testing.T) {
	store := &stubAssetStore{getByKey: &portal.Asset{ID: "a1", SizeBytes: 1234}}
	adapter := NewAPIExporter(store, nil, nil, "", nil)

	ref, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	require.NoError(t, err)
	assert.Equal(t, "a1", ref.ID)
	assert.Equal(t, int64(1234), ref.SizeBytes)
}

func TestAPIExporter_GetByIdempotencyKeyError(t *testing.T) {
	store := &stubAssetStore{getByKeyErr: fmt.Errorf("not found")}
	adapter := NewAPIExporter(store, nil, nil, "", nil)

	_, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "looking up export idempotency key")
}

func TestAPIExporter_CreateVersion(t *testing.T) {
	store := &stubVersionStore{}
	adapter := NewAPIExporter(nil, store, nil, "", nil)

	n, err := adapter.CreateExportVersion(context.Background(), apigatewaykit.ExportVersion{
		ID: "v1", AssetID: "a1", S3Key: "key", S3Bucket: "b",
		ContentType: "application/json", SizeBytes: 100,
		CreatedBy: "alice@example.com", ChangeSummary: "Exported from API endpoint",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, store.created)
	assert.Equal(t, "v1", store.created.ID)
	assert.Equal(t, "a1", store.created.AssetID)
	assert.Equal(t, "Exported from API endpoint", store.created.ChangeSummary)
}

func TestAPIExporter_CreateVersionError(t *testing.T) {
	store := &stubVersionStore{createErr: fmt.Errorf("db down")}
	adapter := NewAPIExporter(nil, store, nil, "", nil)

	_, err := adapter.CreateExportVersion(context.Background(), apigatewaykit.ExportVersion{AssetID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating export version")
}

func TestAPIExporter_CreatePublicShare(t *testing.T) {
	store := &stubShareStore{}
	adapter := NewAPIExporter(nil, nil, store, "https://platform.example.com", nil)

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	assert.Contains(t, url, "https://platform.example.com/portal/view/")
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.AssetID)
	assert.Equal(t, "alice@example.com", store.inserted.CreatedBy)
	assert.NotEmpty(t, store.inserted.Token)
	assert.NotEmpty(t, store.inserted.NoticeText)
}

func TestAPIExporter_CreatePublicShareNoBaseURL(t *testing.T) {
	store := &stubShareStore{}
	adapter := NewAPIExporter(nil, nil, store, "", nil)

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	assert.Empty(t, url)
	assert.NotNil(t, store.inserted)
}

func TestAPIExporter_CreatePublicShareInsertError(t *testing.T) {
	store := &stubShareStore{insertErr: fmt.Errorf("db error")}
	adapter := NewAPIExporter(nil, nil, store, "https://x", nil)

	_, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting export share")
}

// The export records itself last, so the last entry is the export call.
func TestAPIOwnCallTakesTheExportCall(t *testing.T) {
	call := apiOwnCall([]apigatewaykit.ExportProvenanceCall{
		{ToolName: "api_invoke_endpoint", Timestamp: "2026-01-01T00:00:00Z"},
		{ToolName: "api_export", Timestamp: "2026-01-01T00:00:01Z", Parameters: map[string]any{"method": "POST"}},
	})
	require.NotNil(t, call)
	assert.Equal(t, "api_export", call.Tool)
	assert.Equal(t, "POST", call.Method)
}

func TestAPIOwnCallEmpty(t *testing.T) {
	assert.Nil(t, apiOwnCall(nil))
}

// A timestamp the toolkit did not stamp must not read as the zero time, which
// would render as year 1 in the portal.
func TestParseTimestampFallsBackToNow(t *testing.T) {
	assert.WithinDuration(t, time.Now(), parseTimestamp("not a time"), time.Minute)
}
