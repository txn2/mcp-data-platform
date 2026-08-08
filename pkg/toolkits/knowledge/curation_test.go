package knowledge

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// stubSemanticProvider models the catalog contract bulk_untag depends on: a page
// bounded by the requested limit AND by the backend's own clamp, plus the total
// match count that is the only thing able to report what the page left behind.
type stubSemanticProvider struct {
	*semantic.NoopProvider
	tables []semantic.TableSearchResult
	err    error
	// clampAt caps the page however many rows were requested, as the real DataHub
	// client does at its MaxLimit. Zero means no clamp.
	clampAt int
}

func (s *stubSemanticProvider) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	results, _, err := s.SearchTablesCounted(ctx, filter)
	return results, err
}

func (s *stubSemanticProvider) SearchTablesCounted(
	_ context.Context, filter semantic.SearchFilter,
) ([]semantic.TableSearchResult, int, error) {
	if s.err != nil {
		return nil, semantic.TotalUnknown, s.err
	}
	page := s.tables
	if s.clampAt > 0 && s.clampAt < len(page) {
		page = page[:s.clampAt]
	}
	if filter.Limit > 0 && filter.Limit < len(page) {
		page = page[:filter.Limit]
	}
	return page, len(s.tables), nil
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

// --- apply-time rollback advertisement honesty (#922) ---

// TestChangesetRevertibility_And_Message covers the contract that fixes the #922
// contradiction: the apply/list_changesets `revertible` field and the success message
// must both derive from the same all-or-nothing gate rollback enforces, so a changeset
// is never advertised as rollback-able and then refused. Rollback reverts nothing if
// ANY change lacks a before-image, so a mixed changeset is unrevertible, not partial.
// Table-driven over all-revertible, column-description, getter-less entity type, page
// target, and mixed.
func TestChangesetRevertibility_And_Message(t *testing.T) {
	const domainURN = "urn:li:domain:marketing"
	tests := []struct {
		name           string
		targetURN      string
		changes        []recordedChange
		wantRevertible bool
		wantBlocking   []string
		msgHasRollback bool // message instructs action=rollback
		msgHasRefusal  bool // message states it cannot be rolled back
	}{
		{
			name:           "all revertible",
			targetURN:      testEntityURN,
			changes:        []recordedChange{{ChangeType: "add_glossary_term", Detail: "urn:li:glossaryTerm:x"}},
			wantRevertible: true,
			msgHasRollback: true,
		},
		{
			name:           "column description only",
			targetURN:      testEntityURN,
			changes:        []recordedChange{{ChangeType: "update_description", Target: "column:total_amount", Detail: "d"}},
			wantRevertible: false,
			wantBlocking:   []string{"update_description"},
			msgHasRefusal:  true,
		},
		{
			name:           "getter-less entity type",
			targetURN:      domainURN,
			changes:        []recordedChange{{ChangeType: "update_description", Detail: "d"}},
			wantRevertible: false,
			wantBlocking:   []string{"update_description"},
			msgHasRefusal:  true,
		},
		{
			// A knowledge-page promotion reverts through the page sink, not the DataHub
			// inverse path, so it is always structurally revertible here regardless of
			// its recorded change types.
			name:           "page target",
			targetURN:      "kp:sales-glossary",
			changes:        []recordedChange{{ChangeType: "update_description", Target: "column:total_amount", Detail: "d"}},
			wantRevertible: true,
			msgHasRollback: true,
		},
		{
			// Mixed is NOT partial: rollback is all-or-nothing, so one unrevertible
			// change makes the whole changeset unrevertible. The message must not promise
			// to revert the glossary term the rollback would refuse to touch.
			name:      "mixed is unrevertible",
			targetURN: testEntityURN,
			changes: []recordedChange{
				{ChangeType: "add_glossary_term", Detail: "urn:li:glossaryTerm:x"},
				{ChangeType: "update_description", Target: "column:total_amount", Detail: "d"},
			},
			wantRevertible: false,
			wantBlocking:   []string{"update_description"},
			msgHasRefusal:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			revertible, blocking := changesetRevertibility(tt.targetURN, tt.changes)
			assert.Equal(t, tt.wantRevertible, revertible)
			assert.Equal(t, tt.wantBlocking, blocking)

			msg := applyResultMessage("cs-1", revertible, blocking)
			assert.Equal(t, tt.msgHasRollback, strings.Contains(msg, "action=rollback changeset_id=cs-1"),
				"rollback-instruction presence mismatch: %q", msg)
			assert.Equal(t, tt.msgHasRefusal, strings.Contains(msg, "cannot be rolled back automatically"),
				"refusal-statement presence mismatch: %q", msg)
			for _, ct := range tt.wantBlocking {
				assert.Contains(t, msg, ct, "message must name the blocking change type")
			}
		})
	}
}

