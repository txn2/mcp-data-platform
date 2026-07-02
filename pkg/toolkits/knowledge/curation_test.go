package knowledge

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// stubSemanticProvider returns canned SearchTables results for bulk_untag tests.
type stubSemanticProvider struct {
	*semantic.NoopProvider
	tables []semantic.TableSearchResult
	err    error
}

func (s *stubSemanticProvider) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return s.tables, s.err
}

// writerCallURNs returns the URNs the writer was called with for the given method.
func writerCallURNs(w *spyWriter, method string) []string {
	var out []string
	for _, c := range w.WriteCalls {
		if c.Method == method {
			out = append(out, c.URN)
		}
	}
	return out
}

const tagURN = "urn:li:tag:Deprecated"

// --- change_type: delete_tag (#726) ---

func TestApply_DeleteTag(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, writer)

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action:    "apply",
		EntityURN: tagURN,
		Changes:   []ApplyChange{{ChangeType: "delete_tag"}},
	})
	require.Nil(t, err)
	require.False(t, result.IsError, parseJSONResult(t, result))
	assert.Equal(t, []string{tagURN}, writerCallURNs(writer, "DeleteTag"))
	require.Len(t, csStore.Changesets, 1)
	assert.Equal(t, "delete_tag", csStore.Changesets[0].ChangeType)
}

// --- change_type: set/remove custom property (#726) ---

func TestApply_CustomProperties(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes: []ApplyChange{
			{ChangeType: "set_custom_property", Target: "source_system", Detail: "warehouse"},
			{ChangeType: "remove_custom_property", Target: "legacy_owner"},
		},
	})
	require.Nil(t, err)
	require.False(t, result.IsError, parseJSONResult(t, result))
	assert.Equal(t, []string{testEntityURN}, writerCallURNs(writer, "SetCustomProperties"))
	assert.Equal(t, []string{testEntityURN}, writerCallURNs(writer, "RemoveCustomProperties"))
}

// --- change_type: edit a tag's own description via update_description on a tag URN (#726) ---

func TestApply_TagDescriptionEdit(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{Description: "Old, wrong definition"}}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action:    "apply",
		EntityURN: tagURN,
		Changes:   []ApplyChange{{ChangeType: "update_description", Detail: "The corrected tag definition."}},
	})
	require.Nil(t, err)
	require.False(t, result.IsError, parseJSONResult(t, result))
	assert.Contains(t, writerCallURNs(writer, "UpdateDescription"), tagURN)
}

// --- validation ---

func TestApply_CustomProperty_Validation(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{Metadata: &EntityMetadata{}})

	// set_custom_property requires both target (key) and detail (value).
	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN,
		Changes: []ApplyChange{{ChangeType: "set_custom_property", Target: "k"}},
	})
	assert.True(t, res.IsError, "set_custom_property without detail should fail")

	// remove_custom_property requires target (key).
	res, _, _ = tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN,
		Changes: []ApplyChange{{ChangeType: "remove_custom_property"}},
	})
	assert.True(t, res.IsError, "remove_custom_property without target should fail")
}

// --- rollback contract: the new curation change types are not auto-revertible ---

func TestCurationChangeTypes_NotRevertible(t *testing.T) {
	for _, ct := range []string{"delete_tag", "set_custom_property", "remove_custom_property"} {
		assert.False(t, revertibleChangeTypes[ct],
			"%s must not be auto-revertible (irreversible or no before-image, #726)", ct)
		assert.False(t, isRevertible(recordedChange{ChangeType: ct}, "dataset"),
			"%s isRevertible must be false", ct)
	}
}

// TestBulkUntagChangeset_IsUnrevertible guards the rollback contract: a bulk_untag
// changeset must record a change_N entry so the rollback path parses it and refuses
// the revert. If the recording regresses to a bare {tag_urn, affected_urns} map,
// parseRecordedChanges returns nothing and rollback would silently no-op (#726 review).
func TestBulkUntagChangeset_IsUnrevertible(t *testing.T) {
	newValue := changesToMap([]ApplyChange{{ChangeType: string(actionBulkUntag), Target: tagURN}})
	newValue["tag_urn"] = tagURN
	newValue["affected_urns"] = []string{"urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"}

	changes := parseRecordedChanges(newValue)
	require.NotEmpty(t, changes, "bulk_untag must record a change_N entry so rollback can see it")
	assert.Contains(t, unrevertibleChangeTypes(changes, entityTypeTag), string(actionBulkUntag))
}

// TestBulkUntag_RecordsRevertibleShape asserts the live handler records a changeset
// whose NewValue rollback can parse (end-to-end guard for the same contract).
func TestBulkUntag_RecordsRevertibleShape(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, writer)
	tk.semanticProvider = &stubSemanticProvider{
		NoopProvider: semantic.NewNoopProvider(),
		tables:       []semantic.TableSearchResult{{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"}},
	}

	_, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated", Confirm: true,
	})
	require.Nil(t, err)
	require.Len(t, csStore.Changesets, 1)
	changes := parseRecordedChanges(csStore.Changesets[0].NewValue)
	require.NotEmpty(t, changes)
	assert.Contains(t, unrevertibleChangeTypes(changes, entityTypeTag), string(actionBulkUntag))
}

