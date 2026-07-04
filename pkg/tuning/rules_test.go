package tuning

import "testing"

const (
	rulesTestQualityThreshold = 0.7
	rulesTestMaxQueryLimit    = 10000
	rulesTestMaxLimit5000     = 5000
)

func TestDefaultRules(t *testing.T) {
	rules := DefaultRules()

	if rules.QualityThreshold != rulesTestQualityThreshold {
		t.Errorf("QualityThreshold = %f, want 0.7", rules.QualityThreshold)
	}
	if rules.MaxQueryLimit != rulesTestMaxQueryLimit {
		t.Errorf("MaxQueryLimit = %d, want 10000", rules.MaxQueryLimit)
	}
	if rules.Custom == nil {
		t.Error("Custom should be initialized")
	}
}

func TestRuleEngine_Methods(t *testing.T) {
	rules := &Rules{
		MaxQueryLimit: rulesTestMaxLimit5000,
		Custom: map[string]any{
			"custom_rule": "value",
		},
	}
	engine := NewRuleEngine(rules)

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
