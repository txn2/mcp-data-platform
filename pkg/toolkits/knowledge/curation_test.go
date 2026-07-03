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

func TestApply_CustomProperties_SetBatched(t *testing.T) {
	// Multiple set_custom_property changes on one entity must collapse into a SINGLE
	// SetCustomProperties read-modify-write, not one per key (else eventual-consistency
	// clobbers all but the last, #721/#729).
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes: []ApplyChange{
			{ChangeType: "set_custom_property", Target: "source_system", Detail: "warehouse"},
			{ChangeType: "set_custom_property", Target: "tier", Detail: "gold"},
		},
	})
	require.Nil(t, err)
	require.False(t, result.IsError, parseJSONResult(t, result))
	assert.Equal(t, []string{testEntityURN}, writerCallURNs(writer, "SetCustomProperties"),
		"two sets must be one batched write, not two")
}

func TestApply_CustomProperties_RemoveBatched(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes: []ApplyChange{
			{ChangeType: "remove_custom_property", Target: "legacy_owner"},
			{ChangeType: "remove_custom_property", Target: "old_tier"},
		},
	})
	require.Nil(t, err)
	require.False(t, result.IsError, parseJSONResult(t, result))
	assert.Equal(t, []string{testEntityURN}, writerCallURNs(writer, "RemoveCustomProperties"),
		"two removes must be one batched write, not two")
}

func TestApply_DataProductDescPlusCustomProperty_Rejected(t *testing.T) {
	// A dataProduct stores description and customProperties in one aspect, so an
	// entity-level update_description alongside a custom-property change would clobber.
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: "urn:li:dataProduct:analytics",
		Changes: []ApplyChange{
			{ChangeType: "update_description", Detail: "new description"},
			{ChangeType: "set_custom_property", Target: "tier", Detail: "gold"},
		},
	})
	assert.True(t, res.IsError)
	assert.Contains(t, parseJSONResult(t, res)["error"], "dataProduct")
	// Nothing was written.
	assert.Empty(t, writerCallURNs(writer, "SetCustomProperties"))
	assert.Empty(t, writerCallURNs(writer, "UpdateDescription"))
}

func TestApply_DatasetDescPlusCustomProperty_Allowed(t *testing.T) {
	// On a dataset the description (editableDatasetProperties) and customProperties
	// (datasetProperties) are distinct aspects, so co-applying is safe.
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)

	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN,
		Changes: []ApplyChange{
			{ChangeType: "update_description", Detail: "new description"},
			{ChangeType: "set_custom_property", Target: "tier", Detail: "gold"},
		},
	})
	require.Nil(t, err)
	require.False(t, res.IsError, parseJSONResult(t, res))
	assert.Equal(t, []string{testEntityURN}, writerCallURNs(writer, "SetCustomProperties"))
	assert.Contains(t, writerCallURNs(writer, "UpdateDescription"), testEntityURN)
}

func TestApply_MixedCustomProps_RejectedBeforeConfirmation(t *testing.T) {
	// The unsafe-combination rejection must fire before the confirmation round-trip.
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, writer)

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN, // no Confirm
		Changes: []ApplyChange{
			{ChangeType: "set_custom_property", Target: "a", Detail: "1"},
			{ChangeType: "remove_custom_property", Target: "b"},
		},
	})
	require.True(t, res.IsError, "must reject up front, not return confirmation_required")
	m := parseJSONResult(t, res)
	assert.Nil(t, m["confirmation_required"])
	assert.Contains(t, m["error"], "set and remove custom properties")
}

func TestApply_CustomProperties_WriteError(t *testing.T) {
	// A failed batched custom-property write must surface as an error.
	writer := &spyWriter{Metadata: &EntityMetadata{}, FailAtCall: 1}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)

	setRes, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN,
		Changes: []ApplyChange{{ChangeType: "set_custom_property", Target: "k", Detail: "v"}},
	})
	assert.True(t, setRes.IsError)
	assert.Contains(t, parseJSONResult(t, setRes)["error"], "custom properties")

	writer2 := &spyWriter{Metadata: &EntityMetadata{}, FailAtCall: 1}
	tk2 := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer2)
	rmRes, _, _ := tk2.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN,
		Changes: []ApplyChange{{ChangeType: "remove_custom_property", Target: "k"}},
	})
	assert.True(t, rmRes.IsError)
	assert.Contains(t, parseJSONResult(t, rmRes)["error"], "custom properties")
}

