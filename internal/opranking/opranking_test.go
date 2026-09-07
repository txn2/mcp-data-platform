package opranking

import (
	"math"
	"testing"
)

func TestCosine(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical unit vectors", []float32{1, 0, 0}, []float32{1, 0, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"zero vector has no signal", []float32{0, 0}, []float32{1, 0}, 0},
		{"empty", nil, []float32{1}, 0},
		{"dimension drift does not panic", []float32{1, 0}, []float32{1, 0, 0}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Cosine(c.a, c.b); math.Abs(got-c.want) > 1e-9 {
				t.Errorf("Cosine = %v; want %v", got, c.want)
			}
		})
	}
}

func TestIsZero(t *testing.T) {
	cases := []struct {
		name string
		v    []float32
		want bool
	}{
		{"all zero", []float32{0, 0, 0}, true},
		{"empty is zero", nil, true},
		{"one non-zero element", []float32{0, 0.0001, 0}, false},
		{"negative counts", []float32{-1, 0}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsZero(c.v); got != c.want {
				t.Errorf("IsZero = %v; want %v", got, c.want)
			}
		})
	}
}

func TestPositional(t *testing.T) {
	if got := Positional(0, 4); got != 1 {
		t.Errorf("top rank = %v; want 1", got)
	}
	if got := Positional(3, 4); got != 0.25 {
		t.Errorf("last rank = %v; want 0.25", got)
	}
	if got := Positional(0, 0); got != 0 {
		t.Errorf("empty set = %v; want 0", got)
	}
}

func TestTokensAndMatchesAll(t *testing.T) {
	fields := []string{"listGifts", "/v1/gifts", "List a constituent's gifts", "giving"}
	cases := []struct {
		name  string
		query string
		want  bool
	}{
		{"every token present across fields", "gift list", true},
		{"one token absent fails the AND", "gift purchase", false},
		{"case is ignored", "GIFT LIST", true},
		{"empty query matches everything", "", true},
		{"token matched by a tag", "giving", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MatchesAll(fields, Tokens(c.query)); got != c.want {
				t.Errorf("MatchesAll(%q) = %v; want %v", c.query, got, c.want)
			}
		})
	}
}

func TestFilterKeepsOnlyFullMatchesInOrder(t *testing.T) {
	cands := []Candidate{
		{ID: "a", Fields: []string{"list gifts"}},
		{ID: "b", Fields: []string{"create gift"}},
		{ID: "c", Fields: []string{"list orders"}},
	}
	got := Filter(cands, "list gift", 0)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("Filter = %+v; want only a", got)
	}
	if !got[0].Lexical {
		t.Error("a filtered row must report a lexical match")
	}
	if got[0].Score != 1 {
		t.Errorf("top row score = %v; want the positional 1", got[0].Score)
	}
}

func TestFilterEmptyQueryReturnsEverythingUnmatched(t *testing.T) {
	cands := []Candidate{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	got := Filter(cands, "  ", 2)
	if len(got) != 2 {
		t.Fatalf("limit not applied: got %d rows", len(got))
	}
	if got[0].Lexical {
		t.Error("an unranked row must not claim a lexical match")
	}
}

func TestScoreCreditsLexicalOnAnUnembeddedCandidate(t *testing.T) {
	cands := []Candidate{
		{ID: "match", Fields: []string{"list orders"}},
		{ID: "unrelated", Fields: []string{"delete widget"}, Vector: []float32{1, 0, 0}},
	}
	got := Score(cands, "orders", []float32{0, 1, 0}, ModeHybrid)
	if got[0].ID != "match" {
		t.Fatalf("hybrid put %q first; the unembedded token match must lead", got[0].ID)
	}
	if !got[0].Lexical {
		t.Error("the token match must be flagged lexical")
	}
}

func TestScoreSemanticIgnoresAnUnembeddedCandidate(t *testing.T) {
	cands := []Candidate{{ID: "match", Fields: []string{"list orders"}}}
	got := Score(cands, "orders", []float32{0, 1, 0}, ModeSemantic)
	if got[0].Score != 0 {
		t.Errorf("semantic score without a vector = %v; want 0", got[0].Score)
	}
}

func TestScoreRanksByCosineUnderSemantic(t *testing.T) {
	cands := []Candidate{
		{ID: "far", Fields: []string{"x"}, Vector: []float32{0, 1}},
		{ID: "near", Fields: []string{"y"}, Vector: []float32{1, 0}},
	}
	got := Score(cands, "anything", []float32{1, 0}, ModeSemantic)
	if got[0].ID != "near" {
		t.Fatalf("semantic ordering = %q first; want near", got[0].ID)
	}
}

func TestBoundPutsMatchesFirstThenBoundedNeighbors(t *testing.T) {
	scored := []Scored{
		{ID: "n1", Score: 0.99},
		{ID: "m1", Score: 0.50, Lexical: true},
		{ID: "n2", Score: 0.98},
		{ID: "m2", Score: 0.10, Lexical: true},
	}
	got := Bound(scored, 0)
	if got.Kept[0].ID != "m1" || got.Kept[1].ID != "m2" {
		t.Fatalf("matches did not lead: %+v", got.Kept)
	}
	if got.MatchedLexical != 2 || got.ShownSemantic != 2 {
		t.Errorf("boundary counts = %d/%d; want 2/2", got.MatchedLexical, got.ShownSemantic)
	}
}

func TestBoundDropsNeighborsBelowTheFloor(t *testing.T) {
	scored := []Scored{{ID: "low", Score: ScoreFloor - 0.01}, {ID: "ok", Score: ScoreFloor}}
	got := Bound(scored, 0)
	if len(got.Kept) != 1 || got.Kept[0].ID != "ok" {
		t.Fatalf("floor not applied: %+v", got.Kept)
	}
}

func TestBoundCapsNeighborsAtTheNeighborLimit(t *testing.T) {
	scored := make([]Scored, 0, NeighborLimit+3)
	for i := range NeighborLimit + 3 {
		scored = append(scored, Scored{ID: string(rune('a' + i)), Score: 0.9})
	}
	got := Bound(scored, 0)
	if got.ShownSemantic != NeighborLimit {
		t.Errorf("neighbors = %d; want the %d cap", got.ShownSemantic, NeighborLimit)
	}
}

func TestBoundLimitStillCaps(t *testing.T) {
	scored := []Scored{
		{ID: "m1", Score: 0.9, Lexical: true},
		{ID: "m2", Score: 0.8, Lexical: true},
		{ID: "m3", Score: 0.7, Lexical: true},
	}
	got := Bound(scored, 2)
	if len(got.Kept) != 2 {
		t.Fatalf("limit = %d rows; want 2", len(got.Kept))
	}
	if got.MatchedLexical != 2 {
		t.Errorf("MatchedLexical = %d; want 2", got.MatchedLexical)
	}
}
