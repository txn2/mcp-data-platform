//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Issue #2432: an asset links another by writing mcp:asset:<id> as a link
// target, in an HTML or JSX <a href> or a Markdown [text](...) link. A link
// grants nothing: it is not a declared reference, saving it checks nothing,
// and following it opens the target's page, whose own access check decides
// what the reader sees (the target if they can open it, the request-access
// page otherwise). The target lists the assets linking to it ("Linked from"),
// only those the reader can open. A managed resource used as a link target is
// still refused. The click itself, from a framed HTML asset and from a
// Markdown asset, both outcomes, and a forged frame message ignored, is held
// in a browser by ui/e2e/interactive/asset-marks-links.spec.ts.
//
// Wire forms: save_asset's `name`, `content` and `content_type` are typed
// string (content also object or array for a JSON type, not one these bodies
// use) and sent as strings; manage_asset share's `access_mode` and
// `expires_in` are strings. The links route takes no body.

// links2432 reads an asset's links route as c.
func links2432(t *testing.T, c *client, id string) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodGet, "/api/v1/portal/assets/"+id+"/links", nil)
}

// ids2432 is the ids a links response lists under key.
func ids2432(resp map[string]any, key string) []string {
	var out []string
	rows, _ := resp[key].([]any)
	for _, r := range rows {
		switch v := r.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			out = append(out, fmt.Sprint(v["id"]))
		}
	}
	return out
}

// linkFixture2432 is three assets the admin owns: A links B and C, the
// analyst can open A and B and not C, and the engineer can open B alone.
type linkFixture2432 struct {
	a, b, c           string
	aName, cName      string
	owner             *client
	analyst, engineer *client
}

func newLinkFixture2432(t *testing.T, contentType, link func(id string) string, body func(links ...string) string) linkFixture2432 {
	t.Helper()
	_ = contentType
	f := linkFixture2432{owner: connect(t), analyst: connectAs(t, analystKey), engineer: connectAs(t, engineerKey)}
	s := stamp2502()
	f.cName = "acc-2432-locked-" + s
	f.b = saveAsset2502(t, f.owner, "acc-2432-target-"+s, "text/markdown", "# Regional report\n", nil)
	f.c = saveAsset2502(t, f.owner, f.cName, "text/markdown", "# Locked figures\n", nil)
	f.aName = "acc-2432-index-" + s
	f.a = saveAsset2502(t, f.owner, f.aName, "text/markdown", body(link(f.b), link(f.c)), nil)
	share2502(t, f.owner, f.a, analystEmail, "viewer")
	share2502(t, f.owner, f.b, analystEmail, "viewer")
	share2502(t, f.owner, f.b, engineerEmail, "viewer")
	return f
}

func markdownFixture2432(t *testing.T) linkFixture2432 {
	t.Helper()
	return newLinkFixture2432(t, nil,
		func(id string) string { return "mcp:asset:" + id },
		func(links ...string) string {
			return "# Index\n\nFor regional numbers see [the regional report](" + links[0] +
				"), and [the locked figures](" + links[1] + ").\n"
		})
}

// TestIssue2432_ALinkToAnotherAssetIsSavedInBothForms: save_asset with an
// HTML body linking <a href="mcp:asset:B"> and a Markdown body linking
// [guide](mcp:asset:B) both succeed, declare no reference, report nothing
// undeclared, and the links route lists B.
func TestIssue2432_ALinkToAnotherAssetIsSavedInBothForms(t *testing.T) {
	c := connect(t)
	s := stamp2502()
	target := saveAsset2502(t, c, "acc-2432-guide-"+s, "text/markdown", "# Guide\n", nil)
	for form, body := range map[string]struct{ contentType, content string }{
		"html":     {"text/html", `<html><body><p>Start with <a href="mcp:asset:` + target + `">the guide</a>.</p></body></html>`},
		"markdown": {"text/markdown", "# Index\n\nStart with [the guide](mcp:asset:" + target + ").\n"},
	} {
		t.Run(form, func(t *testing.T) {
			res, text, err := c.callRaw("save_asset", map[string]any{
				"name": "acc-2432-link-" + form + "-" + s, "content_type": body.contentType, "content": body.content,
			})
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError {
				t.Fatalf("save_asset refused a link to another asset: %s", text)
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatalf("save_asset answered with no JSON: %s", text)
			}
			id, _ := out["asset_id"].(string)
			t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/assets/"+id, nil) })
			if strings.Contains(text, "undeclared_references") || out["references_declared"] != nil {
				t.Fatalf("the link was treated as a reference: %s", text)
			}
			status, links := links2432(t, c, id)
			if status != http.StatusOK || strings.Join(ids2432(links, "links"), ",") != target {
				t.Fatalf("the links route answered %d %v; want links [%s]", status, links, target)
			}
		})
	}
}

// TestIssue2432_AResourceLinkIsStillRefused: the same calls with a managed
// resource as the link target are refused, and nothing is written.
func TestIssue2432_AResourceLinkIsStillRefused(t *testing.T) {
	c := connect(t)
	resourceURI, _ := dataFiles2127(t, c)
	s := stamp2502()
	for form, body := range map[string]struct{ contentType, content string }{
		"html":     {"text/html", `<p><a href="` + resourceURI + `">the data</a></p>`},
		"markdown": {"text/markdown", "[the data](" + resourceURI + ")\n"},
	} {
		name := "acc-2432-resource-" + form + "-" + s
		refused, text := save1875(t, c, name, body.contentType, body.content)
		if !refused || !strings.Contains(text, "cannot be a link target") || !strings.Contains(text, "mcp:asset:<id>") {
			t.Errorf("%s: a link to a managed resource was not refused with the asset-link way out: %s", form, text)
		}
		if n := assetsNamed1875(t, c, name); n != 0 {
			t.Errorf("%s: the refused save stored %d asset(s)", form, n)
		}
	}
}

