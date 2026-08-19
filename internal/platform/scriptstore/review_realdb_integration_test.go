//go:build integration

package scriptstore

// The real-schema proof for the #1287 review queue. The queue is a predicate,
// not a filter applied in Go: "a pending draft, or the live version of a script
// with no approved version, one row per script, superseded scripts excluded".
// sqlmock cannot evaluate that — it returns whatever rows a test hands it — so
// what the predicate actually selects is only knowable against Postgres.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// draftOn proposes an edit to an already-approved script, which the edit funnel
// defers into a pending draft rather than applying.
func draftOn(ctx context.Context, t *testing.T, s *Store, live *script.Script, source string) int {
	t.Helper()
	before := *live
	after := *live
	after.Source = source
	outcome, err := script.ApplyEdit(ctx, s, script.Edit{Before: &before, After: &after, Author: testAuthor})
	require.NoError(t, err)
	require.False(t, outcome.Applied, "a gated script defers its edits")
	return outcome.PendingVersion
}

// findReview returns the queue row for a script, or nil.
func findReview(rows []script.PendingReview, scriptID string) *script.PendingReview {
	for i := range rows {
		if rows[i].ScriptID == scriptID {
			return &rows[i]
		}
	}
	return nil
}

// TestRealDB_QueueHoldsWhatIsNotExecuting is the whole predicate: a script
// nobody has approved is waiting even though its only version reads as
// "applied", an approved script with nothing proposed is not waiting, and an
// approved script with a proposed change is waiting again.
func TestRealDB_QueueHoldsWhatIsNotExecuting(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	// Never approved: the live version 1 is status "applied", so a queue built
	// on status alone would miss it entirely.
	fresh := newScript("fresh", "jane@example.com")
	require.NoError(t, s.Create(ctx, fresh, testAuthor))

	// Approved and unchanged: nothing is waiting on a reviewer.
	settled, _ := approvedScript(ctx, t, s, "settled")

	// Approved with a proposed change: the draft is waiting, and the approved
	// version keeps executing meanwhile.
	changed, _ := approvedScript(ctx, t, s, "changed")
	draftVersion := draftOn(ctx, t, s, changed, "print('v2')\n")

	rows, err := s.ListPendingReviews(ctx)
	require.NoError(t, err)

	freshRow := findReview(rows, fresh.ID)
	require.NotNil(t, freshRow, "a script with no approved version is waiting for a first approval")
	assert.True(t, freshRow.FirstApproval)
	assert.Equal(t, 1, freshRow.Version)
	assert.Equal(t, script.VersionStatusApplied, freshRow.VersionStatus)
	assert.Equal(t, testAuthor.Roles, freshRow.AuthorRoles,
		"the queue shows the authority approving would bind")

	assert.Nil(t, findReview(rows, settled.ID),
		"an approved script with nothing proposed is not in the queue")

	changedRow := findReview(rows, changed.ID)
	require.NotNil(t, changedRow)
	assert.False(t, changedRow.FirstApproval, "this one is a change to something already running")
	assert.Equal(t, draftVersion, changedRow.Version)
	assert.Equal(t, script.VersionStatusDraft, changedRow.VersionStatus)
}

// TestRealDB_QueueHoldsOneRowPerScript: approving any version supersedes every
// other pending draft of the same script, so competing drafts are one decision
// and the queue shows the newest of them.
func TestRealDB_QueueHoldsOneRowPerScript(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	live, _ := approvedScript(ctx, t, s, "busy")
	draftOn(ctx, t, s, live, "print('a')\n")
	newest := draftOn(ctx, t, s, live, "print('b')\n")

	rows, err := s.ListPendingReviews(ctx)
	require.NoError(t, err)

	var seen int
	for _, r := range rows {
		if r.ScriptID == live.ID {
			seen++
			assert.Equal(t, newest, r.Version, "the reviewable proposal is the newest draft")
		}
	}
	assert.Equal(t, 1, seen, "two drafts of one script are one decision, not two queue rows")
}