// --- action: bulk_untag (#726) ---

func TestBulkUntag(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, writer)
	tk.semanticProvider = &stubSemanticProvider{
		NoopProvider: semantic.NewNoopProvider(),
		tables: []semantic.TableSearchResult{
			{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"},
			{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.d,PROD)"},
		},
	}

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated", Confirm: true,
	})
	require.Nil(t, err)
	require.False(t, result.IsError, parseJSONResult(t, result))
	m := parseJSONResult(t, result)
	assert.Equal(t, float64(2), m["entities_untagged"])
	assert.Equal(t, tagURN, m["tag_urn"])
	// The tag was removed from each entity, and one changeset recorded.
	assert.Len(t, writerCallURNs(writer, "ApplyTagChanges"), 2)
	require.Len(t, csStore.Changesets, 1)
	assert.Equal(t, "bulk_untag", csStore.Changesets[0].ChangeType)
}

func TestBulkUntag_RequiresConfirmation(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, writer)
	tk.semanticProvider = &stubSemanticProvider{
		NoopProvider: semantic.NewNoopProvider(),
		tables:       []semantic.TableSearchResult{{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"}},
	}

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated",
	})
	require.Nil(t, callErr)
	m := parseJSONResult(t, result)
	assert.Equal(t, true, m["confirmation_required"])
	assert.Equal(t, float64(1), m["entities_found"])
	// Nothing was removed without confirmation.
	assert.Empty(t, writerCallURNs(writer, "ApplyTagChanges"))
}

func TestBulkUntag_Errors(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{Metadata: &EntityMetadata{}})

	// Missing tag_urn.
	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag"})
	assert.True(t, res.IsError)

	// No semantic provider configured.
	res, _, _ = tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag", TagURN: "X", Confirm: true})
	assert.True(t, res.IsError)

	// Provider configured but no entities carry the tag: a clean zero, not an error.
	tk.semanticProvider = &stubSemanticProvider{NoopProvider: semantic.NewNoopProvider()}
	res, _, _ = tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag", TagURN: "X", Confirm: true})
	require.False(t, res.IsError)
	assert.Equal(t, float64(0), parseJSONResult(t, res)["entities_untagged"])

	// Search itself failing is surfaced as an error.
	tk.semanticProvider = &stubSemanticProvider{NoopProvider: semantic.NewNoopProvider(), err: assert.AnError}
	res, _, _ = tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag", TagURN: "X", Confirm: true})
	assert.True(t, res.IsError)
}

func TestBulkUntag_ChangesetInsertFailureIsError(t *testing.T) {
	// The untag succeeds but the audit changeset fails to persist: the caller must
	// be told, not handed a changeset_id that does not resolve (#726 review).
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	csStore := &spyChangesetStore{InsertErr: assert.AnError}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, writer)
	tk.semanticProvider = &stubSemanticProvider{
		NoopProvider: semantic.NewNoopProvider(),
		tables:       []semantic.TableSearchResult{{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"}},
	}

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated", Confirm: true,
	})
	assert.True(t, res.IsError, "a failed audit record must surface as an error")
	assert.Contains(t, parseJSONResult(t, res)["error"], "audit")
}

func TestBulkUntag_TruncationSignalled(t *testing.T) {
	// A full page (>= cap) must flag truncation and echo only a bounded URN sample.
	tables := make([]semantic.TableSearchResult, bulkUntagMaxEntities)
	for i := range tables {
		tables[i] = semantic.TableSearchResult{URN: fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,a.b.t%d,PROD)", i)}
	}
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)
	tk.semanticProvider = &stubSemanticProvider{NoopProvider: semantic.NewNoopProvider(), tables: tables}

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated", Confirm: true,
	})
	m := parseJSONResult(t, res)
	assert.Equal(t, true, m["truncated"])
	assert.Len(t, m["affected_urns_sample"], bulkUntagURNSample, "response must echo only a bounded sample")
}

func TestBulkUntag_AllRemovalsFail(t *testing.T) {
	// Writer fails on the first (and only) untag call: nothing succeeds, so the
	// whole action reports an error rather than a false success.
	writer := &spyWriter{Metadata: &EntityMetadata{}, FailAtCall: 1}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)
	tk.semanticProvider = &stubSemanticProvider{
		NoopProvider: semantic.NewNoopProvider(),
		tables:       []semantic.TableSearchResult{{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"}},
	}

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag", TagURN: "X", Confirm: true})
	assert.True(t, res.IsError)
}