// TestApply_ColumnDescription_DoesNotAdvertiseRollback is the #922 acceptance test:
// an apply whose only change is a column-level update_description returns a message
// that does NOT instruct rollback, carries revertible:false, and names the offending
// change type — through the real handler, not just the classifier in isolation.
func TestApply_ColumnDescription_DoesNotAdvertiseRollback(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, writer)

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes:   []ApplyChange{{ChangeType: "update_description", Target: "column:total_amount", Detail: "the order total"}},
	})
	require.Nil(t, err)
	require.False(t, result.IsError, parseJSONResult(t, result))

	out := parseJSONResult(t, result)
	assert.Equal(t, false, out["revertible"])
	msg, _ := out[fieldMessage].(string)
	assert.NotContains(t, msg, "action=rollback", "must not instruct rollback for an unrevertible changeset")
	assert.Contains(t, msg, "cannot be rolled back automatically")
	unrev, _ := out["unrevertible_change_types"].([]any)
	require.Len(t, unrev, 1)
	assert.Equal(t, "update_description", unrev[0])
}

// TestApply_AllRevertible_AdvertisesRollback keeps the happy path honest: an
// all-revertible apply still advertises rollback and carries revertible:true, and
// list_changesets surfaces the same true so a caller can pick a rollback target
// without discovering the refusal after the fact.
func TestApply_AllRevertible_AdvertisesRollback(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{GlossaryTerms: []string{}, Tags: []string{}, Owners: []string{}}}
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, writer)

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes:   []ApplyChange{{ChangeType: "add_glossary_term", Detail: "urn:li:glossaryTerm:x"}},
	})
	require.Nil(t, err)
	require.False(t, result.IsError, parseJSONResult(t, result))

	out := parseJSONResult(t, result)
	assert.Equal(t, true, out["revertible"])
	assert.NotContains(t, out, "unrevertible_change_types")
	msg, _ := out[fieldMessage].(string)
	assert.Contains(t, msg, "action=rollback")

	listRes, _, err := tk.handleListChangesets(context.Background(), applyKnowledgeInput{
		Action: "list_changesets", EntityURN: testEntityURN,
	})
	require.Nil(t, err)
	listOut := parseJSONResult(t, listRes)
	list, ok := listOut["changesets"].([]any)
	require.True(t, ok)
	require.Len(t, list, 1)
	entry, _ := list[0].(map[string]any)
	assert.Equal(t, true, entry["revertible"])
}

// TestApply_MixedChangeset_AdvertisesUnrevertible_ThenRollbackRefuses is the end-to-end
// proof that the advertisement matches rollback's actual behavior (#922): a changeset
// mixing a revertible and an unrevertible change must be advertised revertible:false
// (rollback is all-or-nothing) AND the subsequent rollback must refuse — never the
// advertise-then-refuse contradiction the review caught in the first cut.
func TestApply_MixedChangeset_AdvertisesUnrevertible_ThenRollbackRefuses(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{GlossaryTerms: []string{}, Tags: []string{}, Owners: []string{}}}
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, writer)
	ctx := context.Background()

	result, _, err := tk.handleApplyKnowledge(ctx, nil, applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes: []ApplyChange{
			{ChangeType: "add_glossary_term", Detail: "urn:li:glossaryTerm:x"},
			{ChangeType: "update_description", Target: "column:total_amount", Detail: "the order total"},
		},
	})
	require.Nil(t, err)
	require.False(t, result.IsError, parseJSONResult(t, result))

	out := parseJSONResult(t, result)
	assert.Equal(t, false, out["revertible"], "a mixed changeset is not partially revertible; rollback is all-or-nothing")
	msg, _ := out[fieldMessage].(string)
	assert.NotContains(t, msg, "action=rollback", "must not instruct a rollback rollback would refuse")
	csID, _ := out["changeset_id"].(string)
	require.NotEmpty(t, csID)

	// The advertised refusal must actually happen: rolling back is refused, reverting
	// nothing (not even the revertible glossary term).
	rbRes, _, err := tk.handleRollback(ctx, applyKnowledgeInput{
		Action: "rollback", ChangesetID: csID, Confirm: true,
	})
	require.Nil(t, err)
	require.True(t, rbRes.IsError, "rollback of a mixed changeset must be refused")
	rbText, _ := rbRes.Content[0].(*mcp.TextContent)
	require.NotNil(t, rbText)
	assert.Contains(t, rbText.Text, "cannot be rolled back automatically")
	require.Len(t, csStore.Changesets, 1)
	assert.False(t, csStore.Changesets[0].RolledBack, "a refused rollback must not mark the changeset rolled back")
}

// TestListChangesets_AlreadyRolledBack_NotRevertible covers #922 review finding: an
// already-rolled-back changeset, though structurally revertible, must report
// revertible:false so a caller filtering on that field alone never picks it (the
// rollback would refuse with "already rolled back").
func TestListChangesets_AlreadyRolledBack_NotRevertible(t *testing.T) {
	csStore := &spyChangesetStore{Changesets: []Changeset{{
		ID:         "cs-rolled",
		TargetURN:  testEntityURN,
		ChangeType: "add_glossary_term",
		NewValue:   changesToMap([]ApplyChange{{ChangeType: "add_glossary_term", Detail: "urn:li:glossaryTerm:x"}}),
		RolledBack: true,
	}}}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, &spyWriter{Metadata: &EntityMetadata{}})

	listRes, _, err := tk.handleListChangesets(context.Background(), applyKnowledgeInput{
		Action: "list_changesets", EntityURN: testEntityURN,
	})
	require.Nil(t, err)
	list, _ := parseJSONResult(t, listRes)["changesets"].([]any)
	require.Len(t, list, 1)
	entry, _ := list[0].(map[string]any)
	assert.Equal(t, false, entry["revertible"], "an already-rolled-back changeset is not revertible")
	assert.Equal(t, true, entry["rolled_back"])
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

