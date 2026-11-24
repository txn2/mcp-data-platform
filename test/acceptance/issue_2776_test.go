//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Acceptance for #2776: a deployment names the scheme its references are
// written under (server.reference_scheme). With acme on the stack (the
// refscheme environment of environments.txt), every reference the platform
// hands out reads acme:<kind>:..., every reference it reads is accepted as
// acme: and as mcp:, what is stored stays mcp:, and a scheme with another
// meaning refuses startup naming the key.
//
// Wire forms: references are JSON strings in every parameter that takes one,
// each sent as acme:<kind>:... and as mcp:<kind>:...: fetch's reference,
// save_asset's content and references (a list of strings), run_script's args
// values; the portal's routes take a reference in the path and in a JSON
// body. search takes query as a string. The branding and system routes are
// GETs.

const scheme2776 = "acme"

// canonicalRef2776 matches a reference written under mcp:.
var canonicalRef2776 = regexp.MustCompile(`(^|[^A-Za-z0-9_./-])mcp:(asset|prompt|collection|knowledge_page|connection|insight|resource|memory|script|call|session|dataset|column|glossary_term|glossary_node|tag|domain|data_product|schema_document|form|endpoint|site):`)

// lastRead2776 is the read stamp a fetch moves.
var lastRead2776 = regexp.MustCompile(`"last_read_at":"[^"]*",?`)

// needsScheme2776 fails a criterion run on a stack without the scheme.
func needsScheme2776(t *testing.T) {
	t.Helper()
	status, branding := rest2559(t, keyAdmin2604, http.MethodGet, "/api/v1/admin/public/branding", nil)
	if status != http.StatusOK || branding["reference_scheme"] != scheme2776 {
		t.Fatalf("this criterion needs server.reference_scheme: %s (ACCEPTANCE_ENV=refscheme); the stack reports %v", scheme2776, branding["reference_scheme"])
	}
}

