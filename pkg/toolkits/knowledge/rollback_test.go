package knowledge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const rbURN = "mcp:dataset:(trino,acme,hive.sales.orders)"

// seededStore returns a changeset store pre-populated with cs so RollbackChangeset
// (which looks the changeset up by id) succeeds.
func seededStore(cs *Changeset) *spyChangesetStore {
	return &spyChangesetStore{Changesets: []Changeset{*cs}}
}

// changeEntry builds a single recorded-change map as stored in new_value.
func changeEntry(changeType, target, detail string) map[string]any {
	return map[string]any{"change_type": changeType, "target": target, "detail": detail}
}

func baseChangeset(id string, newValue, prevValue map[string]any) *Changeset {
	return &Changeset{
		ID:            id,
		TargetURN:     rbURN,
		CreatedAt:     time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		PreviousValue: prevValue,
		NewValue:      newValue,
	}
}

func TestParseRecordedChanges(t *testing.T) {
	nv := map[string]any{
		"change_0": changeEntry("add_tag", "", "pii"),
		"change_1": changeEntry("add_glossary_term", "", "mcp:glossary_term:x"),
	}
	got := parseRecordedChanges(nv)
	require.Len(t, got, 2)
	assert.Equal(t, "add_tag", got[0].ChangeType)
	assert.Equal(t, "mcp:glossary_term:x", got[1].Detail)
}

func TestNoopStore_ReturnToReview(t *testing.T) {
	returned, err := NewNoopStore().ReturnToReview(context.Background(), "x", "admin", "cs-1")
	assert.NoError(t, err)
	assert.False(t, returned)
}

// The note names the changeset so a reviewer seeing a pending insight that
// carries applied_by knows why it is back in the queue.
func TestRollbackReviewNote(t *testing.T) {
	assert.Contains(t, RollbackReviewNote("cs-7"), "cs-7")
	assert.NotEmpty(t, RollbackReviewNote(""))
	assert.True(t, strings.HasPrefix(RollbackReviewNote(""), "Returned to review:"),
		"the down migration matches returned insights on this prefix")
	assert.True(t, strings.HasPrefix(RollbackReviewNote("cs-7"), "Returned to review:"))
}

// A changeset applied to DataHub before the catalog replaced it recorded no
// catalog changes: it is refused, and nothing is marked rolled back.
func TestRevertChangeset_DataHubChangesetRefused(t *testing.T) {
	cs := baseChangeset("cs-old", map[string]any{"change_0": changeEntry("add_tag", "", "pii")}, nil)
	cs.TargetURN = "mcp:dataset:(trino,warehouse,hive.sales.orders)"
	store := seededStore(cs)
	_, err := RevertChangeset(context.Background(), RollbackDeps{Catalog: newFakeCatalog(), Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.ErrorIs(t, err, ErrDataHubChangeset)
	assert.False(t, store.Changesets[0].RolledBack)

	revertible, blocking := changesetRevertibility(cs)
	assert.False(t, revertible)
	assert.Equal(t, []string{"add_tag"}, blocking)
}

func TestRevertChangeset_AlreadyRolledBack(t *testing.T) {
	cs := baseChangeset("cs1", map[string]any{fieldCatalogChanges: []any{}}, nil)
	cs.RolledBack = true
	_, err := RevertChangeset(context.Background(), RollbackDeps{Catalog: newFakeCatalog(), Changesets: seededStore(cs)}, cs, "admin")
	assert.ErrorIs(t, err, ErrChangesetAlreadyRolledBack)
}

// A changeset that created a prompt is not rolled back: the rollback would
// leave the prompt behind.
func TestRevertChangeset_PromptIsUnrevertible(t *testing.T) {
	cs := baseChangeset("cs1", map[string]any{
		"change_0":          changeEntry("add_prompt", "p", "body"),
		fieldCatalogChanges: []any{},
	}, nil)
	_, err := RevertChangeset(context.Background(), RollbackDeps{Catalog: newFakeCatalog(), Changesets: seededStore(cs)}, cs, "admin")
	var unrevertible *UnrevertibleError
	require.ErrorAs(t, err, &unrevertible)
	assert.Equal(t, []string{"add_prompt"}, unrevertible.ChangeTypes)
}
