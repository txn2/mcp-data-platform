package memory

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateDimension(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid knowledge", DimensionKnowledge, false},
		{"valid event", DimensionEvent, false},
		{"valid entity", DimensionEntity, false},
		{"valid relationship", DimensionRelationship, false},
		{"valid preference", DimensionPreference, false},
		{"empty is valid", "", false},
		{"invalid value", "bogus", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDimension(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid dimension")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCategory(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid correction", CategoryCorrection, false},
		{"valid business_context", CategoryBusinessCtx, false},
		{"valid data_quality", CategoryDataQuality, false},
		{"valid usage_guidance", CategoryUsageGuidance, false},
		{"valid relationship", CategoryRelationship, false},
		{"valid enhancement", CategoryEnhancement, false},
		{"valid general", CategoryGeneral, false},
		{"empty is valid", "", false},
		{"invalid value", "unknown_cat", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCategory(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid category")
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
		{"valid high", ConfidenceHigh, false},
		{"valid medium", ConfidenceMedium, false},
		{"valid low", ConfidenceLow, false},
		{"empty is valid", "", false},
		{"invalid value", "very_high", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfidence(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid confidence")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSource(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid user", SourceUser, false},
		{"valid agent_discovery", SourceAgentDiscovery, false},
		{"valid enrichment_gap", SourceEnrichmentGap, false},
		{"valid automation", SourceAutomation, false},
		{"valid lineage_event", SourceLineageEvent, false},
		{"empty is valid", "", false},
		{"invalid value", "manual", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSource(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid source")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateStatus(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid active", StatusActive, false},
		{"valid stale", StatusStale, false},
		{"valid superseded", StatusSuperseded, false},
		{"valid archived", StatusArchived, false},
		{"empty is valid", "", false},
		{"invalid value", "deleted", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStatus(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid status")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateContent(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		errMsg  string
	}{
		{"empty", "", true, "content is required"},
		{"too short", "short", true, "must be at least"},
		{"exact minimum", strings.Repeat("a", MinContentLen), false, ""},
		{"valid content", "This is a valid memory content entry.", false, ""},
		{"exact maximum", strings.Repeat("x", MaxContentLen), false, ""},
		{"too long", strings.Repeat("x", MaxContentLen+1), true, "must be at most"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContent(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEntityURNs(t *testing.T) {
	tests := []struct {
		name    string
		urns    []string
		wantErr bool
	}{
		{"nil slice", nil, false},
		{"empty slice", []string{}, false},
		{"within limit", make([]string, MaxEntityURNs), false},
		{"over limit", make([]string, MaxEntityURNs+1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEntityURNs(tt.urns)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "exceeds maximum")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRelatedColumns(t *testing.T) {
	tests := []struct {
		name    string
		cols    []RelatedColumn
		wantErr bool
	}{
		{"nil slice", nil, false},
		{"empty slice", []RelatedColumn{}, false},
		{"within limit", make([]RelatedColumn, MaxRelatedCols), false},
		{"over limit", make([]RelatedColumn, MaxRelatedCols+1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRelatedColumns(tt.cols)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "exceeds maximum")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNormalizeConfidence(t *testing.T) {
	assert.Equal(t, ConfidenceMedium, NormalizeConfidence(""))
	assert.Equal(t, ConfidenceHigh, NormalizeConfidence(ConfidenceHigh))
	assert.Equal(t, "custom", NormalizeConfidence("custom"))
}

func TestNormalizeSource(t *testing.T) {
	assert.Equal(t, SourceUser, NormalizeSource(""))
	assert.Equal(t, SourceAutomation, NormalizeSource(SourceAutomation))
	assert.Equal(t, "custom", NormalizeSource("custom"))
}

func TestNormalizeDimension(t *testing.T) {
	assert.Equal(t, DefaultDimension, NormalizeDimension(""))
	assert.Equal(t, DimensionEvent, NormalizeDimension(DimensionEvent))
	assert.Equal(t, "custom", NormalizeDimension("custom"))
}

func TestNormalizeCategory(t *testing.T) {
	assert.Equal(t, CategoryBusinessCtx, NormalizeCategory(""))
	assert.Equal(t, CategoryCorrection, NormalizeCategory(CategoryCorrection))
	assert.Equal(t, "custom", NormalizeCategory("custom"))
}

func TestFilter_EffectiveLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"zero defaults", 0, DefaultLimit},
		{"negative defaults", -5, DefaultLimit},
		{"over max caps", MaxLimit + 50, MaxLimit},
		{"valid passthrough", 42, 42}, //nolint:revive // test value
		{"exact max", MaxLimit, MaxLimit},
		{"exact default", DefaultLimit, DefaultLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Filter{Limit: tt.limit}
			assert.Equal(t, tt.want, f.EffectiveLimit())
		})
	}
}