func asText2776(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Criteria 1 and 7: search writes every reference under acme:, and neither
// tools/list nor platform_info names a reference under mcp:.
func TestIssue2776_EveryReferenceHandedOutIsUnderTheScheme(t *testing.T) {
	needsScheme2776(t)
	c := connect(t)
	saveAsset2502(t, c, "acc-2776-search-"+stamp2502(), "text/markdown", "# Reference scheme 2776\n", nil)
	got := c.call("search", map[string]any{"intent": "platform pages sites reference scheme 2776", "purpose": "Checking which scheme the references search hands out use (acceptance 2776)."})
	text := asText2776(got)
	refs := regexp.MustCompile(`"reference":"([^"]+)"`).FindAllStringSubmatch(text, -1)
	if len(refs) == 0 {
		t.Fatalf("search returned no references: %s", text)
	}
	kinds := map[string]bool{}
	for _, m := range refs {
		if !strings.HasPrefix(m[1], scheme2776+":") {
			t.Errorf("search returned %s, not under %s:", m[1], scheme2776)
		}
		kinds[strings.SplitN(m[1], ":", 3)[1]] = true
	}
	t.Logf("search wrote references of kinds %v", kinds)
	if loc := canonicalRef2776.FindStringIndex(text); loc != nil {
		t.Errorf("search names a reference under mcp:: %s", text[max(0, loc[0]-40):min(len(text), loc[1]+40)])
	}

	tools, err := c.session.ListTools(c.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := asText2776(tools)
	if loc := canonicalRef2776.FindStringIndex(listed); loc != nil {
		t.Errorf("tools/list names a reference under mcp:: %s", listed[max(0, loc[0]-60):min(len(listed), loc[1]+60)])
	}
	if !strings.Contains(listed, scheme2776+":asset:") {
		t.Errorf("tools/list names no reference under %s:", scheme2776)
	}
	info := asText2776(c.call("platform_info", map[string]any{}))
	if loc := canonicalRef2776.FindStringIndex(info); loc != nil {
		t.Errorf("platform_info names a reference under mcp:: %s", info[max(0, loc[0]-60):min(len(info), loc[1]+60)])
	}
}

// Criteria 2 and 3: fetch with acme: and with mcp: returns the same document
// for an asset, a resource, a knowledge page and a connection, each stored as
// mcp: before it was fetched.
func TestIssue2776_FetchReadsBothForms(t *testing.T) {
	needsScheme2776(t)
	c := connect(t)
	asset := saveAsset2502(t, c, "acc-2776-fetch-"+stamp2502(), "text/markdown", "# Fetched 2776\nbody-2776\n", nil)
	resourceID, _ := personalResource2616(t, c, "resource-body-2776")
	db := devDB(t)
	var stored string
	if err := db.QueryRowContext(t.Context(), `SELECT 'mcp:asset:' || id FROM portal_assets WHERE id = $1`, asset).Scan(&stored); err != nil {
		t.Fatalf("reading the stored asset: %v", err)
	}
	for kind, id := range map[string]string{
		"asset": asset, "resource": resourceID, "knowledge_page": "platform-pages-and-sites",
	} {
		var bodies []string
		for _, scheme := range []string{scheme2776, "mcp"} {
			ref := scheme + ":" + kind + ":" + id
			out := c.call("fetch", map[string]any{"reference": ref, "purpose": "acceptance 2776"})
			delete(out, "session_id")
			// When the resource was last read moves with the first fetch.
			bodies = append(bodies, lastRead2776.ReplaceAllString(asText2776(out), ""))
		}
		if bodies[0] != bodies[1] {
			t.Errorf("%s: fetch answers differently for the two forms:\n%s\n%s", kind, bodies[0], bodies[1])
		}
		if strings.Contains(bodies[0], `"error"`) || len(bodies[0]) < 40 {
			t.Errorf("%s: fetch did not return the document: %s", kind, bodies[0])
		}
	}
	conns := asText2776(c.call("list_connections", map[string]any{}))
	m := regexp.MustCompile(scheme2776 + `:connection:\([^)]+\)`).FindString(conns)
	if m == "" {
		t.Fatalf("list_connections wrote no connection reference under %s: %s", scheme2776, conns)
	}
	a := c.call("fetch", map[string]any{"reference": m, "purpose": "acceptance 2776"})
	b := c.call("fetch", map[string]any{"reference": "mcp" + strings.TrimPrefix(m, scheme2776), "purpose": "acceptance 2776"})
	if asText2776(a) != asText2776(b) {
		t.Errorf("connection: fetch answers differently for the two forms")
	}
}

// Criteria 4 and 5: a body citing acme:resource:<id> and one citing
// mcp:resource:<id>, each declaring it in the other form, are saved and
// declared alike, an undeclared one is reported alike, and a link to an asset
// in either form lists both linking assets as one target's Linked from.
func TestIssue2776_BodiesAndLinksAreOneEntityUnderBothForms(t *testing.T) {
	needsScheme2776(t)
	c := connect(t)
	resourceID, _ := personalResource2616(t, c, "declared-2776")
	_, row := c.rest(http.MethodGet, "/api/v1/resources/"+resourceID, http.NoBody)
	shown, _ := row["uri"].(string)
	rest, ok := strings.CutPrefix(shown, scheme2776+"://")
	if !ok {
		t.Fatalf("the portal shows the resource's address as %q, not under %s://", shown, scheme2776)
	}
	forms := map[string]string{scheme2776: scheme2776 + "://" + rest, "mcp": "mcp://" + rest}
	s := stamp2502()
	results := map[string]string{}
	for _, pair := range [][2]string{{scheme2776, "mcp"}, {"mcp", scheme2776}} {
		body := "<p><img src=\"" + forms[pair[0]] + "\"></p>"
		out := c.call("save_asset", map[string]any{"name": "acc-2776-declared-" + pair[0] + "-" + s, "content_type": "text/html", "content": body,
			"references": []any{forms[pair[1]]}})
		id, _ := out["asset_id"].(string)
		t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/assets/"+id, nil) })
		if out["undeclared_references"] != nil {
			t.Errorf("%s body declared as %s reports an undeclared reference: %v", pair[0], pair[1], out)
		}
		results[pair[0]] = fmt.Sprint(out["references_declared"])
		status, content := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id+"/content", nil)
		if status != http.StatusOK {
			t.Errorf("%s body: the portal could not serve it: HTTP %d %v", pair[0], status, content)
		}
	}
	if results[scheme2776] != results["mcp"] {
		t.Errorf("the two forms declare differently: %v", results)
	}
	undeclared := map[string]string{}
	for _, scheme := range []string{scheme2776, "mcp"} {
		res, text, err := c.callRaw("save_asset", map[string]any{"name": "acc-2776-undeclared-" + scheme + "-" + s, "content_type": "text/html",
			"content": "<p><img src=\"" + forms[scheme] + "\"></p>"})
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(text), &out)
		if id, _ := out["asset_id"].(string); id != "" {
			t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/assets/"+id, nil) })
		}
		undeclared[scheme] = fmt.Sprintf("error=%v undeclared=%v", res.IsError, out["undeclared_references"] != nil || strings.Contains(text, "undeclared"))
	}
	if undeclared[scheme2776] != undeclared["mcp"] || !strings.Contains(undeclared["mcp"], "undeclared=true") {
		t.Errorf("an undeclared reference is treated differently under the two forms, or not reported: %v", undeclared)
	}

	target := saveAsset2502(t, c, "acc-2776-target-"+s, "text/markdown", "# Target\n", nil)
	var linkers []string
	for _, scheme := range []string{scheme2776, "mcp"} {
		linkers = append(linkers, saveAsset2502(t, c, "acc-2776-linker-"+scheme+"-"+s, "text/markdown", "[the target]("+scheme+":asset:"+target+")\n", nil))
	}
	status, links := links2432(t, c, target)
	got := ids2432(links, "linked_from")
	if status != http.StatusOK || len(got) != 2 || !strings.Contains(strings.Join(got, ","), linkers[0]) || !strings.Contains(strings.Join(got, ","), linkers[1]) {
		t.Errorf("Linked from lists %v (HTTP %d); want both linking assets %v", got, status, linkers)
	}
}

