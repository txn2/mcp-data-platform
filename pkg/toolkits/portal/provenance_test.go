package portal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// manage_asset get returned every capture, and a capture is appended on every
// write: an asset a scheduled script refreshes hourly made the tool's own
// result the largest thing the platform served (#1623). The get now carries the
// newest captures and the count of what the asset holds, and action=provenance
// reaches the rest.

func assetWithCaptures(t *testing.T, n int) *inMemoryAssetStore {
	t.Helper()
	store := newInMemoryAssetStore()
	captures := make([]portal.ProvenanceCapture, 0, n)
	for i := 1; i <= n; i++ {
		captures = append(captures, portal.ProvenanceCapture{Tool: "manage_asset", Version: i})
	}
	require.NoError(t, store.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "Dashboard", Tags: []string{},
		Provenance: portal.Provenance{SessionID: "dps_abc", Captures: captures},
	}))
	return store
}

func resultAsset(t *testing.T, r *mcp.CallToolResult) portal.Asset {
	t.Helper()
	require.NotEmpty(t, r.Content)
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var a portal.Asset
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &a))
	return a
}

func TestManageAsset_GetCarriesTheNewestCapturesAndTheirTotal(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: assetWithCaptures(t, 333), S3Bucket: "bucket"})

	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: actionGet, AssetID: "a1",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	got := resultAsset(t, result)
	assert.Len(t, got.Provenance.Captures, portaldomain.ProvenanceCapturesInline)
	assert.Equal(t, 333, got.Provenance.CapturesTotal)
	assert.Equal(t, 333, got.Provenance.Captures[len(got.Provenance.Captures)-1].Version,
		"the newest capture is still the last one")
}

// An asset with a single capture reads exactly as it did before the bound
// existed.
func TestManageAsset_GetASingleCaptureReadsUnchanged(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: assetWithCaptures(t, 1), S3Bucket: "bucket"})

	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: actionGet, AssetID: "a1",
	})
	require.NoError(t, err)
	got := resultAsset(t, result)
	assert.Len(t, got.Provenance.Captures, 1)
	assert.Zero(t, got.Provenance.CapturesTotal)
	assert.Equal(t, "dps_abc", got.Provenance.SessionID)
}

// The page after the newest twenty, newest first, with no overlap and no gap.
func TestManageAsset_ProvenancePagesTheRest(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: assetWithCaptures(t, 50), S3Bucket: "bucket"})

	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: actionProvenance, AssetID: "a1", Offset: 20, Limit: 20,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	m := searchResultMap(t, result)
	assert.Equal(t, float64(50), m[fieldTotal])
	assert.Equal(t, float64(20), m["offset"])
	assert.Equal(t, float64(20), m["limit"])
	captures, ok := m["captures"].([]any)
	require.True(t, ok)
	require.Len(t, captures, 20)
	first, _ := captures[0].(map[string]any)
	last, _ := captures[19].(map[string]any)
	assert.Equal(t, float64(30), first["version"], "the capture after the newest twenty")
	assert.Equal(t, float64(11), last["version"])
}

// The default page is what a caller naming no bounds gets, and an oversized
// request is cut to the maximum rather than answered in full.
func TestManageAsset_ProvenanceDefaultsAndClamps(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: assetWithCaptures(t, 500), S3Bucket: "bucket"})

	for _, tc := range []struct {
		name  string
		limit int
		want  int
	}{
		{"default", 0, portaldomain.DefaultProvenancePageSize},
		{"clamped", 5000, portaldomain.MaxProvenancePageSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
				Action: actionProvenance, AssetID: "a1", Limit: tc.limit,
			})
			require.NoError(t, err)
			m := searchResultMap(t, result)
			assert.Equal(t, float64(tc.want), m["limit"])
			captures, _ := m["captures"].([]any)
			assert.Len(t, captures, tc.want)
		})
	}
}

func TestManageAsset_ProvenanceRequiresAnAsset(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Bucket: "bucket"})

	for _, tc := range []struct {
		name    string
		input   manageAssetInput
		wantErr bool
	}{
		{"no id", manageAssetInput{Action: actionProvenance}, true},
		{"unknown id", manageAssetInput{Action: actionProvenance, AssetID: "missing"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, _, err := tk.handleManageAsset(context.Background(), nil, tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.wantErr, result.IsError)
		})
	}
}

func TestManageAsset_ProvenanceOnADeletedAsset(t *testing.T) {
	store := assetWithCaptures(t, 3)
	require.NoError(t, store.SoftDelete(context.Background(), "a1"))
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	result, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: actionProvenance, AssetID: "a1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// The tool says what its listings carry and how the captures a get leaves out
// are reached: an agent that cannot see the summary in the description reads
// the whole listing to find out it is not there.
func TestManageToolDescription_NamesTheSummaryAndThePage(t *testing.T) {
	assert.Contains(t, manageToolDescription, "provenance_summary")
	assert.Contains(t, manageToolDescription, "captures_total")
	assert.Contains(t, manageToolDescription, "search, provenance")
}
