//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2177: the platform renders a form from its spec, and an
// HTML asset embeds forms by declaring mcp:form:<endpoint>. One asset
// embedding two forms is shared publicly; an anonymous browser opens the
// share with ?t=<token>, finds both forms rendered in frames of their own
// inside the asset's frame (whose sandbox is unchanged), submits each and is
// shown its confirmation; each submission lands in its own form's source with
// the token form_spec read from req["query"]. An authenticated form embedded
// in an asset is told who the signed-in reader is by the viewer token the
// asset is served with, and only its owner may embed it. Screenshots of the
// form page in light and dark at desktop and phone width are kept under
// build/2177/.
//
// Wire forms: save_asset's content and content_type as strings and
// references as a list of strings; manage_asset share's access_mode and
// expires_in as strings; endpoint_set as in #2178; the browser's own
// navigations, the form page's GET and its JSON POST of the fields with
// X-Bot-Check absent; X-Form-Viewer as a header on a GET and a JSON POST with
// Origin: null.

// formsAsset is an HTML document embedding each form at its reference.
func formsAsset(forms ...string) string {
	var b strings.Builder
	b.WriteString("<!doctype html><html><body><h1>Tell us</h1>")
	for _, f := range forms {
		fmt.Fprintf(&b, `<section><h2>%s</h2><iframe src="mcp:form:%s" style="width:100%%;height:520px;border:0"></iframe></section>`, f, f)
	}
	b.WriteString("</body></html>")
	return b.String()
}

// saveFormsAsset saves an HTML asset declaring each form as c, and returns
// its id.
func saveFormsAsset(t *testing.T, c *client, forms ...string) string {
	t.Helper()
	refs := make([]any, 0, len(forms))
	for _, f := range forms {
		refs = append(refs, "mcp:form:"+f)
	}
	saved := c.call("save_asset", map[string]any{
		"name": "Acceptance forms " + time.Now().Format("150405.000"), "content": formsAsset(forms...),
		"content_type": "text/html", "references": refs, "purpose": formsPurpose,
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id, "purpose": formsPurpose})
	})
	return id
}

// shareFormsAsset shares an asset publicly and returns the share page's path.
func shareFormsAsset(t *testing.T, c *client, assetID string) string {
	t.Helper()
	share := c.call("manage_asset", map[string]any{
		"action": "share", "asset_id": assetID, "access_mode": "public", "expires_in": "1h", "purpose": formsPurpose,
	})
	shareURL, _ := share["share_url"].(string)
	i := strings.Index(shareURL, "/portal/view/")
	if i < 0 {
		t.Fatalf("the share carries no viewer link: %v", share)
	}
	return shareURL[i:]
}

// formsBrowser runs one case of ui/e2e/forms against the stack.
func formsBrowser(t *testing.T, criterion string, env ...string) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/forms/playwright.config.ts", "--grep", "criterion "+criterion+":")
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(append(os.Environ(), "FORMS_BASE_URL="+baseURL(), "FORMS_CONFIRMATION=Thanks, we have it."), env...)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright, criterion %s:\n%s", criterion, out)
	if err != nil {
		t.Fatalf("the browser case %s failed: %v", criterion, err)
	}
}

// TestIssue2177_ASharedAssetsTwoFormsAreSubmittedByAnAnonymousBrowser is
// criteria 1 to 4: two forms in one asset render and store into their own
// sources, an anonymous reader of a public share submits them and sees the
// confirmation, the asset's sandbox is unchanged, and ?t= reaches form_spec.
func TestIssue2177_ASharedAssetsTwoFormsAreSubmittedByAnAnonymousBrowser(t *testing.T) {
	admin := connect(t)
	first, firstTable := bindForm(t, admin, "acc-2177-first", formFields, map[string]any{"auth_mode": "public"})
	second, secondTable := bindForm(t, admin, "acc-2177-second", formFields, map[string]any{"auth_mode": "public"})
	assetID := saveFormsAsset(t, admin, first, second)

	content := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/assets/"+assetID+"/content", bearer(devAPIKey()), "")
	status, body := content.status, content.body
	if status != http.StatusOK || !strings.Contains(body, "/forms/"+first) || !strings.Contains(body, "/forms/"+second) ||
		strings.Contains(body, "mcp:form:") {
		t.Fatalf("the asset's content does not name both form pages: %d %s", status, body)
	}

	token := "tok-" + endpointName("t")
	share := shareFormsAsset(t, admin, assetID)
	formsBrowser(t, "share", "FORMS_SHARE_PATH="+share+"?t="+url.QueryEscape(token), "FORMS_COUNT=2", "FORMS_TOKEN="+token)

	for _, table := range []string{firstTable, secondTable} {
		rows := formRows(t, admin, table, 1)
		if len(rows) != 1 {
			t.Fatalf("%s holds %d rows, want the one submission its form took", table, len(rows))
		}
		if v := rows[0].values(); v["t"] != token || !strings.HasPrefix(fmt.Sprint(v["email"]), "anon") {
			t.Fatalf("%s's row holds %v; want the anonymous submission with t=%s", table, v, token)
		}
	}
}

