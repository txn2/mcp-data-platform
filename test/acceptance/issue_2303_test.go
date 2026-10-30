//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #2303: a Graphviz DOT source is an asset the portal draws. What is
// held here, through the surface an agent and a reader meet:
//
//   - a DOT source saved as text/vnd.graphviz opens as the drawing on the asset
//     page, an authenticated share and a public share signed out, with the
//     source one toggle away, and the source reads back unchanged;
//   - a source choosing neato is laid out by neato;
//   - a source that does not parse is refused with the parser's error and its
//     line, and no asset is created;
//   - a node whose URL is javascript: draws with no link in the page;
//   - the tile is the drawing, light and dark.
//
// Wire forms: save_asset's `content` admits a string or a JSON object or
// array; a DOT source is text, so it is sent as a JSON string (an object is
// refused for a type that is not JSON, which TestIssue2296_* holds).
// `name`, `content_type` and `purpose` are typed string and sent once each.
// manage_asset's `action`, `asset_id`, `access_mode` and `expires_in` are
// typed string, sent as strings, and its `limit` an integer, sent as one.
//
// The ticket said fetch returns the source. fetch on an asset returns its
// record (name, type, size, provenance) and not its text; the read an agent
// makes of the source is manage_asset get_content, which is what is held.

const issue2303Purpose = "Acceptance for #2303: a Graphviz DOT source drawn by the portal."

// issue2303Fan is one root with three leaves: dot lays the leaves on one rank,
// neato does not. The first node carries a javascript: URL and a tooltip, which
// the drawing must keep as text and drop as a link.
const issue2303Fan = `digraph fan {
  root [URL="javascript:alert(1)" tooltip="the root"];
  root -> left; root -> middle; root -> right;
  left -> leaf_a; middle -> leaf_b;
}`

const issue2303Neato = `digraph fan {
  layout=neato;
  root -> left; root -> middle; root -> right;
  left -> leaf_a; middle -> leaf_b;
}`

// drawnCase is one asset the drawn-sources browser suite opens; its JSON
// names are the suite's (ui/e2e/drawn-sources/drawn-sources.spec.ts).
type drawnCase struct {
	Name            string          `json:"name"`
	Kind            string          `json:"kind"`
	Asset           string          `json:"asset"`
	AuthShare       string          `json:"authShare,omitempty"`
	PublicShare     string          `json:"publicShare,omitempty"`
	SourceLabel     string          `json:"sourceLabel"`
	SourceText      string          `json:"sourceText"`
	NoLinks         bool            `json:"noLinks,omitempty"`
	LeavesShareARow *bool           `json:"leavesShareARow,omitempty"`
	Marks           *drawnMarks     `json:"marks,omitempty"`
	Selection       *drawnSelection `json:"selection,omitempty"`
}

type drawnMarks struct {
	Selector string `json:"selector"`
	Count    int    `json:"count"`
}

type drawnSelection struct {
	Pick     string `json:"pick"`
	Filtered string `json:"filtered"`
}

// runDrawnSuite opens every case in a real browser through the drawn-sources
// Playwright suite and fails with its output when any surface did not draw.
func runDrawnSuite(t *testing.T, cases []drawnCase) {
	t.Helper()
	raw, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/drawn-sources/playwright.config.ts")
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(), "DRAWN_BASE_URL="+baseURL(), "DRAWN_CASES="+string(raw),
		"DRAWN_SIGN_IN_EMAIL="+issue2373SignInEmail, "DRAWN_SIGN_IN_PASSWORD="+issue2373SignInPassword)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright:\n%s", out)
	if err != nil {
		t.Fatalf("a drawing did not draw where it is read: %v", err)
	}
}

// saveDrawn saves content as an asset and returns its id, deleting it when
// the test ends. content is sent exactly as given: a string or a JSON value.
func saveDrawn(t *testing.T, c *client, name, contentType string, content any, purpose string) string {
	t.Helper()
	saved := c.call("save_asset", map[string]any{
		"name":         fmt.Sprintf("%s %d", name, time.Now().UnixNano()),
		"content":      content,
		"content_type": contentType,
		"purpose":      purpose,
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id, "purpose": purpose})
	})
	return id
}

