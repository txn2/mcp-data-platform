package knowledge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for types_test.go.
const (
	testMedium   = "medium"
	testTarget   = "tgt"
	testDetail   = "d"
	testMustBeOf = "must be one of"
)

func TestValidateCategory(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid correction", input: "correction", wantErr: false},
		{name: "valid business_context", input: "business_context", wantErr: false},
		{name: "valid data_quality", input: "data_quality", wantErr: false},
		{name: "valid usage_guidance", input: "usage_guidance", wantErr: false},
		{name: "valid relationship", input: "relationship", wantErr: false},
		{name: "valid enhancement", input: "enhancement", wantErr: false},
		{name: "empty string", input: "", wantErr: true},
		{name: "invalid value", input: "unknown", wantErr: true},
		{name: "case sensitive", input: "Correction", wantErr: true},
		{name: "extra spaces", input: " correction ", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCategory(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testMustBeOf)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateConfidence(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid high", input: "high", wantErr: false},
		{name: "valid medium", input: testMedium, wantErr: false},
		{name: "valid low", input: "low", wantErr: false},
		{name: "empty defaults to medium", input: "", wantErr: false},
		{name: "invalid value", input: "very_high", wantErr: true},
		{name: "case sensitive", input: "High", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfidence(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testMustBeOf)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNormalizeConfidence(t *testing.T) {
	assert.Equal(t, testMedium, NormalizeConfidence(""))
	assert.Equal(t, "high", NormalizeConfidence("high"))
	assert.Equal(t, "low", NormalizeConfidence("low"))
	assert.Equal(t, testMedium, NormalizeConfidence(testMedium))
}

func TestValidateSource(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid user", input: "user", wantErr: false},
		{name: "valid agent_discovery", input: "agent_discovery", wantErr: false},
		{name: "valid enrichment_gap", input: "enrichment_gap", wantErr: false},
		{name: "empty defaults to user", input: "", wantErr: false},
		{name: "invalid value", input: "system", wantErr: true},
		{name: "case sensitive", input: "User", wantErr: true},
		{name: "extra spaces", input: " user ", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSource(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testMustBeOf)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNormalizeSource(t *testing.T) {
	assert.Equal(t, "user", NormalizeSource(""))
	assert.Equal(t, "user", NormalizeSource("user"))
	assert.Equal(t, "agent_discovery", NormalizeSource("agent_discovery"))
	assert.Equal(t, "enrichment_gap", NormalizeSource("enrichment_gap"))
}

func TestValidateInsightText(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		errMsg  string
	}{
		{name: "valid text", input: "This is a valid insight text.", wantErr: false},
		{name: "minimum length", input: "1234567890", wantErr: false},
		{name: "empty string", input: "", wantErr: true, errMsg: "required"},
		{name: "too short", input: "short", wantErr: true, errMsg: "at least 10"},
		{name: "9 chars", input: "123456789", wantErr: true, errMsg: "at least 10"},
		{name: "max length", input: strings.Repeat("a", MaxInsightTextLen), wantErr: false},
		{name: "too long", input: strings.Repeat("a", MaxInsightTextLen+1), wantErr: true, errMsg: "at most 4000"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateInsightText(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				if tc.errMsg != "" {
					assert.Contains(t, err.Error(), tc.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSuggestedActions(t *testing.T) {
	validAction := SuggestedAction{ActionType: "update_description", Target: testTarget, Detail: testDetail}

	tests := []struct {
		name    string
		input   []SuggestedAction
		wantErr bool
		errMsg  string
	}{
		{name: "nil is valid", input: nil, wantErr: false},
		{name: "empty slice is valid", input: []SuggestedAction{}, wantErr: false},
		{name: "valid update_description", input: []SuggestedAction{{ActionType: "update_description", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid add_tag", input: []SuggestedAction{{ActionType: "add_tag", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid add_glossary_term", input: []SuggestedAction{{ActionType: "add_glossary_term", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid flag_quality_issue", input: []SuggestedAction{{ActionType: "flag_quality_issue", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid add_documentation", input: []SuggestedAction{{ActionType: "add_documentation", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid remove_tag", input: []SuggestedAction{{ActionType: "remove_tag", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid add_curated_query", input: []SuggestedAction{{ActionType: "add_curated_query", Detail: "q", QuerySQL: "SELECT 1"}}, wantErr: false},
		{name: "add_curated_query missing sql", input: []SuggestedAction{{ActionType: "add_curated_query", Detail: "q"}}, wantErr: true, errMsg: "query_sql is required"},
		{name: "invalid action_type", input: []SuggestedAction{{ActionType: "delete_entity", Target: testTarget, Detail: testDetail}}, wantErr: true, errMsg: "invalid action_type"},
		{name: "max actions", input: []SuggestedAction{validAction, validAction, validAction, validAction, validAction}, wantErr: false},
		{name: "exceeds max", input: []SuggestedAction{validAction, validAction, validAction, validAction, validAction, validAction}, wantErr: true, errMsg: "exceeds maximum of 5"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSuggestedActions(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				if tc.errMsg != "" {
					assert.Contains(t, err.Error(), tc.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEntityURNs(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		wantErr bool
	}{
		{name: "nil slice", input: nil, wantErr: false},
		{name: "empty slice", input: []string{}, wantErr: false},
		{name: "valid single", input: []string{"urn:li:dataset:foo"}, wantErr: false},
		{name: "max 10", input: make([]string, 10), wantErr: false},
		{name: "exceeds 10", input: make([]string, 11), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEntityURNs(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "exceeds maximum of 10")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRelatedColumns(t *testing.T) {
	tests := []struct {
		name    string
		input   []RelatedColumn
		wantErr bool
	}{
		{name: "nil slice", input: nil, wantErr: false},
		{name: "empty slice", input: []RelatedColumn{}, wantErr: false},
		{name: "valid single", input: []RelatedColumn{{URN: "u", Column: "c", Relevance: "r"}}, wantErr: false},
		{name: "max 20", input: make([]RelatedColumn, 20), wantErr: false},
		{name: "exceeds 20", input: make([]RelatedColumn, 21), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRelatedColumns(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "exceeds maximum of 20")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateStatusTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		// Valid transitions from pending
		{name: "pending to approved", from: StatusPending, to: StatusApproved, wantErr: false},
		{name: "pending to rejected", from: StatusPending, to: StatusRejected, wantErr: false},
		{name: "pending to superseded", from: StatusPending, to: StatusSuperseded, wantErr: false},

		// Valid transitions from approved
		{name: "approved to applied", from: StatusApproved, to: StatusApplied, wantErr: false},

		// Valid transitions from applied
		{name: "applied to rolled_back", from: StatusApplied, to: StatusRolledBack, wantErr: false},

		// Invalid transitions from pending
		{name: "pending to applied", from: StatusPending, to: StatusApplied, wantErr: true},
		{name: "pending to rolled_back", from: StatusPending, to: StatusRolledBack, wantErr: true},
		{name: "pending to pending", from: StatusPending, to: StatusPending, wantErr: true},

		// Invalid transitions from approved
		{name: "approved to pending", from: StatusApproved, to: StatusPending, wantErr: true},
		{name: "approved to rejected", from: StatusApproved, to: StatusRejected, wantErr: true},
		{name: "approved to approved", from: StatusApproved, to: StatusApproved, wantErr: true},
		{name: "approved to rolled_back", from: StatusApproved, to: StatusRolledBack, wantErr: true},
		{name: "approved to superseded", from: StatusApproved, to: StatusSuperseded, wantErr: true},

		// Invalid transitions from rejected (terminal state)
		{name: "rejected to pending", from: StatusRejected, to: StatusPending, wantErr: true},
		{name: "rejected to approved", from: StatusRejected, to: StatusApproved, wantErr: true},
		{name: "rejected to applied", from: StatusRejected, to: StatusApplied, wantErr: true},

		// Invalid transitions from applied
		{name: "applied to pending", from: StatusApplied, to: StatusPending, wantErr: true},
		{name: "applied to approved", from: StatusApplied, to: StatusApproved, wantErr: true},
		{name: "applied to applied", from: StatusApplied, to: StatusApplied, wantErr: true},

		// Invalid transitions from rolled_back (terminal state)
		{name: "rolled_back to pending", from: StatusRolledBack, to: StatusPending, wantErr: true},
		{name: "rolled_back to approved", from: StatusRolledBack, to: StatusApproved, wantErr: true},
		{name: "rolled_back to applied", from: StatusRolledBack, to: StatusApplied, wantErr: true},

		// Invalid transitions from superseded (terminal state)
		{name: "superseded to pending", from: StatusSuperseded, to: StatusPending, wantErr: true},
		{name: "superseded to approved", from: StatusSuperseded, to: StatusApproved, wantErr: true},

		// Unknown statuses
		{name: "unknown from status", from: "unknown", to: StatusApproved, wantErr: true},
		{name: "empty from status", from: "", to: StatusApproved, wantErr: true},
		{name: "empty to status", from: StatusPending, to: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStatusTransition(tc.from, tc.to)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid status transition")
				assert.Contains(t, err.Error(), tc.from)
				assert.Contains(t, err.Error(), tc.to)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInsightFilterEffectiveLimit(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		expected int
	}{
		{name: "zero uses default", limit: 0, expected: DefaultLimit},
		{name: "negative uses default", limit: -1, expected: DefaultLimit},
		{name: "one is valid", limit: 1, expected: 1},
		{name: "custom within range", limit: 50, expected: 50}, //nolint:revive // test value
		{name: "at default boundary", limit: DefaultLimit, expected: DefaultLimit},
		{name: "at max boundary", limit: MaxLimit, expected: MaxLimit},
		{name: "over max capped", limit: MaxLimit + 1, expected: MaxLimit},
		{name: "way over max capped", limit: 10000, expected: MaxLimit}, //nolint:revive // test value
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &InsightFilter{Limit: tc.limit}
			assert.Equal(t, tc.expected, f.EffectiveLimit())
		})
	}
}

func TestChangesetFilterEffectiveLimit(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		expected int
	}{
		{name: "zero uses default", limit: 0, expected: DefaultLimit},
		{name: "negative uses default", limit: -1, expected: DefaultLimit},
		{name: "one is valid", limit: 1, expected: 1},
		{name: "custom within range", limit: 50, expected: 50}, //nolint:revive // test value
		{name: "at default boundary", limit: DefaultLimit, expected: DefaultLimit},
		{name: "at max boundary", limit: MaxLimit, expected: MaxLimit},
		{name: "over max capped", limit: MaxLimit + 1, expected: MaxLimit},
		{name: "way over max capped", limit: 10000, expected: MaxLimit}, //nolint:revive // test value
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &ChangesetFilter{Limit: tc.limit}
			assert.Equal(t, tc.expected, f.EffectiveLimit())
		})
	}
}

func TestValidateApplyChanges(t *testing.T) {
	validChange := ApplyChange{ChangeType: "update_description", Target: testTarget, Detail: testDetail}

	tests := []struct {
		name    string
		input   []ApplyChange
		wantErr bool
		errMsg  string
	}{
		{name: "nil is invalid", input: nil, wantErr: true, errMsg: "required and must not be empty"},
		{name: "empty slice is invalid", input: []ApplyChange{}, wantErr: true, errMsg: "required and must not be empty"},
		{name: "valid update_description", input: []ApplyChange{{ChangeType: "update_description", Target: testTarget, Detail: testDetail}}, wantErr: false}, //nolint:revive // test value
		{name: "valid add_tag", input: []ApplyChange{{ChangeType: "add_tag", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid add_glossary_term", input: []ApplyChange{{ChangeType: "add_glossary_term", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid flag_quality_issue", input: []ApplyChange{{ChangeType: "flag_quality_issue", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid add_documentation", input: []ApplyChange{{ChangeType: "add_documentation", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid remove_tag", input: []ApplyChange{{ChangeType: "remove_tag", Target: testTarget, Detail: testDetail}}, wantErr: false},
		{name: "valid add_curated_query", input: []ApplyChange{{ChangeType: "add_curated_query", Detail: "my query", QuerySQL: "SELECT 1"}}, wantErr: false},
		{name: "add_curated_query missing sql", input: []ApplyChange{{ChangeType: "add_curated_query", Detail: "my query"}}, wantErr: true, errMsg: "query_sql is required"},
		{name: "invalid change_type", input: []ApplyChange{{ChangeType: "delete_entity", Target: testTarget, Detail: testDetail}}, wantErr: true, errMsg: "invalid change_type"},
		{name: "empty change_type", input: []ApplyChange{{ChangeType: "", Target: testTarget, Detail: testDetail}}, wantErr: true, errMsg: "invalid change_type"},
		{name: "multiple valid changes", input: []ApplyChange{validChange, {ChangeType: "add_tag", Target: "t2", Detail: "d2"}}, wantErr: false},
		{name: "second change invalid", input: []ApplyChange{validChange, {ChangeType: "bad", Target: "t2", Detail: "d2"}}, wantErr: true, errMsg: "changes[1]"},
		{name: "at max changes", input: makeApplyChanges(MaxApplyChanges, validChange), wantErr: false},
		{name: "exceeds max changes", input: makeApplyChanges(MaxApplyChanges+1, validChange), wantErr: true, errMsg: "exceeds maximum of 20"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateApplyChanges(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				if tc.errMsg != "" {
					assert.Contains(t, err.Error(), tc.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateAction(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid bulk_review", input: "bulk_review", wantErr: false},
		{name: "valid review", input: "review", wantErr: false},
		{name: "valid synthesize", input: "synthesize", wantErr: false},
		{name: "valid apply", input: "apply", wantErr: false},
		{name: "valid approve", input: "approve", wantErr: false},
		{name: "valid reject", input: "reject", wantErr: false},
		{name: "empty string", input: "", wantErr: true},
		{name: "invalid value", input: "delete", wantErr: true},
		{name: "case sensitive", input: "Review", wantErr: true},
		{name: "extra spaces", input: " review ", wantErr: true},
		{name: "partial match", input: "rev", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAction(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testMustBeOf)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStatusConstants(t *testing.T) {
	// Verify all status constants have expected values.
	assert.Equal(t, "pending", StatusPending)
	assert.Equal(t, "approved", StatusApproved)
	assert.Equal(t, "rejected", StatusRejected)
	assert.Equal(t, "applied", StatusApplied)
	assert.Equal(t, "superseded", StatusSuperseded)
	assert.Equal(t, "rolled_back", StatusRolledBack)
}

func TestLimitConstants(t *testing.T) {
	assert.Equal(t, 20, DefaultLimit)    //nolint:revive // verifying constant value
	assert.Equal(t, 100, MaxLimit)       //nolint:revive // verifying constant value
	assert.Equal(t, 20, MaxApplyChanges) //nolint:revive // verifying constant value
	assert.Equal(t, 50, MaxInsightIDs)   //nolint:revive // verifying constant value
}

func TestEntityInsightSummaryJSONTags(t *testing.T) {
	s := EntityInsightSummary{
		EntityURN:  "urn:li:dataset:test",
		Count:      3, //nolint:revive // test value
		Categories: []string{"correction", "enhancement"},
		LatestAt:   "2025-01-01T00:00:00Z",
	}

	data, err := json.Marshal(s)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "urn:li:dataset:test", m["entity_urn"])
	assert.InDelta(t, 3, m["count"], 0.01) //nolint:revive // test value
	assert.Equal(t, "2025-01-01T00:00:00Z", m["latest_at"])
	cats, ok := m["categories"].([]any)
	require.True(t, ok)
	assert.Len(t, cats, 2)
}

func TestProposedChangeJSONTags(t *testing.T) {
	pc := ProposedChange{
		ChangeType:       "update_description",
		Target:           "description",
		CurrentValue:     "old",
		SuggestedValue:   "new",
		SourceInsightIDs: []string{"id-1", "id-2"},
	}

	data, err := json.Marshal(pc)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "update_description", m["change_type"])
	assert.Equal(t, "description", m["target"])
	assert.Equal(t, "old", m["current_value"])
	assert.Equal(t, "new", m["suggested_value"])
	ids, ok := m["source_insight_ids"].([]any)
	require.True(t, ok)
	assert.Len(t, ids, 2)
}

func TestChangesetJSONTags(t *testing.T) {
	cs := Changeset{
		ID:               "cs-1",
		TargetURN:        "urn:li:dataset:foo",
		ChangeType:       "update_description",
		PreviousValue:    map[string]any{"desc": "old"},
		NewValue:         map[string]any{"desc": "new"},
		SourceInsightIDs: []string{"ins-1"},
		ApprovedBy:       "admin",
		AppliedBy:        "admin",
		RolledBack:       false,
	}

	data, err := json.Marshal(cs)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "cs-1", m["id"])
	assert.Equal(t, "urn:li:dataset:foo", m["target_urn"])
	assert.Equal(t, "update_description", m["change_type"])
	assert.NotNil(t, m["previous_value"])
	assert.NotNil(t, m["new_value"])
	assert.Equal(t, "admin", m["approved_by"])
	assert.Equal(t, "admin", m["applied_by"]) //nolint:revive // test value
	assert.Equal(t, false, m["rolled_back"])

	// Omitempty fields should not be present when zero-valued
	_, hasRolledBackBy := m["rolled_back_by"]
	assert.False(t, hasRolledBackBy, "rolled_back_by should be omitted when empty")
	_, hasRolledBackAt := m["rolled_back_at"]
	assert.False(t, hasRolledBackAt, "rolled_back_at should be omitted when nil")
}

func TestInsightJSONTags(t *testing.T) {
	ins := Insight{
		ID:          "ins-1",
		SessionID:   "sess-1",
		CapturedBy:  "user",
		Persona:     "analyst",
		Source:      "agent_discovery",
		Category:    "correction",
		InsightText: "test insight text that is long enough",
		Confidence:  testMedium,
		EntityURNs:  []string{"urn:li:dataset:foo"}, //nolint:revive // test value
		Status:      StatusPending,
	}

	data, err := json.Marshal(ins)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "ins-1", m["id"])
	assert.Equal(t, "sess-1", m["session_id"])
	assert.Equal(t, "user", m["captured_by"])
	assert.Equal(t, "analyst", m["persona"])
	assert.Equal(t, "agent_discovery", m["source"])
	assert.Equal(t, "correction", m["category"]) //nolint:revive // test value
	assert.Equal(t, "test insight text that is long enough", m["insight_text"])
	assert.Equal(t, testMedium, m["confidence"])
	assert.Equal(t, StatusPending, m["status"])

	// Omitempty lifecycle fields should not be present when zero-valued
	_, hasReviewedBy := m["reviewed_by"]
	assert.False(t, hasReviewedBy, "reviewed_by should be omitted when empty")
	_, hasReviewedAt := m["reviewed_at"]
	assert.False(t, hasReviewedAt, "reviewed_at should be omitted when nil")
	_, hasReviewNotes := m["review_notes"]
	assert.False(t, hasReviewNotes, "review_notes should be omitted when empty")
	_, hasAppliedBy := m["applied_by"]
	assert.False(t, hasAppliedBy, "applied_by should be omitted when empty")
	_, hasAppliedAt := m["applied_at"]
	assert.False(t, hasAppliedAt, "applied_at should be omitted when nil")
	_, hasChangesetRef := m["changeset_ref"]
	assert.False(t, hasChangesetRef, "changeset_ref should be omitted when empty")
}

func TestApplyChangeJSONTags(t *testing.T) {
	ac := ApplyChange{
		ChangeType: "add_tag", //nolint:revive // test value
		Target:     "col_name",
		Detail:     "pii",
	}

	data, err := json.Marshal(ac)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "add_tag", m["change_type"])
	assert.Equal(t, "col_name", m["target"])
	assert.Equal(t, "pii", m["detail"])
}

func TestEntityMetadataJSONTags(t *testing.T) {
	em := EntityMetadata{
		Description:   "A table",
		Tags:          []string{"tag1"},
		GlossaryTerms: []string{"term1"},
		Owners:        []string{"owner1"},
	}

	data, err := json.Marshal(em)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "A table", m["description"])
	assert.NotNil(t, m["tags"])
	assert.NotNil(t, m["glossary_terms"])
	assert.NotNil(t, m["owners"])
}

func TestInsightUpdateJSONTags(t *testing.T) {
	// Non-empty fields should be present
	u := InsightUpdate{
		InsightText: "updated text",
		Category:    "correction",
		Confidence:  "high", //nolint:revive // test value
	}

	data, err := json.Marshal(u)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "updated text", m["insight_text"])
	assert.Equal(t, "correction", m["category"])
	assert.Equal(t, "high", m["confidence"])

	// Empty fields should be omitted
	empty := InsightUpdate{}
	data, err = json.Marshal(empty)
	require.NoError(t, err)

	var m2 map[string]any
	err = json.Unmarshal(data, &m2)
	require.NoError(t, err)

	_, hasText := m2["insight_text"]
	assert.False(t, hasText, "insight_text should be omitted when empty")
	_, hasCat := m2["category"]
	assert.False(t, hasCat, "category should be omitted when empty")
	_, hasConf := m2["confidence"]
	assert.False(t, hasConf, "confidence should be omitted when empty")
}

func TestInsightStatsJSONTags(t *testing.T) {
	s := InsightStats{
		TotalPending: 5, //nolint:revive // test value
		ByEntity: []EntityInsightSummary{
			{EntityURN: "urn:li:dataset:foo", Count: 2, Categories: []string{"correction"}, LatestAt: "2025-01-01"},
		},
		ByCategory:   map[string]int{"correction": 2, "enhancement": 3}, //nolint:revive // test values
		ByConfidence: map[string]int{"high": 1, "medium": 4},            //nolint:revive // test values
		ByStatus:     map[string]int{StatusPending: 5},                  //nolint:revive // test value
	}

	data, err := json.Marshal(s)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "total_pending", findJSONKey(data, "total_pending"))
	assert.Equal(t, "by_entity", findJSONKey(data, "by_entity"))
	assert.Equal(t, "by_category", findJSONKey(data, "by_category"))
	assert.Equal(t, "by_confidence", findJSONKey(data, "by_confidence"))
	assert.Equal(t, "by_status", findJSONKey(data, "by_status"))
	assert.InDelta(t, 5, m["total_pending"], 0.01) //nolint:revive // test value
}

// makeApplyChanges creates a slice of n copies of the given ApplyChange.
func makeApplyChanges(n int, template ApplyChange) []ApplyChange {
	result := make([]ApplyChange, n)
	for i := range result {
		result[i] = template
	}
	return result
}

// findJSONKey returns the key if present in the JSON bytes, or empty string.
func findJSONKey(data []byte, key string) string {
	s := string(data)
	target := `"` + key + `"`
	if strings.Contains(s, target) {
		return key
	}
	return ""
}