// script2776 reads one resource named by reference, and the same resource by
// a reference it assembles under the scheme at run time.
const script2776 = `def main():
    """Reads a resource by reference, and by one assembled at run time."""
    a = platform.read_resource(run.params["ref"])
    b = platform.read_resource(run.params["scheme"] + ":resource:" + run.params["id"])
    print("same %s %s %d" % (a.sha256 == b.sha256, a.sha256, b.size))
`

// Criterion 6: a script reading acme:resource:<id> and mcp:resource:<id>
// reads the same bytes, including a reference it assembles at run time.
func TestIssue2776_AScriptReadsBothForms(t *testing.T) {
	needsScheme2776(t)
	owner := connectAs(t, keyOwner2616)
	id, _ := personalResource2616(t, owner, "script-read-2776")
	name := "acc2776-read-" + unique1579()
	params := []any{
		map[string]any{"name": "ref", "type": "string", "required": true, "description": "The resource."},
		map[string]any{"name": "scheme", "type": "string", "required": true, "description": "The scheme to assemble a reference under."},
		map[string]any{"name": "id", "type": "string", "required": true, "description": "The resource id."},
	}
	owner.saveScript(map[string]any{"command": "create", "name": name, "description": "Acceptance #2776: reads a resource under both schemes.", "source": script2776, "params": params},
		map[string]any{"ref": "mcp:resource:" + id, "scheme": scheme2776, "id": id})
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	for _, ref := range []string{scheme2776 + ":resource:" + id, "mcp:resource:" + id} {
		run := owner.call("run_script", map[string]any{"name": name, "args": map[string]any{"ref": ref, "scheme": scheme2776, "id": id}, "wait_seconds": 120})
		if run["status"] != "succeeded" || !strings.Contains(fmt.Sprint(run["log"]), "same True") {
			t.Errorf("reading %s: %v", ref, run)
		}
	}
}

// Criterion 10: the portal reads the scheme from the branding and system
// routes, gets references under it, and a reference in either form in a path
// resolves.
func TestIssue2776_ThePortalWritesAndReadsTheScheme(t *testing.T) {
	needsScheme2776(t)
	status, info := rest2559(t, keyAdmin2604, http.MethodGet, "/api/v1/admin/system/info", nil)
	if status != http.StatusOK || info["reference_scheme"] != scheme2776 {
		t.Errorf("Admin > Settings reads %v from the system route", info["reference_scheme"])
	}
	c := connect(t)
	target := saveAsset2502(t, c, "acc-2776-portal-target-"+stamp2502(), "text/markdown", "# Target\n", nil)
	linker := saveAsset2502(t, c, "acc-2776-portal-linker-"+stamp2502(), "text/markdown", "[t](mcp:asset:"+target+")\n", nil)
	status, content := c.rest(http.MethodGet, "/api/v1/portal/assets/"+linker+"/content", nil)
	raw := asText2776(content)
	if status != http.StatusOK || canonicalRef2776.MatchString(raw) {
		t.Errorf("the portal reads the body with a reference under mcp: (HTTP %d): %s", status, raw)
	}
	for _, ref := range []string{scheme2776 + ":knowledge_page:platform-pages-and-sites", "mcp:knowledge_page:platform-pages-and-sites"} {
		got := c.call("fetch", map[string]any{"reference": ref, "purpose": "acceptance 2776"})
		if !strings.Contains(asText2776(got), scheme2776+":knowledge_page:") {
			t.Errorf("the built-in page as served names no reference under %s: (%s)", scheme2776, ref)
		}
	}
}

// Criterion 9: http, Acme and a:b each refuse startup naming
// server.reference_scheme.
func TestIssue2776_AnUnusableSchemeRefusesStartup(t *testing.T) {
	needsScheme2776(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "platform")
	build := exec.Command("go", "build", "-o", bin, "./cmd/mcp-data-platform")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the platform: %v\n%s", err, out)
	}
	for _, scheme := range []string{"http", "Acme", "a:b"} {
		cfg := filepath.Join(t.TempDir(), "platform.yaml")
		if err := os.WriteFile(cfg, []byte("server:\n  name: acc-2776\n  transport: stdio\n  reference_scheme: \""+scheme+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		run := exec.Command(bin, "--config", cfg)
		run.Stdin = strings.NewReader("")
		out, err := run.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "server.reference_scheme") {
			t.Errorf("reference_scheme %q: exit %v, want a refusal naming server.reference_scheme:\n%s", scheme, err, out)
		}
	}
}
