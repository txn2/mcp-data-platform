//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// Issue #2021: the asset reference cap is a deployment setting.
//
// The dev stack sets portal.asset_refs.max to 30, ten past the default, so
// every criterion below fails on a platform that still reads the default:
// the tool schemas, the refusal, the reference panel and the reference
// route's rate limit are each held to 30.
//
// Wire forms: save_asset's name, content, content_type and description and
// manage_asset's action and asset_id are typed strings in their schemas, and
// references is a typed array of strings on both tools; each admits exactly
// one JSON form and each is sent below as a literal tools/call parameter of
// that form. The reference panel and the reference route take no body.

// configuredRefs2021 is the dev stack's portal.asset_refs.max.
const configuredRefs2021 = 30

// TestIssue2021_TheAssetToolsAdvertiseTheConfiguredCap is criterion 3:
// save_asset and manage_asset state the configured number in
// references.maxItems and in the field's description.
func TestIssue2021_TheAssetToolsAdvertiseTheConfiguredCap(t *testing.T) {
	c := connect(t)
	seen := map[string]bool{}
	for _, tool := range c.tools() {
		if tool.Name != "save_asset" && tool.Name != "manage_asset" {
			continue
		}
		seen[tool.Name] = true
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]struct {
				MaxItems    int    `json:"maxItems"`
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		refs := schema.Properties["references"]
		if refs.MaxItems != configuredRefs2021 {
			t.Errorf("%s advertises references.maxItems %d, want %d", tool.Name, refs.MaxItems, configuredRefs2021)
		}
		if want := fmt.Sprintf("At most %d references per asset", configuredRefs2021); !strings.Contains(refs.Description, want) {
			t.Errorf("%s's references description does not state the cap (%q): %s", tool.Name, want, refs.Description)
		}
	}
	if !seen["save_asset"] || !seen["manage_asset"] {
		t.Fatalf("tools/list did not carry both asset tools: %v", seen)
	}
}

// TestIssue2021_ADeclarationPastTheConfiguredCapIsRefusedNamingIt is
// criterion 2, on both tools that declare references.
func TestIssue2021_ADeclarationPastTheConfiguredCapIsRefusedNamingIt(t *testing.T) {
	c := connect(t)
	uris := createRefFiles2021(t, c, 1)
	over := make([]string, configuredRefs2021+1)
	for i := range over {
		over[i] = uris[0]
	}

	res, text, err := c.callRaw("save_asset", map[string]any{
		"name":         "acceptance-2021-over-" + unique1787(),
		"content":      `<img src="` + uris[0] + `">`,
		"content_type": "text/html",
		"description":  "Acceptance #2021: one reference past the cap.",
		"references":   over,
	})
	assertRefusedNaming2021(t, "save_asset", res != nil && res.IsError, text, err)

	id := saveWithRefs2021(t, c, uris)
	res, text, err = c.callRaw("manage_asset", map[string]any{
		"action":     "update",
		"asset_id":   id,
		"references": over,
	})
	assertRefusedNaming2021(t, "manage_asset update", res != nil && res.IsError, text, err)
}

// TestIssue2021_ThePanelAndTheRouteHonorTheConfiguredCap is criteria 2 and 4:
// an asset declaring the configured number (past the default) is saved, its
// reference panel reports the configured cap, and a reader's page load
// fetches every one of them without a 429.
func TestIssue2021_ThePanelAndTheRouteHonorTheConfiguredCap(t *testing.T) {
	c := connect(t)
	id := saveWithRefs2021(t, c, createRefFiles2021(t, c, configuredRefs2021))

	status, out := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id+"/references", nil)
	if status != http.StatusOK {
		t.Fatalf("listing references: %d %v", status, out)
	}
	if got, _ := out["max"].(float64); int(got) != configuredRefs2021 {
		t.Errorf("the reference panel reports a cap of %v, want %d", out["max"], configuredRefs2021)
	}
	listed := listRefs1584(t, c, id)
	if len(listed) != configuredRefs2021 {
		t.Fatalf("the asset lists %d references, want %d", len(listed), configuredRefs2021)
	}
	statuses := make([]int, len(listed))
	var wg sync.WaitGroup
	for i, ref := range listed {
		url, _ := ref["content_url"].(string)
		if strings.HasPrefix(url, "/") {
			url = baseURL() + url
		}
		wg.Go(func() { statuses[i] = anonymousGet1791(t, url) })
	}
	wg.Wait()
	for i, s := range statuses {
		if s != http.StatusOK {
			t.Errorf("reference %d of %d answered %d, want 200", i+1, len(statuses), s)
		}
	}
}

// assertRefusedNaming2021 holds a refusal to stating the configured cap.
func assertRefusedNaming2021(t *testing.T, call string, isError bool, text string, err error) {
	t.Helper()
	if err == nil && !isError {
		t.Fatalf("%s accepted %d references past a cap of %d: %s", call, configuredRefs2021+1, configuredRefs2021, text)
	}
	msg := text
	if err != nil {
		msg += " " + err.Error()
	}
	if !strings.Contains(msg, fmt.Sprint(configuredRefs2021)) {
		t.Errorf("%s's refusal does not state the cap %d: %s", call, configuredRefs2021, msg)
	}
}

// createRefFiles2021 files n small images and returns their URIs.
func createRefFiles2021(t *testing.T, c *client, n int) []string {
	t.Helper()
	run := unique1787()
	uris := make([]string, 0, n)
	for i := range n {
		name := fmt.Sprintf("acceptance-2021-%s-%02d", run, i)
		out := c.call("manage_resource", map[string]any{
			"action":       "create",
			"filename":     name + ".svg",
			"display_name": name,
			"path":         "acceptance-2021",
			"description":  "Acceptance #2021: one of the files a document declares.",
			"content":      swatchSVG1791,
			"content_type": "image/svg+xml",
		})
		rid, _ := out["resource_id"].(string)
		uri, _ := out["uri"].(string)
		if rid == "" || uri == "" {
			t.Fatalf("manage_resource create returned no resource: %v", out)
		}
		t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+rid, http.NoBody) })
		uris = append(uris, uri)
	}
	return uris
}

// saveWithRefs2021 saves an HTML asset loading every file in uris and declaring
// them, and returns its id.
func saveWithRefs2021(t *testing.T, c *client, uris []string) string {
	t.Helper()
	var imgs strings.Builder
	for _, u := range uris {
		imgs.WriteString(`<img src="` + u + `" width="40" height="40">`)
	}
	out := c.call("save_asset", map[string]any{
		"name":         "acceptance-2021-" + unique1787(),
		"content":      "<!DOCTYPE html><html><body>" + imgs.String() + "</body></html>",
		"content_type": "text/html",
		"description":  "Acceptance #2021: a document declaring the configured cap's worth of files.",
		"references":   uris,
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
	})
	return id
}
