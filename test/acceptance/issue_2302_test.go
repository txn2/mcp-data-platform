//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// Issue #2302: a D2 source is an asset the platform renders. What is held
// here, through the surface an agent and a reader meet:
//
//   - a D2 source with nested containers and an icon from a managed resource,
//     saved as text/vnd.d2, opens as the drawing, laid out automatically, on
//     the asset page, an authenticated share and a public share signed out,
//     with the source one toggle away, and reads back unchanged;
//   - a source naming an icon on an external host is refused, naming the URL;
//   - an invalid source is refused with the compiler's error and its line, and
//     no asset is created;
//   - the tile is the drawing, light and dark.
//
// Wire forms: save_asset's `content` admits a string or a JSON object or
// array; a D2 source is text, so it is sent as a JSON string (an object is
// refused for a type that is not JSON). `references` is a JSON array of
// strings. `name`, `content_type` and `purpose` are typed string.
//
// The ticket said fetch returns the source. fetch on an asset returns its
// record, not its text (#2377); the read is manage_asset get_content.

const issue2302Purpose = "Acceptance for #2302: a D2 diagram rendered by the platform."

// issue2302Source names its icon by the resource URI it is given.
func issue2302Source(icon string) string {
	return fmt.Sprintf(`direction: right
users: Users {shape: person}
vpc: VPC {
  web: Web
  api: API {icon: %s}
  data: Data {
    db: Postgres {shape: cylinder}
    cache: Cache
  }
  web -> api: HTTPS
  api -> data.db
  api -> data.cache
}
users -> vpc.web
runbook: |md
  **Runbook**: page the on-call
|
vpc.api -> runbook
`, icon)
}

func TestIssue2302_AD2SourceDrawsWhereItIsReadAndReadsBackUnchanged(t *testing.T) {
	c := connect(t)
	_, icon := issue2395Upload(t, c, "api.png", "image/png", issue2395PNG)
	src := issue2302Source(icon)
	saved := c.call("save_asset", map[string]any{
		"name": fmt.Sprintf("Acceptance 2302 %d", len(src)), "content": src, "content_type": "text/vnd.d2",
		"references": []string{icon}, "purpose": issue2302Purpose,
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id, "purpose": issue2302Purpose})
	})
	if got := storedContent(t, c, id); got != src {
		t.Errorf("get_content did not return the source unchanged:\n%s", got)
	}
	runDrawnSuite(t, []drawnCase{{
		Name: "D2 nested containers with a resource icon", Kind: "d2", Asset: id,
		AuthShare: issue2373Share(t, c, id, "authenticated"), PublicShare: issue2373Share(t, c, id, "public"),
		SourceLabel: "View D2", SourceText: "api -> data.db", NoLinks: true,
		Marks: &drawnMarks{Selector: "foreignObject", Count: 1},
	}})
}

func TestIssue2302_AnExternalIconIsRefusedNamingTheURL(t *testing.T) {
	c := connect(t)
	text := refusedSave(t, c, "text/vnd.d2", issue2302Source("https://icons.terrastruct.com/essentials/112-server.svg"), issue2302Purpose)
	if !strings.Contains(text, "https://icons.terrastruct.com/essentials/112-server.svg") {
		t.Errorf("the refusal does not name the URL: %s", text)
	}
}

func TestIssue2302_AnInvalidSourceIsRefusedWithItsLine(t *testing.T) {
	c := connect(t)
	text := refusedSave(t, c, "text/vnd.d2", "a -> b\nc -> -> d\n", issue2302Purpose)
	for _, want := range []string{"does not parse", "diagram.d2:2:", "Nothing was written"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not contain %q: %s", want, text)
		}
	}
}

func TestIssue2302_TheTileIsTheDrawingLightAndDark(t *testing.T) {
	c := connect(t)
	id := saveDrawn(t, c, "Acceptance 2302 tile", "text/vnd.d2", "vpc: {\n  web -> api -> db\n}\nusers -> vpc.web\n", issue2302Purpose)
	for _, variant := range []string{"light", "dark"} {
		if ink := issue2373Ink(issue2068Tile(t, c, id, variant)); ink < 0.005 {
			t.Errorf("the %s tile is blank (%.4f)", variant, ink)
		}
	}
}