// TestIssue2432_AReaderOpensWhatTheyCanAndAsksForTheRest: a reader who opens
// A and B finds B among A's links and opens it; for C, which they cannot
// open, they get the request-access answer, a request opens pending, no share
// exists until the owner approves, and C's content and links routes still
// refuse them. Nothing about C but its id reaches them.
func TestIssue2432_AReaderOpensWhatTheyCanAndAsksForTheRest(t *testing.T) {
	f := markdownFixture2432(t)
	status, links := links2432(t, f.analyst, f.a)
	if status != http.StatusOK {
		t.Fatalf("the reader's links route on A: HTTP %d %v", status, links)
	}
	got := strings.Join(ids2432(links, "links"), ",")
	if !strings.Contains(got, f.b) || !strings.Contains(got, f.c) {
		t.Fatalf("A's links read %s; want both linked ids", got)
	}
	raw, _ := json.Marshal(links)
	if strings.Contains(string(raw), f.cName) || strings.Contains(string(raw), "Locked figures") {
		t.Fatalf("A's links gave the reader C's name or content: %s", raw)
	}
	if status, _ := portalAsset2502(t, f.analyst, f.b); status != http.StatusOK {
		t.Fatalf("the reader could not open B, which is shared with them: HTTP %d", status)
	}

	base := "/api/v1/portal/assets/" + f.c
	if status, body := f.analyst.rest(http.MethodGet, base, nil); status == http.StatusOK {
		t.Fatalf("the reader opened C, which nobody shared with them: %v", body)
	}
	status, page := f.analyst.rest(http.MethodGet, base+"/access-request", nil)
	if status != http.StatusOK || page["has_access"] != false {
		t.Fatalf("C's request-access answer: HTTP %d %v", status, page)
	}
	if strings.Contains(fmt.Sprint(page), f.cName) {
		t.Errorf("the request-access answer named C: %v", page)
	}
	status, req := f.analyst.rest(http.MethodPost, base+"/access-requests", jsonBody(t, map[string]any{"message": "acc-2432 from the index link"}))
	if (status != http.StatusCreated && status != http.StatusOK) || req["status"] != "pending" {
		t.Fatalf("the request for C: HTTP %d %v", status, req)
	}
	if status, shares := f.owner.rest(http.MethodGet, base+"/shares", nil); status == http.StatusOK &&
		strings.Contains(fmt.Sprint(shares), analystEmail) {
		t.Fatalf("a share on C exists before the owner approved: %v", shares)
	}
	for _, path := range []string{base + "/content", base + "/links"} {
		if status, _ := f.analyst.rest(http.MethodGet, path, nil); status == http.StatusOK {
			t.Errorf("after following the link the reader read %s", path)
		}
	}
}

// TestIssue2432_LinkedFromListsOnlyWhatTheReaderCanOpen: a reader who opens A
// and B sees A under B's Linked from; a reader who opens B and not A sees no
// entry and no count.
func TestIssue2432_LinkedFromListsOnlyWhatTheReaderCanOpen(t *testing.T) {
	f := markdownFixture2432(t)
	status, links := links2432(t, f.analyst, f.b)
	if status != http.StatusOK || strings.Join(ids2432(links, "linked_from"), ",") != f.a {
		t.Fatalf("B's Linked from for a reader of A reads %d %v; want [%s]", status, links, f.a)
	}
	status, links = links2432(t, f.engineer, f.b)
	if status != http.StatusOK {
		t.Fatalf("B's links for the engineer: HTTP %d", status)
	}
	raw, _ := json.Marshal(links)
	if n := len(ids2432(links, "linked_from")); n != 0 || strings.Contains(string(raw), f.a) || strings.Contains(string(raw), f.aName) {
		t.Fatalf("a reader who cannot open A sees it under B's Linked from (%d entries): %s", n, raw)
	}
	for key := range links {
		if strings.Contains(key, "count") || strings.Contains(key, "total") {
			t.Errorf("the links answer carries %q, a count of what the reader cannot open", key)
		}
	}
}

// TestIssue2432_APublicShareLeavesTheLinkToSignIn: a publicly shared A keeps
// its link as written, not rewritten to a URL serving B's bytes, and B is not
// served to an anonymous viewer: its portal route asks for sign-in.
func TestIssue2432_APublicShareLeavesTheLinkToSignIn(t *testing.T) {
	f := markdownFixture2432(t)
	share := f.owner.call("manage_asset", map[string]any{
		"action": "share", "asset_id": f.a, "access_mode": "public", "expires_in": "1h",
	})
	shareURL, _ := share["share_url"].(string)
	if shareURL == "" {
		t.Fatalf("share returned no URL: %v", share)
	}
	status, content := anonymousGet(t, shareURL+"/content")
	if status != http.StatusOK {
		t.Fatalf("the public share's content answered %d", status)
	}
	if !strings.Contains(content, "mcp:asset:"+f.b) || strings.Contains(content, "/portal/refs/") {
		t.Fatalf("the public share rewrote the link into a served URL:\n%s", content)
	}
	if status, _ := anonymousGet(t, strings.TrimSuffix(f.owner.base, "/")+"/api/v1/portal/assets/"+f.b); status != http.StatusUnauthorized {
		t.Fatalf("B's portal route answered an anonymous viewer %d, want 401", status)
	}
}
