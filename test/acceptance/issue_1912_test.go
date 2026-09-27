//go:build integration

package acceptance

// Issue #1912: the portal section is named Automations, and a script is the one
// kind of automation that exists. What a person or an agent meets as a product
// concept says "automation"; the tools, the REST routes and every stored
// identifier keep the script name.
//
// These criteria are the agent's side of that, executed through a real MCP
// client: what platform_info and the server instructions tell an agent, what
// tools/list says manage_script and run_script are for, what a search for
// "automation" finds, and where show_scripts sends the human. The portal side
// (nav, tabs, Kind column, the /scripts redirects) is exercised by the
// Playwright suite against the assembled app.

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// issue1912WritingPageTitle is the title of the built-in knowledge page on
// writing a managed script, which a search for "automation" has to reach.
// Search cites a page by its stored id rather than by the slug it ships
// under, so the page is recognized by what fetch says it is.
const issue1912WritingPageTitle = "Writing a managed script"

// TestIssue1912_PlatformInfoSaysAnAutomationIsBuiltWithManageScript: a person
// who asks for an automation gets a script without knowing the word, because
// the text the agent is given says so. The MCP server instructions only send
// the agent to platform_info, so platform_info is where the text is read.
func TestIssue1912_PlatformInfoSaysAnAutomationIsBuiltWithManageScript(t *testing.T) {
	c := connect(t)
	composed, _ := c.call("platform_info", nil)["agent_instructions"].(string)
	for _, want := range []string{"Automations are scripts", "asks for an automation", "`manage_script`", "`schedule_set`"} {
		if !strings.Contains(composed, want) {
			t.Errorf("platform_info does not say %q:\n%s", want, composed)
		}
	}
}

// TestIssue1912_ToolsListSaysManageScriptBuildsAutomations: the descriptions
// tools/list returns say what the tools are for in the words a request comes
// in, and the names and titles a client displays are unchanged.
func TestIssue1912_ToolsListSaysManageScriptBuildsAutomations(t *testing.T) {
	c := connect(t)
	byName := map[string]string{}
	titles := map[string]string{}
	for _, tool := range c.tools() {
		byName[tool.Name] = tool.Description
		titles[tool.Name] = tool.Title
	}
	for _, name := range []string{"manage_script", "run_script", "show_scripts"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("tools/list does not carry %s", name)
		}
	}
	if d := byName["manage_script"]; !strings.HasPrefix(d, "Build and change automations.") || !strings.Contains(d, "managed script") {
		t.Errorf("manage_script description does not open by saying it builds automations as managed scripts:\n%s", d)
	}
	if d := byName["run_script"]; !strings.HasPrefix(d, "Run an automation on request") {
		t.Errorf("run_script description does not say it runs an automation on request:\n%s", d)
	}
	if titles["manage_script"] != "Manage Scripts" || titles["show_scripts"] != "Show Scripts" {
		t.Errorf("display titles changed: manage_script %q, show_scripts %q", titles["manage_script"], titles["show_scripts"])
	}
}

// TestIssue1912_SearchForAutomationFindsTheScriptAuthoringPage: an agent that
// searches the word a person used lands on how a script is written.
func TestIssue1912_SearchForAutomationFindsTheScriptAuthoringPage(t *testing.T) {
	c := connect(t)
	out := c.call("search", map[string]any{
		"intent":  "automation",
		"limit":   50,
		"purpose": "Acceptance #1912: a search for automation finds how one is built.",
	})
	for _, ref := range issue1912PageRefs(out) {
		doc, _ := c.call("fetch", map[string]any{"reference": ref, "purpose": "Acceptance #1912: which page a search hit is."})["document"].(map[string]any)
		if doc["title"] == issue1912WritingPageTitle {
			return
		}
	}
	t.Fatalf("search for \"automation\" does not return the page %q:\n%v", issue1912WritingPageTitle, out)
}

// issue1912PageRefs is every knowledge-page reference a search answered with.
func issue1912PageRefs(out map[string]any) []string {
	var refs []string
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		hits, _ := group["hits"].([]any)
		for _, h := range hits {
			hit, _ := h.(map[string]any)
			if ref, _ := hit["reference"].(string); strings.HasPrefix(ref, "mcp:knowledge_page:") {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// TestIssue1912_ShowScriptsOpensAutomations: the URL show_scripts hands the
// human is the renamed section, and the message speaks of automations. Both
// wire forms the schema admits are sent: no arguments, and a search string.
func TestIssue1912_ShowScriptsOpensAutomations(t *testing.T) {
	c := connect(t)
	for _, args := range []map[string]any{{}, {"search": "daily"}} {
		out := c.call("show_scripts", args)
		url, _ := out["url"].(string)
		if !strings.HasSuffix(url, "/portal/automations") {
			t.Errorf("show_scripts %v url = %q; want it to end in /portal/automations", args, url)
		}
		if msg, _ := out["message"].(string); !strings.Contains(msg, "Automations") {
			t.Errorf("show_scripts %v message does not speak of automations: %q", args, msg)
		}
	}
}

// TestIssue1912_OldScriptLinksAndTheRESTRoutesStillAnswer: a run link mailed
// before the rename still reaches the portal, which sends it on to the same
// run under /automations, and the REST routes the portal reads kept their
// paths.
func TestIssue1912_OldScriptLinksAndTheRESTRoutesStillAnswer(t *testing.T) {
	c := connect(t)
	for _, path := range []string{
		"/portal/scripts/script-001/runs/run-001",
		"/portal/admin/scripts",
		"/portal/automations",
	} {
		req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.base+path, nil)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "<div id=\"root\"") {
			t.Errorf("GET %s: status %d, and the portal shell is not what answered", path, res.StatusCode)
		}
	}
	if status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts", nil); status != http.StatusOK {
		t.Errorf("GET /api/v1/portal/scripts: status %d: %v", status, out)
	}
}
