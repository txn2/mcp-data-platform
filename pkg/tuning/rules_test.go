package tuning

import "testing"

const (
	rulesTestQualityThreshold = 0.7
	rulesTestMaxQueryLimit    = 10000
	rulesTestMaxLimit5000     = 5000
)

func TestDefaultRules(t *testing.T) {
	rules := DefaultRules()

	if !rules.RequireDataHubCheck {
		t.Error("RequireDataHubCheck should be true by default")
	}
	if !rules.WarnOnDeprecated {
		t.Error("WarnOnDeprecated should be true by default")
	}
	if rules.QualityThreshold != rulesTestQualityThreshold {
		t.Errorf("QualityThreshold = %f, want 0.7", rules.QualityThreshold)
	}
	if rules.MaxQueryLimit != rulesTestMaxQueryLimit {
		t.Errorf("MaxQueryLimit = %d, want 10000", rules.MaxQueryLimit)
	}
}

func TestRuleEngine_NoViolations(t *testing.T) {
	engine := NewRuleEngine(DefaultRules())
	score := 0.9
	metadata := QueryMetadata{QualityScore: &score, IsDeprecated: false, ContainsPII: false}
	violations := engine.CheckQueryExecution(metadata)
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d", len(violations))
	}
}

func TestRuleEngine_QualityThresholdViolation(t *testing.T) {
	engine := NewRuleEngine(DefaultRules())
	score := 0.5
	violations := engine.CheckQueryExecution(QueryMetadata{QualityScore: &score})

	assertViolationExists(t, violations, "quality_threshold", SeverityWarning)
}

func TestRuleEngine_DeprecatedDataViolation(t *testing.T) {
	engine := NewRuleEngine(DefaultRules())
	violations := engine.CheckQueryExecution(QueryMetadata{
		IsDeprecated:    true,
		DeprecationNote: "Use new_table instead",
	})

	found := findViolation(violations, "deprecated_data")
	if found == nil {
		t.Fatal("expected deprecated_data violation")
	}
	if found.Suggestion != "Use new_table instead" {
		t.Errorf("Suggestion = %q, want %q", found.Suggestion, "Use new_table instead")
	}
}

func TestRuleEngine_PIIAccessDefault(t *testing.T) {
	engine := NewRuleEngine(DefaultRules())
	violations := engine.CheckQueryExecution(QueryMetadata{ContainsPII: true})

	for _, v := range violations {
		if v.Rule == "pii_access" {
			t.Error("unexpected pii_access violation (RequirePIIAcknowledgment is false)")
		}
	}
}

func TestRuleEngine_PIIAccessRequired(t *testing.T) {
	piiEngine := NewRuleEngine(&Rules{RequirePIIAcknowledgment: true})
	violations := piiEngine.CheckQueryExecution(QueryMetadata{ContainsPII: true})

	assertViolationExists(t, violations, "pii_access", SeverityInfo)
}

func findViolation(violations []Violation, rule string) *Violation {
	for i := range violations {
		if violations[i].Rule == rule {
			return &violations[i]
		}
	}
	return nil
}

func assertViolationExists(t *testing.T, violations []Violation, rule string, severity Severity) {
	t.Helper()
	v := findViolation(violations, rule)
	if v == nil {
		t.Errorf("expected %s violation", rule)
		return
	}
	if v.Severity != severity {
		t.Errorf("Severity = %v, want %v", v.Severity, severity)
	}
}

func TestRuleEngine_Methods(t *testing.T) {
	rules := &Rules{
		RequireDataHubCheck: true,
		MaxQueryLimit:       rulesTestMaxLimit5000,
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
		if engine.GetMaxQueryLimit() != rulesTestMaxLimit5000 {
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
	if engine.GetMaxQueryLimit() != rulesTestMaxQueryLimit {
		t.Errorf("GetMaxQueryLimit() = %d, want 10000 (default)", engine.GetMaxQueryLimit())
	}
}