// TestRealDB_ScriptsTheGateRefusesAreNotQueued: the execution gate refuses a
// disabled, deprecated, or superseded script whatever a reviewer decides, so
// queueing one would hold a decision nobody can make — and nothing could work
// it off, since rejecting is confined to drafts. Re-enabling brings it back.
func TestRealDB_ScriptsTheGateRefusesAreNotQueued(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	for _, tt := range []struct {
		name string
		park func(sc *script.Script)
	}{
		{"disabled", func(sc *script.Script) { sc.Enabled = false }},
		{"deprecated", func(sc *script.Script) { sc.Status = script.StatusDeprecated }},
		{"superseded", func(sc *script.Script) {
			sc.Status = script.StatusSuperseded
			sc.SupersededBy = "retired-v2"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sc := newScript("parked-"+tt.name, "jane@example.com")
			require.NoError(t, s.Create(ctx, sc, testAuthor))

			rows, err := s.ListPendingReviews(ctx)
			require.NoError(t, err)
			require.NotNil(t, findReview(rows, sc.ID), "a live script is waiting for its first approval")

			tt.park(sc)
			require.NoError(t, s.Update(ctx, sc))

			rows, err = s.ListPendingReviews(ctx)
			require.NoError(t, err)
			assert.Nil(t, findReview(rows, sc.ID),
				"a script the gate refuses is not a decision anybody can make")
		})
	}
}

// TestRealDB_ReenablingRestoresTheQueueRow: parking a script is not a way to
// lose a pending review, only to defer it.
func TestRealDB_ReenablingRestoresTheQueueRow(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("paused", "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor))
	sc.Enabled = false
	require.NoError(t, s.Update(ctx, sc))

	rows, err := s.ListPendingReviews(ctx)
	require.NoError(t, err)
	require.Nil(t, findReview(rows, sc.ID))

	sc.Enabled = true
	require.NoError(t, s.Update(ctx, sc))

	rows, err = s.ListPendingReviews(ctx)
	require.NoError(t, err)
	assert.NotNil(t, findReview(rows, sc.ID))
}

// TestRealDB_RejectResolvesOnlyPendingDrafts: rejecting takes a proposal out of
// the queue and changes nothing about what executes, and anything that is not a
// pending draft is refused rather than relabelled.
func TestRealDB_RejectResolvesOnlyPendingDrafts(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	live, approved := approvedScript(ctx, t, s, "reviewed")
	draftVersion := draftOn(ctx, t, s, live, "print('proposed')\n")

	require.NoError(t, s.RejectVersion(ctx, live.ID, draftVersion))

	rejected, err := s.GetVersion(ctx, live.ID, draftVersion)
	require.NoError(t, err)
	assert.Equal(t, script.VersionStatusRejected, rejected.Status)

	after, err := s.GetByID(ctx, live.ID)
	require.NoError(t, err)
	assert.Equal(t, approved.ID, after.ApprovedVersionID, "rejecting decides nothing about what runs")
	assert.Equal(t, approved.Source, after.Source)

	rows, err := s.ListPendingReviews(ctx)
	require.NoError(t, err)
	assert.Nil(t, findReview(rows, live.ID), "a rejected draft leaves the queue")

	assert.ErrorIs(t, s.RejectVersion(ctx, live.ID, draftVersion), script.ErrVersionConflict,
		"a draft cannot be rejected twice")
	assert.ErrorIs(t, s.RejectVersion(ctx, live.ID, approved.Version), script.ErrVersionConflict,
		"an approved version is not a pending draft")
	assert.ErrorIs(t, s.RejectVersion(ctx, live.ID, 99), script.ErrVersionConflict,
		"a version that does not exist cannot be rejected")
}

// TestRealDB_ApprovingClearsTheQueueRow closes the loop the surface actually
// drives: the row a reviewer approved stops being pending, and the approval
// moves the gate.
func TestRealDB_ApprovingClearsTheQueueRow(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("approve-me", "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor))

	rows, err := s.ListPendingReviews(ctx)
	require.NoError(t, err)
	require.NotNil(t, findReview(rows, sc.ID))

	_, err = s.ApproveVersion(ctx, sc.ID, 1, "admin@example.com", script.Grants{
		Capabilities: []string{script.CapabilityQuery},
	})
	require.NoError(t, err)

	rows, err = s.ListPendingReviews(ctx)
	require.NoError(t, err)
	assert.Nil(t, findReview(rows, sc.ID), "an approved version is no longer waiting")
}
