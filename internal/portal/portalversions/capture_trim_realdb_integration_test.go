//go:build integration

package portalversions

// Real-Postgres tests for the captures a prune takes with the versions it
// removes (#1623). The trim is a jsonb_agg over jsonb_array_elements with
// ORDINALITY and an int cast of a JSON field; sqlmock matches it as a string
// and returns whatever the test supplies, so only a real PostgreSQL says
// whether the statement parses, whether the cast survives a capture with no
// version, and which captures actually survive.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// seedCaptures replaces an asset's provenance with one capture per version
// listed, in the order given, as the append path writes them. A version of 0
// stands for a capture taken before the platform recorded which version it
// produced.
func seedCaptures(t *testing.T, db *sql.DB, assetID string, versions ...int) {
	t.Helper()
	captures := make([]portaldomain.ProvenanceCapture, 0, len(versions))
	for _, v := range versions {
		captures = append(captures, portaldomain.ProvenanceCapture{
			Tool: "manage_asset", Version: v, SessionID: "dps_abc",
			Calls: []portaldomain.ProvenanceCall{{
				EventID: "evt", Kind: portaldomain.ProvenanceKindSQL, Tool: "trino_query",
				Outcome: portaldomain.ProvenanceOutcomeSuccess,
			}},
		})
	}
	payload, err := json.Marshal(portaldomain.Provenance{Captures: captures})
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(),
		`UPDATE portal_assets SET provenance = $1 WHERE id = $2`, payload, assetID)
	require.NoError(t, err)
}

// capturedVersions reads back the versions the asset's captures name, in stored
// order.
func capturedVersions(t *testing.T, db *sql.DB, assetID string) []int {
	t.Helper()
	var raw []byte
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT provenance FROM portal_assets WHERE id = $1`, assetID).Scan(&raw))
	var prov portaldomain.Provenance
	require.NoError(t, json.Unmarshal(raw, &prov))
	out := make([]int, 0, len(prov.Captures))
	for _, c := range prov.Captures {
		out = append(out, c.Version)
	}
	return out
}

// The headline criterion: after a write pushes history past the cap, the
// captures for the pruned versions are gone and the origin capture stays, so
// the version list and the captures agree on what is kept.
func TestPrune_RealDB_CapturesFollowTheirVersions(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgres(db, &recordingDeleter{}, nil, nil)
	id := seedAsset(t, db, intPtr(3))
	for n := 2; n <= 6; n++ {
		writeVersion(t, store, id, n)
	}
	// Six writes, six captures, before any of them is trimmed.
	seedCaptures(t, db, id, 1, 2, 3, 4, 5, 6)

	writeVersion(t, store, id, 7)

	assert.Equal(t, []int{5, 6, 7}, liveVersions(t, db, id))
	assert.Equal(t, []int{1, 5, 6}, capturedVersions(t, db, id),
		"the captures below the watermark go, and the origin capture stays")
}

// A capture the platform could not tie to a version has nothing to match
// against a pruned one, so it survives rather than being guessed at.
func TestPrune_RealDB_KeepsACaptureThatNamesNoVersion(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgres(db, &recordingDeleter{}, nil, nil)
	id := seedAsset(t, db, intPtr(2))
	for n := 2; n <= 5; n++ {
		writeVersion(t, store, id, n)
	}
	seedCaptures(t, db, id, 0, 2, 3, 4, 5)

	// Version 6 pushes the watermark to 4, so the captures for versions 2, 3
	// and 4 go. The version-less one stays beside the newest kept.
	writeVersion(t, store, id, 6)

	assert.Equal(t, []int{0, 5}, capturedVersions(t, db, id))
}

// An asset keeping every version keeps every capture: there is no pruned
// version for a capture to follow.
func TestPrune_RealDB_UnlimitedTrimsNothing(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgres(db, &recordingDeleter{}, nil, nil)
	id := seedAsset(t, db, intPtr(portaldomain.MaxVersionsUnlimited))
	for n := 2; n <= 5; n++ {
		writeVersion(t, store, id, n)
	}
	seedCaptures(t, db, id, 1, 2, 3, 4, 5)

	writeVersion(t, store, id, 6)

	assert.Equal(t, []int{1, 2, 3, 4, 5}, capturedVersions(t, db, id))
}

// A capture holding something other than a number where a version belongs is
// kept rather than failing the write the prune runs inside. The trim runs in
// the transaction that records a content write, so a cast that threw would
// lose the content.
func TestPrune_RealDB_ACaptureWithANonNumericVersionDoesNotFailTheWrite(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgres(db, &recordingDeleter{}, nil, nil)
	id := seedAsset(t, db, intPtr(2))
	for n := 2; n <= 5; n++ {
		writeVersion(t, store, id, n)
	}
	_, err := db.ExecContext(context.Background(), `
		UPDATE portal_assets
		SET provenance = '{"captures":[{"tool":"save_asset","version":"three"},{"tool":"manage_asset","version":4}]}'
		WHERE id = $1`, id)
	require.NoError(t, err)

	writeVersion(t, store, id, 6)

	var raw []byte
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT provenance FROM portal_assets WHERE id = $1`, id).Scan(&raw))
	assert.Contains(t, string(raw), `"three"`,
		"a capture whose version is unreadable is kept, and the write it ran inside stands")
}

// A row whose provenance predates captures entirely is left alone: the guard is
// on the shape, not on the key being present.
func TestPrune_RealDB_LeavesALegacyProvenanceAlone(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgres(db, &recordingDeleter{}, nil, nil)
	id := seedAsset(t, db, intPtr(2))
	_, err := db.ExecContext(context.Background(),
		`UPDATE portal_assets SET provenance = '{"tool_calls":[{"tool_name":"trino_query"}]}' WHERE id = $1`, id)
	require.NoError(t, err)
	for n := 2; n <= 6; n++ {
		writeVersion(t, store, id, n)
	}

	var raw []byte
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT provenance FROM portal_assets WHERE id = $1`, id).Scan(&raw))
	var prov portaldomain.Provenance
	require.NoError(t, json.Unmarshal(raw, &prov))
	assert.Len(t, prov.ToolCalls, 1, "the legacy shape survives a prune untouched")
	assert.Empty(t, prov.Captures)
}
