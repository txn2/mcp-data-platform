//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1701: nothing the platform says to an agent carried a rule about the
// width a stored document lays out at. The save_asset schema describes the
// content and its media type, the instruction baseline's save_asset line is
// about references, and none of the six shipped knowledge pages mentioned
// layout at all. So an agent asked for a dashboard wrote one at whatever width
// its own composition suggested, and the reader who opened it on a phone got a
// document that scrolls sideways.
//
// What these hold, through the surface an agent actually meets: the first
// response of a session names the page, the reference it names resolves on this
// deployment, the page carries the rules rather than a title, and the pages it
// points on to resolve here too.
//
// Wire forms: platform_info is registered through the typed mcp.AddTool form
// over an empty input struct, so its params admit an empty object and an absent
// object and nothing else; both are sent below as literal tools/call params.
// fetch's `reference` and `purpose` are `{"type":"string"}` in its schema, so
// each admits exactly one JSON form and is sent as a JSON string.

// issue1701Ref is the reference the instruction baseline hands an agent. The
// slug is a string on both sides -- the shipped page carries one and the
// instruction text names one -- so only a live call proves they agree.
const issue1701Ref = "mcp:knowledge_page:platform-documents-that-fit-any-screen"

const issue1701Purpose = "Acceptance for #1701: reading the platform's own guidance on writing a document that fits the reader's screen."

// issue1701Instructions returns the agent instructions a session is handed,
// calling platform_info in one of the two forms its schema admits. args nil
// sends absent params; a non-nil empty map sends an empty object.
func issue1701Instructions(t *testing.T, c *client, args map[string]any) string {
	t.Helper()
	res, err := c.session.CallTool(c.ctx, &mcp.CallToolParams{Name: "platform_info", Arguments: args})
	if err != nil {
		t.Fatalf("platform_info: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("platform_info: tool error: %s", firstText(res))
	}
	return firstText(res)
}

// TestIssue1701_TheFirstResponseOfASessionNamesTheLayoutPage is the ticket's
// first criterion: an agent is told the guidance exists before it writes
// anything, rather than finding it on a later search. Both wire forms of
// platform_info are sent, and both have to carry the reference.
func TestIssue1701_TheFirstResponseOfASessionNamesTheLayoutPage(t *testing.T) {
	c := connect(t)
	for _, form := range []struct {
		name string
		args map[string]any
	}{
		{"absent params", nil},
		{"empty object", map[string]any{}},
	} {
		t.Run(form.name, func(t *testing.T) {
			info := issue1701Instructions(t, c, form.args)
			if !strings.Contains(info, issue1701Ref) {
				t.Fatalf("platform_info does not name %s, so an agent writing a document is never told the guidance exists:\n%s", issue1701Ref, info)
			}
		})
	}
}

// TestIssue1701_TheLayoutPageCarriesTheRules is the central criterion: the
// reference resolves on this deployment, and what comes back is the guidance
// rather than a stub. Each string below is one rule the ticket asked for, and
// each is what an agent would have to read to write a document that fits.
func TestIssue1701_TheLayoutPageCarriesTheRules(t *testing.T) {
	c := connect(t)
	out := c.call("fetch", map[string]any{
		"reference": issue1701Ref,
		"purpose":   issue1701Purpose,
	})
	body := fmt.Sprint(out)
	if len(body) < 500 {
		t.Fatalf("%s resolved to %d characters; the reference points at an empty or missing page: %v", issue1701Ref, len(body), out)
	}

	for _, rule := range []struct {
		want  string
		about string
	}{
		{`content="width=device-width, initial-scale=1"`, "the viewport declaration an HTML document owes"},
		{"max-width", "a maximum rather than a fixed width"},
		{"flex-wrap", "rows that wrap instead of overflowing"},
		{"overflow-x", "the scroll box a wide table goes in"},
		{"viewBox", "an SVG that scales rather than sitting at its attribute size"},
		{"repeat(auto-fit", "a column count chosen from the space available"},
	} {
		if !strings.Contains(body, rule.want) {
			t.Errorf("the page does not carry %s (%q is absent), so an agent reading it is not told it", rule.about, rule.want)
		}
	}
}

// TestIssue1701_ThePagesItPointsOnToResolveHere: the page ends by naming the
// two shipped pages that cover what it deliberately leaves out. A reference in
// a page is only worth carrying if it resolves on the deployment the agent is
// talking to, which no unit test on either side can establish.
func TestIssue1701_ThePagesItPointsOnToResolveHere(t *testing.T) {
	c := connect(t)
	page := fmt.Sprint(c.call("fetch", map[string]any{
		"reference": issue1701Ref,
		"purpose":   issue1701Purpose,
	}))

	refs := issue1701PageRefs(page)
	if len(refs) == 0 {
		t.Fatalf("the page names no other page, so this criterion is asserting nothing:\n%s", page)
	}
	for _, ref := range refs {
		t.Run(ref, func(t *testing.T) {
			out := c.call("fetch", map[string]any{
				"reference": ref,
				"purpose":   issue1701Purpose,
			})
			if body := fmt.Sprint(out); len(body) < 500 {
				t.Fatalf("%s resolved to %d characters; the page points at an empty or missing page: %v", ref, len(body), out)
			}
		})
	}
}

// issue1701PageRefs extracts the `mcp:knowledge_page:<slug>` references a body
// names, excluding the page's own reference (the fetch result echoes it).
func issue1701PageRefs(text string) []string {
	const marker = "mcp:knowledge_page:"
	seen := map[string]bool{issue1701Ref: true}
	var refs []string
	for rest := text; ; {
		i := strings.Index(rest, marker)
		if i < 0 {
			return refs
		}
		rest = rest[i+len(marker):]
		end := strings.IndexFunc(rest, func(r rune) bool {
			return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-'
		})
		if end < 0 {
			end = len(rest)
		}
		ref := marker + rest[:end]
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
		rest = rest[end:]
	}
}
