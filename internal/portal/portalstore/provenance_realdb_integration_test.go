//go:build integration

package portalstore

// Real-Postgres tests for the provenance capture append (#1320). The append is
// written as a jsonb expression over the row's own current value, so what it
// does to a row whose provenance is empty, pre-#1320, or already holding
// captures is a property of PostgreSQL's jsonb semantics — not something a
// mocked driver can show.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

const provOwner = "550e8400-e29b-41d4-a716-446655440222"

func provAsset(id string) portaldomain.Asset {
	return portaldomain.Asset{
		ID: id, OwnerID: provOwner, OwnerEmail: "u@example.com",
		Name: "asset " + id, ContentType: "text/html", S3Bucket: "portal-assets", S3Key: "k-" + id,
		SizeBytes: 10, Tags: []string{}, CurrentVersion: 1,
	}
}

func capture(tool string, version int, eventIDs ...string) portaldomain.ProvenanceCapture {
	calls := make([]portaldomain.ProvenanceCall, 0, len(eventIDs))
	for _, id := range eventIDs {
		calls = append(calls, portaldomain.ProvenanceCall{
			EventID: id, Kind: portaldomain.ProvenanceKindSQL, Tool: "trino_query",
			Statement: "SELECT 1", Outcome: portaldomain.ProvenanceOutcomeSuccess,
			Timestamp: time.Now().UTC().Truncate(time.Second),
		})
	}
	return portaldomain.ProvenanceCapture{
		Tool: tool, Version: version, SessionID: "dps_test",
		CapturedAt: time.Now().UTC().Truncate(time.Second),
		EventIDs:   eventIDs, Calls: calls,
	}
}

// Each write appends: the asset ends up holding what fed every one of its
// versions, in order.
func TestAppendProvenanceCapture_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()

	asset := provAsset("prov-append")
	asset.Provenance = portaldomain.Provenance{SessionID: "dps_test", UserID: provOwner}
	require.NoError(t, store.Insert(ctx, asset))

	require.NoError(t, store.AppendProvenanceCapture(ctx, asset.ID, capture("save_asset", 1, "e1", "e2")))
	require.NoError(t, store.AppendProvenanceCapture(ctx, asset.ID, capture("manage_asset", 2, "e3")))

	got, err := store.Get(ctx, asset.ID)
	require.NoError(t, err)
	require.Len(t, got.Provenance.Captures, 2)
	assert.Equal(t, "save_asset", got.Provenance.Captures[0].Tool)
	assert.Equal(t, []string{"e1", "e2"}, got.Provenance.Captures[0].EventIDs)
	assert.Equal(t, "manage_asset", got.Provenance.Captures[1].Tool)
	assert.Equal(t, []string{"e3"}, got.Provenance.Captures[1].EventIDs)
	assert.Equal(t, 2, got.Provenance.Captures[1].Version)
	require.Len(t, got.Provenance.Captures[1].Calls, 1)
	assert.Equal(t, portaldomain.ProvenanceKindSQL, got.Provenance.Captures[1].Calls[0].Kind)

	// The fields the asset already carried are untouched by the append.
	assert.Equal(t, "dps_test", got.Provenance.SessionID)
	assert.Equal(t, provOwner, got.Provenance.UserID)
}

// An asset written before #1320 carries tool_calls and no captures key. The
// append must start the list without discarding what it already recorded.
func TestAppendProvenanceCaptureOntoALegacyRow_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()

	asset := provAsset("prov-legacy")
	require.NoError(t, store.Insert(ctx, asset))
	legacy := `{"session_id":"dps_old","tool_calls":[{"tool_name":"trino_query","timestamp":"2026-01-01T00:00:00Z"}]}`
	_, err := db.ExecContext(ctx, `UPDATE portal_assets SET provenance = $1::jsonb WHERE id = $2`, legacy, asset.ID)
	require.NoError(t, err)

	require.NoError(t, store.AppendProvenanceCapture(ctx, asset.ID, capture("manage_asset", 2, "e9")))

	got, err := store.Get(ctx, asset.ID)
	require.NoError(t, err)
	require.Len(t, got.Provenance.ToolCalls, 1, "the old shape is preserved")
	assert.Equal(t, "trino_query", got.Provenance.ToolCalls[0].ToolName)
	require.Len(t, got.Provenance.Captures, 1)
	assert.Equal(t, []string{"e9"}, got.Provenance.Captures[0].EventIDs)
}

// A row whose provenance column is JSON null or holds a captures key of the
// wrong type still gets its capture, rather than the append vanishing.
func TestAppendProvenanceCaptureOntoAMalformedRow_RealDB(t *testing.T) {
	// One container for both shapes: each subtest owns its own asset row, and
	// the real-DB gate starts a container per test as it is.
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()

	for name, stored := range map[string]string{
		"json null":              `null`,
		"captures is not a list": `{"captures":"none"}`,
	} {
		t.Run(name, func(t *testing.T) {
			asset := provAsset("prov-odd-" + strings.ReplaceAll(name, " ", "-"))
			require.NoError(t, store.Insert(ctx, asset))
			_, err := db.ExecContext(ctx,
				`UPDATE portal_assets SET provenance = $1::jsonb WHERE id = $2`, stored, asset.ID)
			require.NoError(t, err)

			require.NoError(t, store.AppendProvenanceCapture(ctx, asset.ID, capture("save_asset", 1, "e1")))

			var raw []byte
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT provenance FROM portal_assets WHERE id = $1`, asset.ID).Scan(&raw))
			var prov portaldomain.Provenance
			require.NoError(t, json.Unmarshal(raw, &prov))
			require.Len(t, prov.Captures, 1)
			assert.Equal(t, []string{"e1"}, prov.Captures[0].EventIDs)
		})
	}
}

// A capture cannot be recorded against an asset that does not exist, or one
// that has been deleted.
func TestAppendProvenanceCaptureMissingAsset_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()

	err := store.AppendProvenanceCapture(ctx, "no-such-asset", capture("save_asset", 1, "e1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	asset := provAsset("prov-deleted")
	require.NoError(t, store.Insert(ctx, asset))
	require.NoError(t, store.SoftDelete(ctx, asset.ID))
	assert.Error(t, store.AppendProvenanceCapture(ctx, asset.ID, capture("save_asset", 1, "e1")))
}
