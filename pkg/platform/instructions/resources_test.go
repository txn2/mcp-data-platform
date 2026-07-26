package instructions

import (
	"strings"
	"testing"
)

func TestResourcesNote_SteersThroughSearchAndFetch(t *testing.T) {
	note := ResourcesNote([]string{"search", "fetch", "trino_query"})

	if !strings.Contains(note, ResourcePositioning) {
		t.Errorf("note does not carry the positioning statement:\n%s", note)
	}
	for _, want := range []string{
		"Before you format a deliverable, `search`",
		"`fetch` (pass the result's `reference`)",
		"Material attached to a prompt is authoritative",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "resources/list") {
		t.Errorf("a caller who can reach search must not be steered to resources/list:\n%s", note)
	}
}

// A persona denied `search` still has managed resources: it reaches them over
// the resources/list protocol method, which is not a tool and so is never in
// accessibleTools. The note must name that door instead of a tool the caller
// would be refused.
func TestResourcesNote_FallsBackToProtocolMethodWithoutSearch(t *testing.T) {
	note := ResourcesNote([]string{"trino_query"})

	if !strings.Contains(note, "`resources/list`") {
		t.Errorf("note does not name resources/list for a caller without search:\n%s", note)
	}
	if strings.Contains(note, "`search`") || strings.Contains(note, "`fetch`") {
		t.Errorf("note names a tool the caller cannot reach:\n%s", note)
	}
	if !strings.Contains(note, ResourcePositioning) {
		t.Errorf("the positioning statement is not conditional on tool access:\n%s", note)
	}
}

// fetch is registered alongside search but a persona may deny it on its own, so
// the "read it in full" half must drop out while the resolve half survives.
func TestResourcesNote_OmitsFetchWhenDenied(t *testing.T) {
	note := ResourcesNote([]string{"search"})

	if strings.Contains(note, "`fetch`") {
		t.Errorf("note names fetch for a caller denied it:\n%s", note)
	}
	if !strings.Contains(note, "resolve it with `search` instead of asking them to paste it.") {
		t.Errorf("note dropped the named-file rule along with fetch:\n%s", note)
	}
}

// Compose is what platform_info uses to append the note, so the note must
// survive that layering intact rather than only reading well on its own.
func TestResourcesNote_ComposesBeneathTheBaseline(t *testing.T) {
	tools := []string{"search", "fetch"}
	out := ComposeForCaller("ACME context.", tools, nil, nil, ResourcesNote(tools))

	if !strings.HasPrefix(out, "How to operate this platform:") {
		t.Errorf("composed instructions should still lead with the baseline:\n%s", out)
	}
	baselineAt := strings.Index(out, "How to operate this platform:")
	noteAt := strings.Index(out, "Uploaded reference material:")
	adminAt := strings.Index(out, "ACME context.")
	if noteAt < 0 {
		t.Fatalf("the resources note is absent from the composed stack:\n%s", out)
	}
	if baselineAt >= adminAt || adminAt >= noteAt {
		t.Errorf("layer order wrong (baseline %d, admin %d, note %d):\n%s", baselineAt, adminAt, noteAt, out)
	}
}
