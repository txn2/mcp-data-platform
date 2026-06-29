package knowledge

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const rbURN = "urn:li:dataset:rb"

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

func TestRevertChangeset_RemovesAddedTerm(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:new")},
		map[string]any{"glossary_terms": []any{"urn:li:glossaryTerm:canonical"}},
	)
	cs.SourceInsightIDs = []string{"ins-1"}
	store := seededStore(cs)
	writer := &spyWriter{}
	insights := &fullSpyStore{Insights: []Insight{{ID: "ins-1", Status: StatusApplied}}}

	res, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: insights}, cs, "admin")
	require.NoError(t, err)
	require.Len(t, writer.WriteCalls, 1)
	assert.Equal(t, "ApplyGlossaryTermChanges", writer.WriteCalls[0].Method)
	assert.Empty(t, writer.WriteCalls[0].Arg1, "no adds")
	assert.Equal(t, "urn:li:glossaryTerm:new", writer.WriteCalls[0].Arg2)
	assert.True(t, store.Changesets[0].RolledBack)
	assert.Equal(t, []string{"ins-1"}, res.InsightsRolledBack)
	assert.Equal(t, StatusRolledBack, insights.Insights[0].Status)
	assert.Len(t, res.RevertedChanges, 1)
}

func TestRevertChangeset_KeepsPreExistingTerm(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:canonical")},
		map[string]any{"glossary_terms": []any{"urn:li:glossaryTerm:canonical"}},
	)
	store := seededStore(cs)
	writer := &spyWriter{}

	res, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.NoError(t, err)
	assert.Empty(t, writer.WriteCalls, "pre-existing term must not be removed")
	assert.Len(t, res.SkippedChanges, 1)
	assert.True(t, store.Changesets[0].RolledBack)
}

func TestRevertChangeset_RemovesAddedTag(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("add_tag", "", "pii")},
		map[string]any{"tags": []any{}},
	)
	store := seededStore(cs)
	writer := &spyWriter{}

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.NoError(t, err)
	// The added tag is reverted via a single batched ApplyTagChanges removing it.
	require.Len(t, writer.WriteCalls, 1)
	assert.Equal(t, "ApplyTagChanges", writer.WriteCalls[0].Method)
	assert.Empty(t, writer.WriteCalls[0].Arg1, "no adds")
	assert.Equal(t, normalizeTagURN("pii"), writer.WriteCalls[0].Arg2)
}

// TestRevertChangeset_MultiTagBatch is the #721 regression for rollback: reverting
// a changeset that added several tags must remove them in a single batched
// ApplyTagChanges, not a sequence of per-tag writes that read stale state and
// could leave the entity with zero (or the wrong set of) tags.
func TestRevertChangeset_MultiTagBatch(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{
			"change_0": changeEntry("add_tag", "", "a"),
			"change_1": changeEntry("add_tag", "", "b"),
			"change_2": changeEntry("add_tag", "", "c"),
		},
		// Tag "a" pre-existed, so it must be kept; b and c were added by the changeset.
		map[string]any{"tags": []any{normalizeTagURN("a")}},
	)
	store := seededStore(cs)
	writer := &spyWriter{}

	res, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.NoError(t, err)

	// Exactly one batched write removing only the newly-added tags.
	require.Len(t, writer.WriteCalls, 1)
	assert.Equal(t, "ApplyTagChanges", writer.WriteCalls[0].Method)
	assert.Empty(t, writer.WriteCalls[0].Arg1, "no adds")
	assert.Equal(t, normalizeTagURN("b")+","+normalizeTagURN("c"), writer.WriteCalls[0].Arg2)

	// The pre-existing tag "a" is kept (skipped), b and c are reverted.
	assert.Len(t, res.RevertedChanges, 2)
	assert.Len(t, res.SkippedChanges, 1)
}

// TestRevertChangeset_MultiGlossaryTermBatch is the #729 regression for rollback:
// reverting a changeset that added several glossary terms must remove them in a
// single batched ApplyGlossaryTermChanges, keeping any pre-existing term.
func TestRevertChangeset_MultiGlossaryTermBatch(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{
			"change_0": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:a"),
			"change_1": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:b"),
			"change_2": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:c"),
		},
		// Term "a" pre-existed, so it must be kept; b and c were added by the changeset.
		map[string]any{"glossary_terms": []any{"urn:li:glossaryTerm:a"}},
	)
	store := seededStore(cs)
	writer := &spyWriter{}

	res, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.NoError(t, err)

	// Exactly one batched write removing only the newly-added terms.
	require.Len(t, writer.WriteCalls, 1)
	assert.Equal(t, "ApplyGlossaryTermChanges", writer.WriteCalls[0].Method)
	assert.Empty(t, writer.WriteCalls[0].Arg1, "no adds")
	assert.Equal(t, "urn:li:glossaryTerm:b,urn:li:glossaryTerm:c", writer.WriteCalls[0].Arg2)

	// The pre-existing term "a" is kept (skipped), b and c are reverted.
	assert.Len(t, res.RevertedChanges, 2)
	assert.Len(t, res.SkippedChanges, 1)
}

