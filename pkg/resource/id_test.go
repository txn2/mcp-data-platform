package resource

import (
	"testing"
)

func TestGenerateID(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID() error: %v", err)
	}
	if len(id) != 32 {
		t.Errorf("expected 32-char hex ID, got %d chars: %q", len(id), id)
	}

	// Two IDs should be different.
	id2, _ := GenerateID()
	if id == id2 {
		t.Error("two generated IDs should be different")
	}
}
