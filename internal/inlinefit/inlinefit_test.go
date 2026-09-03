package inlinefit

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// envelope models a caller's result: fields around a body that is swapped
// as the fit shortens it.
type envelope struct {
	Note string `json:"note"`
	Body any    `json:"body"`
}

func TestRenderWithin_IndentedWhenItFits(t *testing.T) {
	e := &envelope{Note: "n", Body: map[string]any{"a": 1}}
	text, ok := RenderWithin(e, 4096)
	if !ok {
		t.Fatalf("RenderWithin reported it does not fit")
	}
	if !strings.Contains(string(text), "\n  ") {
		t.Errorf("rendering = %q; want the indented form when it fits", text)
	}
}

// TestRenderWithin_CompactRatherThanCut: indentation is whitespace, so a
// result that fits compactly is returned whole rather than having content
// dropped to make the indented form fit.
func TestRenderWithin_CompactRatherThanCut(t *testing.T) {
	rows := make([]map[string]any, 0, 60)
	for i := range 60 {
		rows = append(rows, map[string]any{"id": i, "name": "row", "value": "5feceb66ffc86f38"})
	}
	e := &envelope{Note: "n", Body: map[string]any{"rows": rows}}
	indented, err := Render(e)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	budget := len(compact) + 16
	if budget >= len(indented) {
		t.Fatalf("the case needs a budget between the compact (%d) and indented (%d) renderings", len(compact), len(indented))
	}
	text, ok := RenderWithin(e, budget)
	if !ok || len(text) > budget {
		t.Fatalf("RenderWithin = %d bytes, fits=%v; want the compact rendering inside the %d budget", len(text), ok, budget)
	}
	if NeedsCut(e, budget) {
		t.Errorf("NeedsCut = true; want no cut where re-encoding alone fits")
	}
}

func TestNeedsCut_WhenNeitherRenderingFits(t *testing.T) {
	e := &envelope{Note: "n", Body: strings.Repeat("x", 4000)}
	if !NeedsCut(e, 512) {
		t.Errorf("NeedsCut = false; want a cut where neither rendering fits")
	}
	if NeedsCut(e, 0) {
		t.Errorf("NeedsCut = true on a budget of zero; want everything to fit")
	}
}

func TestFit_KeepsTheLongestPrefixThatFits(t *testing.T) {
	const budget = 1024
	body := strings.Repeat("abcdefghij", 400)
	e := &envelope{Note: "n", Body: body}
	text := Fit(e, budget, BodyText(e.Body), func(s string) { e.Body = s })
	if len(text) > budget {
		t.Fatalf("rendering is %d; want it inside the %d budget", len(text), budget)
	}
	kept, _ := e.Body.(string)
	if kept == "" {
		t.Fatalf("body was emptied; want the longest prefix that fits")
	}
	if !strings.HasPrefix(body, kept) {
		t.Errorf("body = %q; want a prefix of the original", kept)
	}
	// One character more must not fit, or the search settled short.
	e.Body = body[:len(kept)+1]
	if _, ok := RenderWithin(e, budget); ok {
		t.Errorf("a body of %d fits too; the search settled short of the longest prefix", len(kept)+1)
	}
}

// TestFit_ParsedBodyIsNotOverCut: a parsed body renders to several times
// its text, so cutting by the overshoot measured against the parsed form
// would drop far more than the budget calls for. The bisection sizes the
// cut to the budget instead.
func TestFit_ParsedBodyIsNotOverCut(t *testing.T) {
	rows := make([]map[string]any, 0, 30)
	for i := range 30 {
		rows = append(rows, map[string]any{"id": i, "value": "5feceb66ffc86f38"})
	}
	e := &envelope{Note: "n", Body: map[string]any{"rows": rows}}
	body := BodyText(e.Body)
	compact, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	// Under the compact rendering, so re-encoding alone cannot save it and
	// the body has to be cut; well above the envelope, so most of it fits.
	budget := len(compact) - len(body)/4
	text := Fit(e, budget, body, func(s string) { e.Body = s })
	if len(text) > budget {
		t.Errorf("rendering is %d; want it inside the %d budget", len(text), budget)
	}
	kept, _ := e.Body.(string)
	if len(kept) < len(body)/2 {
		t.Errorf("kept %d of %d bytes; want the cut sized to the budget, not to the parsed rendering", len(kept), len(body))
	}
}

func TestFit_EnvelopeAloneOverBudgetEmptiesTheBody(t *testing.T) {
	e := &envelope{Note: strings.Repeat("n", 500), Body: "body"}
	Fit(e, 100, BodyText(e.Body), func(s string) { e.Body = s })
	if kept, _ := e.Body.(string); kept != "" {
		t.Errorf("body = %q; want it emptied when no prefix can fit", kept)
	}
}

func TestFit_NoBudgetOrNoSetterReturnsTheRendering(t *testing.T) {
	e := &envelope{Note: "n", Body: strings.Repeat("x", 4000)}
	if text := Fit(e, 0, "x", func(string) {}); len(text) == 0 {
		t.Errorf("a budget of zero fits everything; want the rendering back")
	}
	if kept, _ := e.Body.(string); len(kept) != 4000 {
		t.Errorf("body was cut under a budget of zero")
	}
	if text := Fit(e, 10, "x", nil); len(text) == 0 {
		t.Errorf("Fit with no setter; want the rendering back unchanged")
	}
}

func TestBodyText(t *testing.T) {
	if got := BodyText(nil); got != "" {
		t.Errorf("BodyText(nil) = %q; want no text, so a fit cannot invent a body", got)
	}
	if got := BodyText("already text"); got != "already text" {
		t.Errorf("BodyText(string) = %q; want it unchanged", got)
	}
	if got := BodyText(map[string]any{"a": 1}); got != `{"a":1}` {
		t.Errorf("BodyText(parsed) = %q; want the compact JSON the upstream sent", got)
	}
	if got := BodyText(make(chan int)); got != "" {
		t.Errorf("BodyText(unmarshalable) = %q; want empty", got)
	}
}

func TestItemsSize(t *testing.T) {
	items := []json.RawMessage{json.RawMessage(`{"id":1}`)}
	compact := int64(len(items[0]))
	if got := ItemsSize(items); got <= compact {
		t.Errorf("ItemsSize = %d; want the rendered size, past the %d compact bytes", got, compact)
	}
	if got := ItemsSize([]json.RawMessage{json.RawMessage(`{`)}); got != math.MaxInt64 {
		t.Errorf("ItemsSize(invalid) = %d; want it unbounded so the merge refuses the page", got)
	}
}

// TestReserve: the envelope headroom is capped at a quarter of the budget,
// so a small budget still merges pages instead of having its first page
// refused by a reserve larger than the budget itself.
func TestReserve(t *testing.T) {
	cases := []struct {
		limit, want int64
	}{
		{32 * 1024, DefaultReserve},
		{4096, 1024},
		{100, 25},
		{1, 0},
	}
	for _, tc := range cases {
		got := Reserve(tc.limit)
		if got != tc.want {
			t.Errorf("Reserve(%d) = %d; want %d", tc.limit, got, tc.want)
		}
		if got >= tc.limit {
			t.Errorf("Reserve(%d) = %d; want headroom that leaves room to merge", tc.limit, got)
		}
	}
}