// TestRevertChangeset_PartialRevertReportsProgress verifies that when an earlier
// batched revert (tags) succeeds but a later one (glossary terms) fails, the error
// reports what was already reverted rather than discarding it (and claiming zero).
func TestRevertChangeset_PartialRevertReportsProgress(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{
			"change_0": changeEntry("add_tag", "", "pii"),
			"change_1": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:revenue"),
		},
		map[string]any{"tags": []any{}, "glossary_terms": []any{}},
	)
	store := seededStore(cs)
	// Tag revert is call 1 (succeeds); glossary-term revert is call 2 (fails).
	writer := &spyWriter{FailAtCall: 2}

	res, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.Error(t, err)
	assert.Nil(t, res)
	// The tag was actually removed from DataHub, so the error must not claim zero
	// changes were reverted.
	assert.Contains(t, err.Error(), "reverting 1 change")
	assert.NotContains(t, err.Error(), "reverting 0 change")
}

func TestRevertChangeset_ReAddsRemovedTag(t *testing.T) {
	tagURN := normalizeTagURN("pii")
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("remove_tag", "", "pii")},
		map[string]any{"tags": []any{tagURN}},
	)
	store := seededStore(cs)
	writer := &spyWriter{}

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.NoError(t, err)
	// The removed tag is restored via a single batched ApplyTagChanges adding it.
	require.Len(t, writer.WriteCalls, 1)
	assert.Equal(t, "ApplyTagChanges", writer.WriteCalls[0].Method)
	assert.Equal(t, tagURN, writer.WriteCalls[0].Arg1)
	assert.Empty(t, writer.WriteCalls[0].Arg2, "no removes")
}

func TestRevertChangeset_RemovedTagNotPreviouslyPresentIsNoop(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("remove_tag", "", "pii")},
		map[string]any{"tags": []any{}},
	)
	store := seededStore(cs)
	writer := &spyWriter{}

	res, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.NoError(t, err)
	assert.Empty(t, writer.WriteCalls)
	assert.Len(t, res.SkippedChanges, 1)
}

func TestRevertChangeset_FlagQualityIssueRemovesTag(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("flag_quality_issue", "", "stale data")},
		map[string]any{"tags": []any{}},
	)
	store := seededStore(cs)
	writer := &spyWriter{}

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.NoError(t, err)
	// The QualityIssue tag is reverted via a single batched ApplyTagChanges removing it.
	require.Len(t, writer.WriteCalls, 1)
	assert.Equal(t, "ApplyTagChanges", writer.WriteCalls[0].Method)
	assert.Empty(t, writer.WriteCalls[0].Arg1, "no adds")
	assert.Equal(t, qualityIssueTagURN, writer.WriteCalls[0].Arg2)
}

func TestRevertChangeset_RestoresDescription(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("update_description", "", "new desc")},
		map[string]any{"description": "old desc"},
	)
	store := seededStore(cs)
	writer := &spyWriter{}

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.NoError(t, err)
	require.Len(t, writer.WriteCalls, 1)
	assert.Equal(t, "UpdateDescription", writer.WriteCalls[0].Method)
	assert.Equal(t, "old desc", writer.WriteCalls[0].Arg1)
}

func TestRevertChangeset_RemovesDocumentationLink(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("add_documentation", "https://docs/x", "the docs")},
		map[string]any{},
	)
	store := seededStore(cs)
	writer := &spyWriter{}

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.NoError(t, err)
	require.Len(t, writer.WriteCalls, 1)
	assert.Equal(t, "RemoveDocumentationLink", writer.WriteCalls[0].Method)
	assert.Equal(t, "https://docs/x", writer.WriteCalls[0].Arg1)
}

func TestRevertChangeset_AlreadyRolledBack(t *testing.T) {
	cs := baseChangeset("cs1", map[string]any{}, map[string]any{})
	cs.RolledBack = true

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: &spyWriter{}, Changesets: seededStore(cs), Insights: &fullSpyStore{}}, cs, "admin")
	assert.ErrorIs(t, err, ErrChangesetAlreadyRolledBack)
}

func TestRevertChangeset_UnrevertibleChangeType(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("set_structured_property", "urn:li:structuredProperty:x", "v")},
		map[string]any{},
	)
	store := seededStore(cs)
	writer := &spyWriter{}

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	var unrev *UnrevertibleError
	require.ErrorAs(t, err, &unrev)
	assert.Equal(t, []string{"set_structured_property"}, unrev.ChangeTypes)
	assert.Empty(t, writer.WriteCalls, "must not mutate DataHub for an unrevertible changeset")
	assert.False(t, store.Changesets[0].RolledBack)
}

func TestRevertChangeset_ColumnDescriptionIsUnrevertible(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("update_description", "column:amount", "new col desc")},
		map[string]any{"description": "entity desc"},
	)
	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: &spyWriter{}, Changesets: seededStore(cs), Insights: &fullSpyStore{}}, cs, "admin")
	var unrev *UnrevertibleError
	require.ErrorAs(t, err, &unrev)
	assert.Equal(t, []string{"update_description"}, unrev.ChangeTypes)
}

