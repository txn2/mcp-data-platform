package indexjobs

import (
	"bytes"
	"testing"
)

func TestTextHash(t *testing.T) {
	t.Parallel()
	// Deterministic and matches the hash planVectors stamps: equal text
	// hashes equal, different text differs.
	a := TextHash("run a query")
	if !bytes.Equal(a, TextHash("run a query")) {
		t.Error("TextHash is not deterministic for equal input")
	}
	if bytes.Equal(a, TextHash("run a different query")) {
		t.Error("TextHash collided on different input")
	}
	if len(a) != 32 {
		t.Errorf("TextHash length = %d; want 32 (SHA-256)", len(a))
	}
}

func TestContentGap(t *testing.T) {
	t.Parallel()
	existing := map[string]Vector{
		"a": {ItemID: "a", TextHash: TextHash("ta")},
		"b": {ItemID: "b", TextHash: TextHash("tb")},
	}
	tests := []struct {
		name  string
		items []Item
		want  bool
	}{
		{
			name:  "in sync",
			items: []Item{{ItemID: "a", Text: "ta"}, {ItemID: "b", Text: "tb"}},
			want:  false,
		},
		{
			name:  "added item",
			items: []Item{{ItemID: "a", Text: "ta"}, {ItemID: "b", Text: "tb"}, {ItemID: "c", Text: "tc"}},
			want:  true,
		},
		{
			name:  "removed item",
			items: []Item{{ItemID: "a", Text: "ta"}},
			want:  true,
		},
		{
			name:  "changed text",
			items: []Item{{ItemID: "a", Text: "ta-edited"}, {ItemID: "b", Text: "tb"}},
			want:  true,
		},
		{
			name:  "renamed item (same count, different membership)",
			items: []Item{{ItemID: "a", Text: "ta"}, {ItemID: "z", Text: "tb"}},
			want:  true,
		},
		{
			// Duplicate ItemIDs must not be mistaken for drift: the
			// distinct-id comparison keeps a hypothetical Source that
			// emits a repeat from churning forever.
			name:  "duplicate item id, content matches",
			items: []Item{{ItemID: "a", Text: "ta"}, {ItemID: "b", Text: "tb"}, {ItemID: "a", Text: "ta"}},
			want:  false,
		},
		{
			name:  "empty live against empty existing",
			items: nil,
			want:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ex := existing
			if tc.name == "empty live against empty existing" {
				ex = map[string]Vector{}
			}
			if got := ContentGap(tc.items, ex); got != tc.want {
				t.Errorf("ContentGap() = %v; want %v", got, tc.want)
			}
		})
	}
}
