package tuning

import "testing"

func TestDefaultRules(t *testing.T) {
	rules := DefaultRules()

	if !rules.RequireDataHubCheck {
		t.Error("RequireDataHubCheck should be true by default")
	}
	if !rules.WarnOnDeprecated {
		t.Error("WarnOnDeprecated should be true by default")
	}
	if rules.QualityThreshold != 0.7 {
		t.Errorf("QualityThreshold = %f, want 0.7", rules.QualityThreshold)
	}
	if rules.MaxQueryLimit != 10000 {
		t.Errorf("MaxQueryLimit = %d, want 10000", rules.MaxQueryLimit)
	}
}

func TestRuleEngine_CheckQueryExecution(t *testing.T) {
	engine := NewRuleEngine(DefaultRules())

	t.Run("no violations", func(t *testing.T) {
		score := 0.9
		metadata := QueryMetadata{
			QualityScore: &score,
			IsDeprecated: false,
			ContainsPII:  false,
		}
		violations := engine.CheckQueryExecution(metadata)
		if len(violations) != 0 {
			t.Errorf("expected 0 violations, got %d", len(violations))
		}
	})

	t.Run("quality threshold violation", func(t *testing.T) {
		score := 0.5
		metadata := QueryMetadata{
			QualityScore: &score,
		}
		violations := engine.CheckQueryExecution(metadata)

		found := false
		for _, v := range violations {
			if v.Rule == "quality_threshold" {
				found = true
				if v.Severity != SeverityWarning {
					t.Errorf("Severity = %v, want %v", v.Severity, SeverityWarning)
				}
			}
		}
		if !found {
			t.Error("expected quality_threshold violation")
		}
	})

	t.Run("deprecated data violation", func(t *testing.T) {
		metadata := QueryMetadata{
			IsDeprecated:    true,
			DeprecationNote: "Use new_table instead",
		}
		violations := engine.CheckQueryExecution(metadata)

		found := false
		for _, v := range violations {
			if v.Rule == "deprecated_data" {
				found = true
				if v.Suggestion != "Use new_table instead" {
					t.Errorf("Suggestion = %q, want %q", v.Suggestion, "Use new_table instead")
				}
			}
		}
		if !found {
			t.Error("expected deprecated_data violation")
		}
	})

	t.Run("PII access (no violation by default)", func(t *testing.T) {
		metadata := QueryMetadata{
			ContainsPII: true,
		}
		violations := engine.CheckQueryExecution(metadata)

		// Default rules don't require PII acknowledgment
		for _, v := range violations {
			if v.Rule == "pii_access" {
				t.Error("unexpected pii_access violation (RequirePIIAcknowledgment is false)")
			}
		}
	})

	t.Run("PII access with acknowledgment required", func(t *testing.T) {
		piiEngine := NewRuleEngine(&Rules{
			RequirePIIAcknowledgment: true,
		})
		metadata := QueryMetadata{
			ContainsPII: true,
		}
		violations := piiEngine.CheckQueryExecution(metadata)

		found := false
		for _, v := range violations {
			if v.Rule == "pii_access" {
				found = true
				if v.Severity != SeverityInfo {
					t.Errorf("Severity = %v, want %v", v.Severity, SeverityInfo)
				}
			}
		}
		if !found {
			t.Error("expected pii_access violation when RequirePIIAcknowledgment is true")
		}
	})
}

func TestRuleEngine_Methods(t *testing.T) {
	rules := &Rules{
		RequireDataHubCheck: true,
		MaxQueryLimit:       5000,
		Custom: map[string]any{
			"custom_rule": "value",
		},
	}
	engine := NewRuleEngine(rules)

	t.Run("ShouldRequireDataHubCheck", func(t *testing.T) {
		if !engine.ShouldRequireDataHubCheck() {
			t.Error("ShouldRequireDataHubCheck() = false, want true")
		}
	})

	t.Run("GetMaxQueryLimit", func(t *testing.T) {
		if engine.GetMaxQueryLimit() != 5000 {
			t.Errorf("GetMaxQueryLimit() = %d, want 5000", engine.GetMaxQueryLimit())
		}
	})

	t.Run("GetCustomRule found", func(t *testing.T) {
		val, ok := engine.GetCustomRule("custom_rule")
		if !ok {
			t.Fatal("GetCustomRule() returned false")
		}
		if val != "value" {
			t.Errorf("value = %v, want %q", val, "value")
		}
	})

	t.Run("GetCustomRule not found", func(t *testing.T) {
		_, ok := engine.GetCustomRule("nonexistent")
		if ok {
			t.Error("GetCustomRule() returned true for nonexistent rule")
		}
	})
}

func TestNewRuleEngine_NilRules(t *testing.T) {
	engine := NewRuleEngine(nil)
	// Should use defaults
	if engine.GetMaxQueryLimit() != 10000 {
		t.Errorf("GetMaxQueryLimit() = %d, want 10000 (default)", engine.GetMaxQueryLimit())
	}
}