// TestBulkUntag_ConfirmationTruncated is the #1238 regression on the destructive
// path: the catalog clamps the page to 100 rows however many were requested, so
// the run sees a short page while 250 entities carry the tag. Reading truncation
// off the page length reports a clean sweep and leaves 150 entities tagged; the
// match count is what makes the follow-up run visible.
func TestBulkUntag_ConfirmationTruncated(t *testing.T) {
	const (
		clampAt = 100
		matches = 250
	)
	tables := make([]semantic.TableSearchResult, matches)
	for i := range tables {
		tables[i] = semantic.TableSearchResult{URN: fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c%d,PROD)", i)}
	}
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, writer)
	tk.semanticProvider = &stubSemanticProvider{NoopProvider: semantic.NewNoopProvider(), tables: tables, clampAt: clampAt}

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated",
	})
	m := parseJSONResult(t, res)
	assert.Equal(t, true, m["confirmation_required"])
	assert.Equal(t, true, m["truncated"])
	assert.Equal(t, float64(clampAt), m["entities_found"])
	assert.NotContains(t, m[fieldMessage], "were processed", "confirmation must not claim work already ran")
	assert.Empty(t, writerCallURNs(writer, "ApplyTagChanges"))
}

// TestBulkUntag_CapBoundsTheFanOut pins the write cap against a catalog that
// ignores the requested limit: the run must still process at most the cap, and
// must still say more entities carry the tag.
func TestBulkUntag_CapBoundsTheFanOut(t *testing.T) {
	tables := make([]semantic.TableSearchResult, bulkUntagMaxEntities+7)
	for i := range tables {
		tables[i] = semantic.TableSearchResult{URN: fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c%d,PROD)", i)}
	}
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, writer)
	// clampAt 0 and no limit handling: the stub returns every row it holds.
	tk.semanticProvider = &ignoresLimitProvider{tables: tables}

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated",
	})
	m := parseJSONResult(t, res)
	assert.Equal(t, float64(bulkUntagMaxEntities), m["entities_found"])
	assert.Equal(t, true, m["truncated"])
}

// TestBulkUntag_UnverifiedSweepIsUnfinished pins the fail-safe: a catalog that
// cannot report how many entities carry the tag leaves the sweep unverified, and
// an unverified destructive sweep is reported as unfinished so the caller re-runs
// rather than recording a tag as cleared on the strength of a page length.
func TestBulkUntag_UnverifiedSweepIsUnfinished(t *testing.T) {
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, writer)
	tk.semanticProvider = &uncountedProvider{tables: []semantic.TableSearchResult{
		{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"},
	}}

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated",
	})
	m := parseJSONResult(t, res)
	assert.Equal(t, true, m["truncated"])
	assert.Equal(t, float64(1), m["entities_found"])
}

// uncountedProvider answers the search but cannot report a match count.
type uncountedProvider struct {
	*semantic.NoopProvider
	tables []semantic.TableSearchResult
}

func (p *uncountedProvider) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return p.tables, nil
}

// ignoresLimitProvider returns every row it holds whatever page was requested,
// which is the contract violation the cap must survive.
type ignoresLimitProvider struct {
	*semantic.NoopProvider
	tables []semantic.TableSearchResult
}

func (p *ignoresLimitProvider) SearchTablesCounted(
	_ context.Context, _ semantic.SearchFilter,
) ([]semantic.TableSearchResult, int, error) {
	return p.tables, len(p.tables), nil
}

func (p *ignoresLimitProvider) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	results, _, err := p.SearchTablesCounted(ctx, filter)
	return results, err
}

// TestBulkUntag_CompleteSweepIsNotTruncated is the other side: when the count says
// the page held every carrier, the response must not send the caller back for a
// follow-up run that would find nothing.
func TestBulkUntag_CompleteSweepIsNotTruncated(t *testing.T) {
	tables := make([]semantic.TableSearchResult, 3)
	for i := range tables {
		tables[i] = semantic.TableSearchResult{URN: fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c%d,PROD)", i)}
	}
	writer := &spyWriter{Metadata: &EntityMetadata{}}
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, writer)
	tk.semanticProvider = &stubSemanticProvider{NoopProvider: semantic.NewNoopProvider(), tables: tables, clampAt: 100}

	res, _, _ := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "bulk_untag", TagURN: "Deprecated",
	})
	m := parseJSONResult(t, res)
	assert.NotContains(t, m, "truncated")
	assert.Equal(t, float64(3), m["entities_found"])
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