func TestApply_CustomProperties_MixedSetRemoveRejected(t *testing.T) {
	// set + remove on one entity share the customProperties aspect and cannot be
	// applied atomically upstream, so the apply is rejected before any write.
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)

	result, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes: []ApplyChange{
			{ChangeType: "set_custom_property", Target: "tier", Detail: "gold"},
			{ChangeType: "remove_custom_property", Target: "legacy_owner"},
		},
	})
	assert.True(t, result.IsError)
	assert.Contains(t, parseJSONResult(t, result)["error"], "set and remove custom properties")
	// Nothing was written.
	assert.Empty(t, writerCallURNs(writer, "SetCustomProperties"))
	assert.Empty(t, writerCallURNs(writer, "RemoveCustomProperties"))
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

func TestBulkUntag_ConfirmationTruncated(t *testing.T) {
	// Confirmation preview over the cap must flag truncation and must NOT claim any
	// entity was processed yet (nothing has run).
	tables := make([]semantic.TableSearchResult, bulkUntagMaxEntities+1)
	for i := range tables {
		tables[i] = semantic.TableSearchResult{URN: fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c%d,PROD)", i)}
	}
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, writer)
	tk.semanticProvider = &stubSemanticProvider{NoopProvider: semantic.NewNoopProvider(), tables: tables}

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated",
	})
	m := parseJSONResult(t, res)
	assert.Equal(t, true, m["confirmation_required"])
	assert.Equal(t, true, m["truncated"])
	assert.Equal(t, float64(bulkUntagMaxEntities), m["entities_found"])
	assert.NotContains(t, m[fieldMessage], "were processed", "confirmation must not claim work already ran")
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
	// More than the cap (cap+1 returned by the +1 probe) must flag truncation,
	// process exactly the cap, and echo only a bounded URN sample.
	tables := make([]semantic.TableSearchResult, bulkUntagMaxEntities+1)
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
	assert.Equal(t, float64(bulkUntagMaxEntities), m["entities_untagged"], "processes exactly the cap")
	assert.Len(t, m["affected_urns_sample"], bulkUntagURNSample, "response must echo only a bounded sample")
}

func TestBulkUntag_ExactlyCapNotTruncated(t *testing.T) {
	// Exactly the cap must NOT be flagged truncated (the +1 probe distinguishes it).
	tables := make([]semantic.TableSearchResult, bulkUntagMaxEntities)
	for i := range tables {
		tables[i] = semantic.TableSearchResult{URN: fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,a.b.e%d,PROD)", i)}
	}
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)
	tk.semanticProvider = &stubSemanticProvider{NoopProvider: semantic.NewNoopProvider(), tables: tables}

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated", Confirm: true,
	})
	m := parseJSONResult(t, res)
	assert.Nil(t, m["truncated"], "exactly the cap is not truncated")
	assert.Equal(t, float64(bulkUntagMaxEntities), m["entities_untagged"])
}

func TestBulkUntag_PartialFailureSignalled(t *testing.T) {
	// When some removals fail (below the cap), the response must report the failure
	// count and not present the operation as fully complete.
	writer := &spyWriter{Metadata: &EntityMetadata{}, FailAtCall: 2} // 2nd write onward fails
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, writer)
	tk.semanticProvider = &stubSemanticProvider{
		NoopProvider: semantic.NewNoopProvider(),
		tables: []semantic.TableSearchResult{
			{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"},
			{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.d,PROD)"},
			{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.e,PROD)"},
		},
	}

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated", Confirm: true,
	})
	require.False(t, res.IsError)
	m := parseJSONResult(t, res)
	// At least one succeeded and at least one failed; failed must be surfaced.
	require.NotNil(t, m["failed"], "partial failures must be reported")
	assert.Positive(t, m["failed"])
	assert.Contains(t, m[fieldMessage], "still carry the tag")
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
