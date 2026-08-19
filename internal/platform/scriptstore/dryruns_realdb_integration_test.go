//go:build integration

package scriptstore

// The real-schema proof for the portal run loop's storage (#1363, #1364). Two
// things here cannot be proved with sqlmock, which returns whatever a test
// hands it:
//
//   - The trim. Whether "keep the newest N per author" actually keeps the
//     newest N is a property of the DELETE's subquery against real rows and a
//     real ordering, not of the string it is spelled with.
//   - The trigger_kind CHECK. A run recorded as 'portal' either satisfies the
//     constraint 000114 declares or it does not, and only Postgres knows.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestRealDB_DryRunAccountResolvesBySource is the reviewer's lookup end to end:
// an account written for one source is found by that source and by no other.
func TestRealDB_DryRunAccountResolvesBySource(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("documented", "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor))

	const ran = "print(\"ran\")\n"
	account := &script.DryRun{
		ID: "dpx_draft_real_1", ScriptID: sc.ID, SourceSHA256: script.SourceDigest(ran),
		RequestedBy: "jane@example.com", Status: script.RunStatusSucceeded,
		Log:     "ran",
		Metrics: script.RunMetrics{Steps: 4, DurationMS: 12, Queries: 1},
		Outputs: []script.DryRunOutput{{Name: "daily", Format: "csv", RowCount: 3, Bytes: 90}},
	}
	require.NoError(t, s.RecordDryRun(ctx, account))
	assert.False(t, account.CreatedAt.IsZero(), "the stamp comes from the database clock")

	found, err := s.LatestDryRun(ctx, sc.ID, script.SourceDigest(ran))
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "dpx_draft_real_1", found.ID)
	assert.Equal(t, uint64(4), found.Metrics.Steps)
	require.Len(t, found.Outputs, 1)
	assert.Equal(t, 90, found.Outputs[0].Bytes)

	// A version carrying different code inherits nothing: one added newline is
	// a different version, and it was never run.
	other, err := s.LatestDryRun(ctx, sc.ID, script.SourceDigest(ran+"\n"))
	require.NoError(t, err)
	assert.Nil(t, other)
}

// TestRealDB_DryRunHistoryIsBoundedPerAuthor proves the table cannot grow with
// an afternoon of iteration, and that what survives is the newest.
func TestRealDB_DryRunHistoryIsBoundedPerAuthor(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("iterated", "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor))

	const attempts = dryRunHistoryPerAuthor + 5
	for i := range attempts {
		source := fmt.Sprintf("x = %d\n", i)
		require.NoError(t, s.RecordDryRun(ctx, &script.DryRun{
			ID: fmt.Sprintf("dpx_draft_real_%d", i), ScriptID: sc.ID,
			SourceSHA256: script.SourceDigest(source),
			RequestedBy:  "jane@example.com", Status: script.RunStatusSucceeded,
		}))
	}

	var kept int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM script_dry_runs WHERE script_id = $1`, sc.ID).Scan(&kept))
	assert.Equal(t, dryRunHistoryPerAuthor, kept, "the trim bounds the author's history")

	// The newest survives and the oldest is gone, which is the direction that
	// matters: an author's most recent attempts are the ones they are working on.
	newest, err := s.LatestDryRun(ctx, sc.ID, script.SourceDigest(fmt.Sprintf("x = %d\n", attempts-1)))
	require.NoError(t, err)
	assert.NotNil(t, newest)

	oldest, err := s.LatestDryRun(ctx, sc.ID, script.SourceDigest("x = 0\n"))
	require.NoError(t, err)
	assert.Nil(t, oldest)
}

// TestRealDB_AnotherAuthorsHistoryIsUntouched keeps the bound per person: one
// author iterating must not evict the account a reviewer is about to read on
// somebody else's version.
func TestRealDB_AnotherAuthorsHistoryIsUntouched(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("shared-authoring", "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor))

	const carolsSource = "carol = 1\n"
	require.NoError(t, s.RecordDryRun(ctx, &script.DryRun{
		ID: "dpx_draft_carol", ScriptID: sc.ID, SourceSHA256: script.SourceDigest(carolsSource),
		RequestedBy: "carol@example.com", Status: script.RunStatusSucceeded,
	}))

	for i := range dryRunHistoryPerAuthor + 3 {
		require.NoError(t, s.RecordDryRun(ctx, &script.DryRun{
			ID: fmt.Sprintf("dpx_draft_jane_%d", i), ScriptID: sc.ID,
			SourceSHA256: script.SourceDigest(fmt.Sprintf("jane = %d\n", i)),
			RequestedBy:  "jane@example.com", Status: script.RunStatusSucceeded,
		}))
	}

	found, err := s.LatestDryRun(ctx, sc.ID, script.SourceDigest(carolsSource))
	require.NoError(t, err)
	require.NotNil(t, found, "one author's iteration must not evict another's account")
	assert.Equal(t, "carol@example.com", found.RequestedBy)
}

// TestRealDB_APortalRunIsAcceptedByTheTriggerCheck proves 000114 widened the
// constraint: without it every run an owner asks for on their own page would be
// rejected by the database.
func TestRealDB_APortalRunIsAcceptedByTheTriggerCheck(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	live, version := approvedScript(ctx, t, s, "portal-triggered")
	run := &script.Run{
		ID: "dpx_portal_1", ScriptID: live.ID, VersionID: version.ID, Version: version.Version,
		Trigger: script.TriggerPortal, RequestedBy: "jane@example.com",
	}
	require.NoError(t, s.Enqueue(ctx, run))

	stored, err := s.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, script.TriggerPortal, stored.Trigger,
		"the history must say the owner asked for this run, not an agent")
}
