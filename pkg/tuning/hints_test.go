package tuning

import "testing"

func TestHintManager(t *testing.T) {
	t.Run("SetHint and GetHint", func(t *testing.T) {
		manager := NewHintManager()
		manager.SetHint("test_tool", "This is a test hint")

		hint, ok := manager.GetHint("test_tool")
		if !ok {
			t.Fatal("GetHint() returned false")
		}
		if hint != "This is a test hint" {
			t.Errorf("hint = %q, want %q", hint, "This is a test hint")
		}
	})

	t.Run("GetHint not found", func(t *testing.T) {
		manager := NewHintManager()
		_, ok := manager.GetHint("nonexistent")
		if ok {
			t.Error("GetHint() returned true for nonexistent hint")
		}
	})

	t.Run("SetHints", func(t *testing.T) {
		manager := NewHintManager()
		hints := map[string]string{
			"tool1": "hint1",
			"tool2": "hint2",
		}
		manager.SetHints(hints)

		hint1, _ := manager.GetHint("tool1")
		hint2, _ := manager.GetHint("tool2")
		if hint1 != "hint1" || hint2 != "hint2" {
			t.Error("SetHints() did not set hints correctly")
		}
	})

	t.Run("All", func(t *testing.T) {
		manager := NewHintManager()
		manager.SetHint("tool1", "hint1")
		manager.SetHint("tool2", "hint2")

		all := manager.All()
		if len(all) != 2 {
			t.Errorf("All() returned %d hints, want 2", len(all))
		}
	})
}

func TestDefaultHints(t *testing.T) {
	hints := DefaultHints()

	if len(hints) == 0 {
		t.Error("DefaultHints() returned empty map")
	}

	// Check some expected hints exist
	expectedTools := []string{"datahub_search", "trino_query", "s3_list_buckets"}
	for _, tool := range expectedTools {
		if _, ok := hints[tool]; !ok {
			t.Errorf("DefaultHints() missing hint for %s", tool)
		}
	}
}