func TestRevertChangeset_ConflictWithNewerChangeset(t *testing.T) {
	cs := baseChangeset("cs-old",
		map[string]any{"change_0": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:a")},
		map[string]any{"glossary_terms": []any{}},
	)
	newer := baseChangeset("cs-new",
		map[string]any{"change_0": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:b")},
		map[string]any{},
	)
	newer.CreatedAt = cs.CreatedAt.Add(time.Hour)
	store := &spyChangesetStore{Changesets: []Changeset{*cs, *newer}}
	writer := &spyWriter{}

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	var conflict *RollbackConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, []string{"cs-new"}, conflict.ConflictingIDs)
	assert.Equal(t, []string{"glossary_terms"}, conflict.Aspects)
	assert.Empty(t, writer.WriteCalls)
}

func TestRevertChangeset_RolledBackNewerChangesetIsNotConflict(t *testing.T) {
	cs := baseChangeset("cs-old",
		map[string]any{"change_0": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:a")},
		map[string]any{"glossary_terms": []any{}},
	)
	newer := baseChangeset("cs-new",
		map[string]any{"change_0": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:b")},
		map[string]any{},
	)
	newer.CreatedAt = cs.CreatedAt.Add(time.Hour)
	newer.RolledBack = true
	store := &spyChangesetStore{Changesets: []Changeset{*cs, *newer}}

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: &spyWriter{}, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	assert.NoError(t, err, "a rolled-back newer changeset is not a conflict")
}

func TestRevertChangeset_WriterErrorAbortsBeforeRecording(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:new")},
		map[string]any{"glossary_terms": []any{}},
	)
	store := seededStore(cs)
	writer := &spyWriter{FailAtCall: 1}

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: writer, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.Error(t, err)
	assert.False(t, store.Changesets[0].RolledBack, "must not record a rollback that failed mid-write")
}

func TestRevertChangeset_ConflictListError(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("add_tag", "", "pii")},
		map[string]any{"tags": []any{}},
	)
	store := &spyChangesetStore{Changesets: []Changeset{*cs}, ListErr: errors.New("db down")}

	_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: &spyWriter{}, Changesets: store, Insights: &fullSpyStore{}}, cs, "admin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicting changesets")
}

// --- Parse helpers ---

func TestParseRecordedChanges(t *testing.T) {
	nv := map[string]any{
		"change_0": changeEntry("add_tag", "", "pii"),
		"change_1": changeEntry("add_glossary_term", "", "urn:li:glossaryTerm:x"),
	}
	got := parseRecordedChanges(nv)
	require.Len(t, got, 2)
	assert.Equal(t, "add_tag", got[0].ChangeType)
	assert.Equal(t, "urn:li:glossaryTerm:x", got[1].Detail)
}

func TestParsePriorState(t *testing.T) {
	prev := map[string]any{
		"description":    "d",
		"tags":           []any{"urn:li:tag:a"},
		"glossary_terms": []string{"urn:li:glossaryTerm:b"},
	}
	ps := parsePriorState(prev)
	assert.Equal(t, "d", ps.Description)
	assert.True(t, ps.Tags["urn:li:tag:a"])
	assert.True(t, ps.GlossaryTerms["urn:li:glossaryTerm:b"])
}

func TestStringSetField_AbsentOrWrongType(t *testing.T) {
	assert.Empty(t, stringSetField(map[string]any{}, "tags"))
	assert.Empty(t, stringSetField(map[string]any{"tags": 42}, "tags"))
}

func TestAspectFamily(t *testing.T) {
	cases := map[string]struct {
		change recordedChange
		want   string
	}{
		"entity description": {recordedChange{ChangeType: "update_description", Target: ""}, "description"},
		"column description": {recordedChange{ChangeType: "update_description", Target: "column:amount"}, "column_description:amount"},
		"add tag":            {recordedChange{ChangeType: "add_tag"}, "tags"},
		"remove tag":         {recordedChange{ChangeType: "remove_tag"}, "tags"},
		"quality flag":       {recordedChange{ChangeType: "flag_quality_issue"}, "tags"},
		"glossary":           {recordedChange{ChangeType: "add_glossary_term"}, "glossary_terms"},
		"documentation":      {recordedChange{ChangeType: "add_documentation"}, "documentation"},
		"other":              {recordedChange{ChangeType: "raise_incident"}, "raise_incident"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, aspectFamily(tc.change))
		})
	}
}

// --- MarkRolledBack store impls ---

func TestPostgresStore_MarkRolledBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs(StatusRolledBack, "admin", sqlmock.AnyArg(), "ins-1", StatusApplied).
		WillReturnResult(sqlmock.NewResult(0, 1))

	assert.NoError(t, store.MarkRolledBack(context.Background(), "ins-1", "admin"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_MarkRolledBack_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	mock.ExpectExec("UPDATE knowledge_insights").
		WillReturnError(errors.New("boom"))

	assert.Error(t, store.MarkRolledBack(context.Background(), "ins-1", "admin"))
}

func TestNoopStore_MarkRolledBack(t *testing.T) {
	assert.NoError(t, NewNoopStore().MarkRolledBack(context.Background(), "x", "admin"))
}
