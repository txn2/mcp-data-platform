//go:build integration

package postgres

// Real-Postgres proof for the prompt versioning lifecycle (#1009): create
// snapshots v1, versioned updates append applied snapshots, a review-gated
// draft leaves the live row untouched until ApproveVersion applies it, and
// approval stamps bind immutably to the version they approved. Runs against
// the actual prompt_versions schema (constraints, defaults, FK cascade) that
// the sqlmock tests rubber-stamp.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

func TestVersioning_RealDB_Lifecycle(t *testing.T) {
	store := New(testdb.New(t))
	ctx := context.Background()

	// Create -> v1 applied snapshot.
	p := &prompt.Prompt{
		Name: "sales-report", DisplayName: "Sales Report", Content: "v1 body",
		Scope: prompt.ScopeGlobal, Source: prompt.SourceOperator, Enabled: true,
		OwnerEmail: "jane@example.com",
	}
	require.NoError(t, store.Create(ctx, p))
	assert.Equal(t, 1, p.Version)

	v1, err := store.GetVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	require.NotNil(t, v1, "create writes the v1 snapshot")
	assert.Equal(t, "v1 body", v1.Content)
	assert.Equal(t, "jane@example.com", v1.Author)
	assert.Equal(t, prompt.VersionStatusApplied, v1.Status)
	assert.Empty(t, v1.ApprovedBy, "an unapproved draft prompt has no approval stamp")

	// First approval (draft -> approved via plain Update) stamps v1.
	loaded, err := store.Get(ctx, "sales-report")
	require.NoError(t, err)
	require.NoError(t, loaded.ApplyStatusTransition(prompt.StatusApproved, "", "admin@example.com", true, time.Now().UTC()))
	require.NoError(t, store.Update(ctx, loaded))

	v1, err = store.GetVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", v1.ApprovedBy, "first approval binds to the v1 snapshot")
	require.NotNil(t, v1.ApprovedAt)
	v1Stamp := *v1.ApprovedAt

	// A versioned metadata edit appends an applied v2 without re-approval.
	loaded, err = store.Get(ctx, "sales-report")
	require.NoError(t, err)
	loaded.Description = "now with a description"
	require.NoError(t, store.UpdateWithVersion(ctx, loaded, "jane@example.com"))
	assert.Equal(t, 2, loaded.Version)

	// A review-gated content edit lands as a draft v3; the live row is untouched.
	proposed := *loaded
	proposed.Content = "v3 proposed body"
	n, err := store.CreateDraftVersion(ctx, p.ID, &proposed, "sam@example.com")
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	live, err := store.Get(ctx, "sales-report")
	require.NoError(t, err)
	assert.Equal(t, "v1 body", live.Content, "the draft does not change the served content")
	assert.Equal(t, 2, live.Version)

	// A second competing draft.
	proposed.Content = "v4 competing body"
	n2, err := store.CreateDraftVersion(ctx, p.ID, &proposed, "kim@example.com")
	require.NoError(t, err)
	assert.Equal(t, 4, n2)

	// Approving v3 applies its snapshot, stamps it, and supersedes v4.
	updated, err := store.ApproveVersion(ctx, p.ID, 3, "admin2@example.com")
	require.NoError(t, err)
	assert.Equal(t, "v3 proposed body", updated.Content)
	assert.Equal(t, 3, updated.Version)
	assert.Equal(t, "admin2@example.com", updated.ApprovedBy)

	live, err = store.Get(ctx, "sales-report")
	require.NoError(t, err)
	assert.Equal(t, "v3 proposed body", live.Content, "the approved snapshot is now served")

	v3, err := store.GetVersion(ctx, p.ID, 3)
	require.NoError(t, err)
	assert.Equal(t, prompt.VersionStatusApplied, v3.Status)
	assert.Equal(t, "admin2@example.com", v3.ApprovedBy)

	v4, err := store.GetVersion(ctx, p.ID, 4)
	require.NoError(t, err)
	assert.Equal(t, prompt.VersionStatusSuperseded, v4.Status, "a stale competing draft is superseded")

	// Approving v3 did not alter v1's recorded approval.
	v1Again, err := store.GetVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", v1Again.ApprovedBy)
	assert.True(t, v1Again.ApprovedAt.Equal(v1Stamp), "the v1 approval stamp is immutable")

	// History is complete, newest first, with full content and author.
	versions, err := store.ListVersions(ctx, p.ID)
	require.NoError(t, err)
	require.Len(t, versions, 4)
	assert.Equal(t, []int{4, 3, 2, 1}, []int{versions[0].Version, versions[1].Version, versions[2].Version, versions[3].Version})
	assert.Equal(t, "sam@example.com", versions[1].Author)

	// A fresh draft can be rejected without touching the live row.
	proposed.Content = "v5 rejected body"
	n5, err := store.CreateDraftVersion(ctx, p.ID, &proposed, "sam@example.com")
	require.NoError(t, err)
	require.NoError(t, store.RejectVersion(ctx, p.ID, n5))
	v5, err := store.GetVersion(ctx, p.ID, n5)
	require.NoError(t, err)
	assert.Equal(t, prompt.VersionStatusRejected, v5.Status)
	live, err = store.Get(ctx, "sales-report")
	require.NoError(t, err)
	assert.Equal(t, "v3 proposed body", live.Content)

	// Approving a draft NUMBERED BELOW the live version (a metadata edit
	// advanced the live row past a pending draft) must set the live row to
	// exactly the draft's number, not keep the higher one.
	proposed.Content = "v6 draft body"
	n6, err := store.CreateDraftVersion(ctx, p.ID, &proposed, "sam@example.com")
	require.NoError(t, err)
	loaded, err = store.Get(ctx, "sales-report")
	require.NoError(t, err)
	loaded.Description = "metadata edit after the draft"
	require.NoError(t, store.UpdateWithVersion(ctx, loaded, "jane@example.com"))
	require.Greater(t, loaded.Version, n6, "the applied metadata edit outnumbers the pending draft")

	updated, err = store.ApproveVersion(ctx, p.ID, n6, "admin2@example.com")
	require.NoError(t, err)
	assert.Equal(t, n6, updated.Version)
	live, err = store.Get(ctx, "sales-report")
	require.NoError(t, err)
	assert.Equal(t, "v6 draft body", live.Content)
	assert.Equal(t, n6, live.Version, "the live row reports exactly the snapshot it serves")

	// Deleting the prompt cascades its history.
	require.NoError(t, store.DeleteByID(ctx, p.ID))
	versions, err = store.ListVersions(ctx, p.ID)
	require.NoError(t, err)
	assert.Empty(t, versions, "prompt_versions rows cascade with the prompt")
}
