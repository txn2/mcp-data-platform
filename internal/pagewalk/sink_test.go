package pagewalk

import (
	"encoding/json"
	"errors"
	"fmt"
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

// TestInlineMerge_RenderedLimitsWhatTheResultWillHold: with a Rendered
// measurement the limit is on the merged array as the caller renders it, not
// on the compact bytes the pages hold, so a page whose indented rendering
// would pass the limit is refused even though its compact bytes fit
// (issue #1606).
func TestInlineMerge_RenderedLimitsWhatTheResultWillHold(t *testing.T) {
	page := []json.RawMessage{json.RawMessage(`{"id":1,"name":"a"}`)}
	compact := int64(len(page[0]) + 1)

	loose := &InlineMerge{Limit: compact}
	if err := loose.Add(page); err != nil {
		t.Fatalf("compact accounting refused a page that fits: %v", err)
	}

	rendered := &InlineMerge{
		Limit: compact,
		Rendered: func(items []json.RawMessage) int64 {
			b, err := json.MarshalIndent(items, "", "  ")
			if err != nil {
				return 0
			}
			return int64(len(b))
		},
	}
	if err := rendered.Add(page); !errors.Is(err, ErrPageDoesNotFit) {
		t.Errorf("Add = %v; want the page refused, its rendering being past the limit", err)
	}
	if len(rendered.Merged()) != 0 {
		t.Errorf("merged = %v; want a refused page left out", rendered.Merged())
	}
}

// TestInlineMerge_RenderedDoesNotAliasTheMergedSlice: the measurement is
// handed a copy, so a sizer that retains it cannot see later pages appended
// into the merge's own backing array.
func TestInlineMerge_RenderedDoesNotAliasTheMergedSlice(t *testing.T) {
	var seen [][]json.RawMessage
	m := &InlineMerge{
		Limit: 1 << 20,
		Rendered: func(items []json.RawMessage) int64 {
			seen = append(seen, items)
			return 0
		},
	}
	for i := range 3 {
		if err := m.Add([]json.RawMessage{json.RawMessage(fmt.Sprintf(`{"id":%d}`, i))}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	for i, items := range seen {
		if len(items) != i+1 {
			t.Errorf("measurement %d saw %d items; want %d", i, len(items), i+1)
		}
	}
}