// TestIssue2177_AnEmbeddedSignedInFormKnowsItsReader: the asset served to a
// signed-in reader hands an authenticated form a viewer token, which the form
// takes as the caller from an opaque origin, and a reader who does not own
// the form may not embed it.
func TestIssue2177_AnEmbeddedSignedInFormKnowsItsReader(t *testing.T) {
	admin := connect(t)
	form, table := bindForm(t, admin, "acc-2177-signed", formFields, map[string]any{"auth_mode": "authenticated", "callers": []string{"*"}})

	peer := connectAs(t, devPeerAPIKey)
	if res, text, err := peer.callRaw("save_asset", map[string]any{
		"name": "Acceptance 2177 peer", "content": formsAsset(form), "content_type": "text/html",
		"references": []any{"mcp:form:" + form}, "purpose": formsPurpose,
	}); err != nil || !res.IsError || !strings.Contains(text, "only its owner or an administrator") {
		t.Fatalf("a reader who does not own the form embedded it: %v %s", err, text)
	}

	assetID := saveFormsAsset(t, admin, form)
	content := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/assets/"+assetID+"/content", bearer(devAPIKey()), "")
	status, body := content.status, content.body
	_, after, found := strings.Cut(body, "/forms/"+form+"?")
	query, _ := url.ParseQuery(after[:max(strings.IndexAny(after, `"'`), 0)])
	viewer := query.Get("form_viewer")
	if status != http.StatusOK || !found || viewer == "" {
		t.Fatalf("the signed-in reader's content hands the form no viewer token: %d %s", status, body)
	}

	opaque := http.Header{"Origin": {"null"}, "X-Form-Viewer": {viewer}}
	shown := showForm(t, admin.base, form, "", opaque)
	shown.waitToFill()
	status2, out := submitForm(t, admin.base, form, shown.fields(map[string]string{"email": "reader@example.com"}), true, opaque)
	if status2 != http.StatusOK {
		t.Fatalf("the embedded form's submission with the viewer token: %d %v", status2, out)
	}
	rows := formRows(t, admin, table, 1)
	sub, _ := rows[0].payload["submitter"].(map[string]any)
	if sub["email"] != "admin@example.com" {
		t.Fatalf("the stored submitter is %v, want the signed-in reader", sub)
	}
	if status, _ := submitForm(t, admin.base, form, shown.fields(map[string]string{"email": "x@example.com"}), true,
		http.Header{"Origin": {"null"}}); status != http.StatusUnauthorized {
		t.Fatalf("the form without the viewer token: want 401, got %d", status)
	}
}

// TestIssue2177_TheFormPageInLightAndDarkAtDesktopAndPhoneWidth is criterion
// 5: the screenshots are kept under build/2177/screens.
func TestIssue2177_TheFormPageInLightAndDarkAtDesktopAndPhoneWidth(t *testing.T) {
	admin := connect(t)
	fields := `[
    {"name": "email", "type": "email", "required": True, "label": "Email", "help": "We write back to this address."},
    {"name": "message", "type": "long_text", "label": "Message"},
    {"name": "topic", "type": "single_choice", "label": "Topic", "choices": [{"value": "billing", "label": "Billing"}, {"value": "data", "label": "Data"}]},
    {"name": "updates", "type": "checkbox", "label": "Send me updates"},
]`
	form, _ := bindForm(t, admin, "acc-2177-screens", fields, map[string]any{"auth_mode": "public"})
	root, _ := filepath.Abs("../..")
	dir := filepath.Join(root, "build", "2177", "screens")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	formsBrowser(t, "screens", "FORMS_FORM_PATH=/forms/"+form, "FORMS_SCREENSHOT_DIR="+dir)
	for _, scheme := range []string{"light", "dark"} {
		for _, size := range []string{"desktop", "phone"} {
			path := filepath.Join(dir, "form-"+scheme+"-"+size+".png")
			if info, err := os.Stat(path); err != nil || info.Size() == 0 {
				t.Errorf("no screenshot at %s: %v", path, err)
			}
		}
	}
	page := endpointRequest(t, admin.base, http.MethodGet, "/forms/"+form, nil, "")
	var cfg map[string]any
	_, rest, _ := strings.Cut(page.body, `id="form-config">`)
	cfgText, _, _ := strings.Cut(rest, "</script>")
	if page.status != http.StatusOK || json.Unmarshal([]byte(cfgText), &cfg) != nil || cfg["url"] != "/endpoints/"+form {
		t.Fatalf("the form page: %d %s", page.status, cfgText)
	}
}