// refusedSave saves content expecting a refusal, and returns its text after
// checking no asset by that name was created.
func refusedSave(t *testing.T, c *client, contentType string, content any, purpose string) string {
	t.Helper()
	name := fmt.Sprintf("Acceptance refused %d", time.Now().UnixNano())
	res, text, err := c.callRaw("save_asset", map[string]any{
		"name": name, "content": content, "content_type": contentType, "purpose": purpose,
	})
	if err != nil {
		text = err.Error()
	} else if !res.IsError {
		t.Fatalf("the save was accepted: %s", text)
	}
	listed := c.call("manage_asset", map[string]any{"action": "list", "limit": 100, "purpose": purpose})
	assets, _ := listed["assets"].([]any)
	for _, a := range assets {
		if m, _ := a.(map[string]any); m["name"] == name {
			t.Errorf("a refused save created an asset: %v", m)
		}
	}
	return text
}

// storedContent is the source manage_asset get_content reads back.
func storedContent(t *testing.T, c *client, assetID string) string {
	t.Helper()
	doc := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": assetID, "purpose": issue2303Purpose})
	content, _ := doc["content"].(string)
	if content == "" {
		t.Fatalf("get_content returned no content: %v", doc)
	}
	return content
}

// TestIssue2303_ADOTSourceDrawsWhereItIsReadAndFetchReturnsIt: the drawing on
// three surfaces, the source one toggle away, neato honored, no link from a
// javascript: URL, and the source read back unchanged.
func TestIssue2303_ADOTSourceDrawsWhereItIsReadAndReadsBackUnchanged(t *testing.T) {
	c := connect(t)
	fan := saveDrawn(t, c, "Acceptance 2303 fan", "text/vnd.graphviz", issue2303Fan, issue2303Purpose)
	neato := saveDrawn(t, c, "Acceptance 2303 neato", "text/vnd.graphviz", issue2303Neato, issue2303Purpose)

	if got := storedContent(t, c, fan); got != issue2303Fan {
		t.Errorf("get_content did not return the source unchanged:\n%s", got)
	}
	dot, neatoLayout := true, false
	runDrawnSuite(t, []drawnCase{
		{
			Name: "DOT fan laid out by dot", Kind: "graphviz", Asset: fan,
			AuthShare: issue2373Share(t, c, fan, "authenticated"), PublicShare: issue2373Share(t, c, fan, "public"),
			SourceLabel: "View DOT", SourceText: "root -> middle", NoLinks: true, LeavesShareARow: &dot,
		},
		{
			Name: "DOT fan laid out by neato", Kind: "graphviz", Asset: neato,
			SourceLabel: "View DOT", SourceText: "layout=neato", LeavesShareARow: &neatoLayout,
		},
	})
}

// TestIssue2303_ASourceThatDoesNotParseIsRefusedNamingItsLine: the refusal
// carries the parser's error and the line:column it stopped at, and nothing is
// written.
func TestIssue2303_ASourceThatDoesNotParseIsRefusedNamingItsLine(t *testing.T) {
	c := connect(t)
	text := refusedSave(t, c, "text/vnd.graphviz", "digraph {\n  a -> b\n  c -> -> d\n}", issue2303Purpose)
	for _, want := range []string{"does not parse", "3:8", "Nothing was written"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not contain %q: %s", want, text)
		}
	}
}

// TestIssue2303_TheTileIsTheDrawingLightAndDark: the platform's renderer draws
// the graph into the asset's tile in both schemes.
func TestIssue2303_TheTileIsTheDrawingLightAndDark(t *testing.T) {
	c := connect(t)
	id := saveDrawn(t, c, "Acceptance 2303 tile", "text/vnd.graphviz", issue2303Fan, issue2303Purpose)
	for _, variant := range []string{"light", "dark"} {
		if ink := issue2373Ink(issue2068Tile(t, c, id, variant)); ink < 0.005 {
			t.Errorf("the %s tile is blank (%.4f of its pixels differ from the background)", variant, ink)
		}
	}
}
