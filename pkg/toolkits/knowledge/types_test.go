package knowledge

import (
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
		{name: "invalid action_type", input: []SuggestedAction{{ActionType: "remove_tag", Target: testTarget, Detail: testDetail}}, wantErr: true, errMsg: "invalid action_type"},
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
