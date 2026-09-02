package pagewalk

import (
	"encoding/json"
	"errors"
	"testing"
)

// Size is what an inline walk reports as body_bytes: the merged bytes, an
// item and its separator each, and nothing from a page that did not fit.
func TestInlineMergeSize(t *testing.T) {
	m := &InlineMerge{Limit: 10}
	if m.Size() != 0 {
		t.Fatalf("Size = %d on an empty merge; want 0", m.Size())
	}
	if err := m.Add([]json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`1`)}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if m.Size() != 5 {
		t.Errorf("Size = %d; want 5 (two items plus a separator each)", m.Size())
	}
	if err := m.Add([]json.RawMessage{json.RawMessage(`"toolong"`)}); !errors.Is(err, ErrPageDoesNotFit) {
		t.Fatalf("Add past the limit: %v; want ErrPageDoesNotFit", err)
	}
	if m.Size() != 5 {
		t.Errorf("Size = %d after a refused page; want 5", m.Size())
	}
}
