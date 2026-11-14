//go:build integration

package acceptance

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2494: a form's owner, who is not an administrator, previews
// their form with nothing bound, publishes it to signed-in people with no
// administrator involved, and serves it to people who are not signed in only
// at a version an administrator approved through the Approvals queue
// (manage_approval, #2437's version subject).
//
// The owner is the dev stack's ordinary person acme-owner-key, the peer the
// other ordinary person acme-peer-key, the administrator the default key.
//
// Wire forms: manage_script create, update, form_preview and handler_set kind=endpoint and
// manage_approval submit, list, get and decide take their arguments as the
// literal tools/call params (version and preview_version as JSON numbers,
// callers as an array of strings). A preview's spec is a GET carrying the
// viewer token the preview's page signed in X-Form-Viewer, and its submission
// is sent once as an application/json object and once as multipart/form-data
// carrying a file; the endpoint's are sent as application/json.

// issue2494Secret is the sensitive field's value a preview submission
// carries; it is searched for in every sink a preview must never reach.
const issue2494Secret = "acct-2494-preview-sensitive-9f3c"

// issue2494Form is the form at one version: every field type, with help,
// choices and a prefill, a sensitive field and a file field; form_submit
// refuses one address and confirms the rest with the version's own words.
func issue2494Form(version int) string {
	return fmt.Sprintf(`CONFIRM = "Thanks from version %d."

def form_spec(req):
    """Every field type, with help, choices and a prefill."""
    return {
        "fields": [
            {"name": "email", "type": "email", "required": True, "label": "Email", "help": "Where we reach you"},
            {"name": "account", "type": "text", "label": "Account", "sensitive": True},
            {"name": "notes", "type": "long_text", "label": "Notes"},
            {"name": "seats", "type": "number", "label": "Seats"},
            {"name": "start", "type": "date", "label": "Start"},
            {"name": "plan", "type": "single_choice", "label": "Plan", "choices": [{"value": "basic", "label": "Basic"}, {"value": "team", "label": "Team"}]},
            {"name": "channels", "type": "multiple_choice", "label": "Channels", "choices": [{"value": "mail", "label": "Mail"}, {"value": "phone", "label": "Phone"}]},
            {"name": "agree", "type": "checkbox", "label": "I agree to be contacted"},
            {"name": "attachment", "type": "file", "label": "Attachment"},
            {"name": "customer", "type": "hidden"},
        ],
        "prefill": {"customer": "c-2494"},
        "confirmation": CONFIRM,
    }

def form_submit(req, values):
    """Refuses one address; confirms the rest."""
    if values["email"] == "taken@example.com":
        return {"errors": {"email": "this address is already used"}}
    return None

def main():
    """Drafts the spec and both answers."""
    spec = form_spec({"query": {}, "caller": None})
    platform.result({
        "fields": len(spec["fields"]),
        "taken": form_submit({"caller": None}, {"email": "taken@example.com"}),
        "good": form_submit({"caller": None}, {"email": "ann@example.com"}),
    })
`, version)
}

// issue2494Save saves the form as the owner under a fresh name.
func issue2494Save(t *testing.T, owner *client, prefix string) string {
	t.Helper()
	name := endpointName(prefix)
	owner.saveScript(map[string]any{"command": "create", "name": name, "source": issue2494Form(1)}, nil)
	return name
}

// issue2494Edit saves the next version of the form as the owner.
func issue2494Edit(t *testing.T, owner *client, name string, version int) {
	t.Helper()
	owner.saveEdit(map[string]any{"command": "update", "name": name, "source": issue2494Form(version)}, nil)
}

// issue2494Preview opens a preview with manage_script and returns its path.
func issue2494Preview(t *testing.T, c *client, name string, args map[string]any) string {
	t.Helper()
	params := map[string]any{"command": "form_preview", "name": name}
	for k, v := range args {
		params[k] = v
	}
	out := c.call("manage_script", params)
	link, _ := out["preview_url"].(string)
	i := strings.Index(link, "/forms/_preview/")
	if i < 0 {
		t.Fatalf("form_preview returned no preview link: %v", out)
	}
	return link[i:]
}

// issue2494PageViewer opens the preview's page with key's identity and
// returns the status and the viewer token the page was signed.
func issue2494PageViewer(t *testing.T, base, path, key string) (int, string, string) {
	t.Helper()
	got := endpointRequest(t, base, http.MethodGet, path, bearer(key), "")
	_, rest, ok := strings.Cut(got.body, `<script type="application/json" id="form-config">`)
	if !ok {
		return got.status, "", ""
	}
	raw, _, _ := strings.Cut(rest, "</script>")
	var cfg struct {
		Viewer  string `json:"viewer"`
		URL     string `json:"url"`
		Refused string `json:"refused"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("the preview page's configuration: %v %s", err, raw)
	}
	if cfg.Refused != "" {
		return got.status, "", cfg.Refused
	}
	return got.status, cfg.Viewer, cfg.URL
}

// issue2494Spec GETs a form's spec at path with the headers given.
func issue2494Spec(t *testing.T, base, path string, header http.Header) (map[string]any, string) {
	t.Helper()
	got := endpointRequest(t, base, http.MethodGet, path, header, "")
	if got.status != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, got.status, got.body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("the spec at %s: %v %s", path, err, got.body)
	}
	spec, _ := out["spec"].(map[string]any)
	token, _ := out["form_token"].(string)
	if spec == nil || token == "" {
		t.Fatalf("GET %s carried no spec or token: %v", path, out)
	}
	return spec, token
}

// issue2494Post submits fields as JSON at path, after the fill time.
func issue2494Post(t *testing.T, base, path string, header http.Header, fields map[string]any) (int, map[string]any) {
	t.Helper()
	time.Sleep(2200 * time.Millisecond)
	raw, _ := json.Marshal(fields)
	h := http.Header{"Content-Type": {"application/json"}}
	for k, v := range header {
		h[k] = v
	}
	got := endpointRequest(t, base, http.MethodPost, path, h, string(raw))
	var out map[string]any
	_ = json.Unmarshal([]byte(got.body), &out)
	return got.status, out
}

// TestIssue2494_TheOwnerPreviewsAnUnboundForm is criterion 1 through the
// real surface: manage_script form_preview hands the owner a link; its page
// renders for them, its spec is every field the form declares with its help,
// choices and prefill, form_submit's field error and the confirmation come
// back; and the browser case shows it at 375px in both themes.
func TestIssue2494_TheOwnerPreviewsAnUnboundForm(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := issue2494Save(t, owner, "acc-2494-preview")
	if bound := owner.endpointOf(name, ""); bound["bound"] != false {
		t.Fatalf("the form is bound before it was published: %v", bound)
	}
	path := issue2494Preview(t, owner, name, nil)
	status, viewer, api := issue2494PageViewer(t, owner.base, path, devOwnerAPIKey)
	if status != http.StatusOK || viewer == "" || !strings.HasPrefix(api, "/endpoints/_preview/") {
		t.Fatalf("the owner's preview page: %d viewer=%q api=%q", status, viewer, api)
	}
	header := http.Header{"X-Form-Viewer": {viewer}}
	spec, token := issue2494Spec(t, owner.base, api, header)
	fields, _ := spec["fields"].([]any)
	if len(fields) != 10 {
		t.Fatalf("the preview's spec carries %d fields, want 10: %v", len(fields), spec)
	}
	if prefill, _ := spec["prefill"].(map[string]any); prefill["customer"] != "c-2494" {
		t.Fatalf("the prefill: %v", spec["prefill"])
	}
	if status, out := issue2494Post(t, owner.base, api, header, map[string]any{"email": "taken@example.com", "plan": "team", "form_token": token}); status != http.StatusUnprocessableEntity ||
		!strings.Contains(fmt.Sprint(out["errors"]), "already used") {
		t.Fatalf("form_submit's field error in the preview: %d %v", status, out)
	}
	if status, out := issue2494Post(t, owner.base, api, header, map[string]any{"email": "ann@example.com", "plan": "team", "form_token": token}); status != http.StatusOK ||
		out["confirmation"] != "Thanks from version 1." {
		t.Fatalf("the preview's confirmation: %d %v", status, out)
	}

	dir, _ := filepath.Abs(filepath.Join("..", "..", "build", "2494"))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	formsBrowser(t, "preview", "FORMS_PREVIEW_PATH="+issue2494Preview(t, owner, name, nil), "FORMS_OWNER_KEY="+devOwnerAPIKey,
		"FORMS_SCREENSHOT_DIR="+dir, "FORMS_CONFIRMATION=Thanks from version 1.")
}

// TestIssue2494_APreviewSubmitsNothing is criterion 2: a preview submission
// with a sensitive value and a file leaves the form unbound with no source, no
// managed resource named for the file, no notification, no endpoint request
// recorded, and the sensitive value in none of them nor in the replicas' logs,
// as written, base64, hex, URL- and JSON-escaped. The unsaved source is
// previewed here, so a draft is what runs.
func TestIssue2494_APreviewSubmitsNothing(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := issue2494Save(t, owner, "acc-2494-sinks")
	path := issue2494Preview(t, owner, name, map[string]any{"source": issue2494Form(7)})
	_, viewer, api := issue2494PageViewer(t, owner.base, path, devOwnerAPIKey)
	header := http.Header{"X-Form-Viewer": {viewer}}
	_, token := issue2494Spec(t, owner.base, api, header)

	if status, out := issue2494Post(t, owner.base, api, header, map[string]any{
		"email": "ann@example.com", "account": issue2494Secret, "form_token": token,
	}); status != http.StatusOK || out["confirmation"] != "Thanks from version 7." {
		t.Fatalf("the JSON submission: %d %v", status, out)
	}
	fileName := "preview-" + endpointName("f") + ".txt"
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range map[string]string{"email": "bo@example.com", "account": issue2494Secret, "form_token": token} {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("attachment", fileName)
	_, _ = fw.Write([]byte("a file the preview never saves"))
	_ = mw.Close()
	time.Sleep(2200 * time.Millisecond)
	h := http.Header{"Content-Type": {mw.FormDataContentType()}, "X-Form-Viewer": {viewer}}
	if got := endpointRequest(t, owner.base, http.MethodPost, api, h, body.String()); got.status != http.StatusOK {
		t.Fatalf("the multipart submission: %d %s", got.status, got.body)
	}

	if bound := owner.endpointOf(name, ""); bound["bound"] != false || bound["form"] != nil {
		t.Errorf("a preview made a binding or a form source: %v", bound)
	}
	db := issue2257DB(t)
	count := func(sql string, args ...any) int {
		var n int
		if err := db.QueryRowContext(t.Context(), sql, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM resources WHERE filename = $1`, fileName); n != 0 {
		t.Errorf("a preview saved its file as %d managed resources", n)
	}
	if n := count(`SELECT count(*) FROM webhook_sources WHERE name LIKE '%' || $1 || '%'`, name); n != 0 {
		t.Errorf("a preview made %d webhook sources for the form", n)
	}
	if n := count(`SELECT count(*) FROM audit_logs WHERE source = 'endpoint' AND parameters::text LIKE '%_preview-%'`); n != 0 {
		t.Errorf("a preview request was recorded %d times as an endpoint request", n)
	}
	sinks := map[string]string{}
	for sink, sql := range map[string]string{
		"notifications":     `SELECT COALESCE(string_agg(payload::text, ' '), '') FROM notifications WHERE created_at > now() - interval '10 minutes'`,
		"audit rows":        `SELECT COALESCE(string_agg(parameters::text || COALESCE(error_message, ''), ' '), '') FROM audit_logs WHERE timestamp > now() - interval '10 minutes'`,
		"endpoint failures": `SELECT COALESCE(string_agg(error, ' '), '') FROM script_endpoint_failures WHERE created_at > now() - interval '10 minutes'`,
	} {
		var text string
		if err := db.QueryRowContext(t.Context(), sql).Scan(&text); err != nil {
			t.Fatalf("reading %s: %v", sink, err)
		}
		sinks[sink] = text
	}
	for _, f := range stderrLogFiles(t) {
		raw, err := os.ReadFile(f) // #nosec G304 -- the dev stack's own log
		if err == nil {
			sinks["log "+f] = string(raw)
		}
	}
	jsonEscaped, _ := json.Marshal(issue2494Secret)
	for sink, text := range sinks {
		for _, form := range []string{
			issue2494Secret, base64.StdEncoding.EncodeToString([]byte(issue2494Secret)), hex.EncodeToString([]byte(issue2494Secret)),
			url.QueryEscape(issue2494Secret), strings.Trim(string(jsonEscaped), `"`),
		} {
			if strings.Contains(text, form) {
				t.Errorf("the sensitive value reached %s as %q", sink, form)
			}
		}
	}
}

// TestIssue2494_APreviewIsTheOwners is criterion 3: another person who is
// not an administrator opening the owner's preview link is refused, the page
// signs them nothing, and the preview's API answers nobody without the token.
func TestIssue2494_APreviewIsTheOwners(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := issue2494Save(t, owner, "acc-2494-mine")
	path := issue2494Preview(t, owner, name, nil)
	status, viewer, refused := issue2494PageViewer(t, owner.base, path, devPeerAPIKey)
	if status != http.StatusForbidden || viewer != "" || !strings.Contains(refused, "belongs to the person who opened it") {
		t.Fatalf("the peer opening the owner's preview: %d viewer=%q refused=%q", status, viewer, refused)
	}
	api := strings.Replace(path, "/forms/_preview/", "/endpoints/_preview/", 1)
	if got := endpointRequest(t, owner.base, http.MethodGet, api, bearer(devPeerAPIKey), ""); got.status != http.StatusForbidden {
		t.Fatalf("the preview's API answered the peer: %d %s", got.status, got.body)
	}
}

// TestIssue2494_TheOwnerPublishesAnInternalForm is criterion 4: the owner
// binds and enables an internal form with no administrator, a signed-in
// person fills it at its endpoint, and binding an endpoint on another
// person's script is refused naming the reason.
func TestIssue2494_TheOwnerPublishesAnInternalForm(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	name := issue2494Save(t, owner, "acc-2494-internal")
	bindEndpoint(t, owner, name, "", map[string]any{
		"endpoint": name, "auth_mode": "authenticated", "callers": []string{"*"}, "methods": []string{"GET", "POST"}, "connection": formsConn,
	})
	if out := owner.endpointEnable(name, false); out["enabled"] != false {
		t.Fatalf("the owner turning the form off: %v", out)
	}
	if out := owner.endpointEnable(name, true); out["enabled"] != true {
		t.Fatalf("the owner turning the form on: %v", out)
	}
	_, token := issue2494Spec(t, peer.base, "/endpoints/"+name, bearer(devPeerAPIKey))
	if status, out := issue2494Post(t, peer.base, "/endpoints/"+name, bearer(devPeerAPIKey), map[string]any{"email": "peer@example.com", "form_token": token}); status != http.StatusOK ||
		out["confirmation"] != "Thanks from version 1." {
		t.Fatalf("a signed-in person filling the internal form: %d %v", status, out)
	}
	if got := endpointRequest(t, peer.base, http.MethodGet, "/forms/"+name, bearer(devPeerAPIKey), ""); got.status != http.StatusOK {
		t.Fatalf("the internal form's page for a signed-in person: %d", got.status)
	}

	out, text := endpointSet(t, peer, name, devOwnerEmail, map[string]any{"endpoint": name + "-x", "auth_mode": "authenticated"})
	if out != nil || !strings.Contains(text, "only a script's owner or an administrator") {
		t.Fatalf("the peer binding the owner's script: %v %s", out, text)
	}
}

// issue2494Approvals lists requests as c.
func issue2494Approvals(t *testing.T, c *client, args map[string]any) []map[string]any {
	t.Helper()
	params := map[string]any{"command": "list"}
	for k, v := range args {
		params[k] = v
	}
	out := c.call("manage_approval", params)
	raw, _ := json.Marshal(out["requests"])
	var rows []map[string]any
	_ = json.Unmarshal(raw, &rows)
	return rows
}

// issue2494RequestNotices counts the queued notifications asking for
// approval of version of name, per recipient.
func issue2494RequestNotices(t *testing.T, admin *client, name string, version int) map[string]int {
	t.Helper()
	status, body := admin.rest(http.MethodGet, "/api/v1/admin/notifications?per_page=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the notification history: %d %v", status, body)
	}
	rows, _ := body["data"].([]any)
	want := fmt.Sprintf("asked for approval of version %d of the form %s ", version, name)
	got := map[string]int{}
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if strings.Contains(fmt.Sprint(row["subject"]), want) {
			got[fmt.Sprint(row["recipient"])]++
		}
	}
	return got
}

// TestIssue2494_APublicFormNeedsAnApprovedVersion is criteria 5 to 8: public
// without an approved version is refused naming the request to file; the
// owner requests version 2 through MCP, each administrator is notified once,
// the administrator approves it through MCP, the owner switches the form to
// public and a caller with no session fills it; version 3 is served to
// signed-in people while the public keeps version 2, and its request names
// the change; and a non-administrator deciding any request, their own
// included, is refused.
func TestIssue2494_APublicFormNeedsAnApprovedVersion(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	admin := connect(t)
	name := issue2494Save(t, owner, "acc-2494-public")
	internal := map[string]any{"endpoint": name, "auth_mode": "authenticated", "callers": []string{"*"}, "methods": []string{"GET", "POST"}, "connection": formsConn}
	bindEndpoint(t, owner, name, "", internal)

	// 5: refused, naming the request to file.
	public := map[string]any{"endpoint": name, "auth_mode": "public", "methods": []string{"GET", "POST"}}
	if out, text := endpointSet(t, owner, name, "", public); out != nil || !strings.Contains(text, "manage_approval") || !strings.Contains(text, "Request public access") {
		t.Fatalf("public with no approved version: %v %s", out, text)
	}

	// 6: version 2 requested, notified once, approved, served publicly.
	issue2494Edit(t, owner, name, 2)
	submitted := owner.call("manage_approval", map[string]any{"command": "submit", "script": name, "version": 2, "reason": "Going on the site."})
	request, _ := submitted["request"].(map[string]any)
	id, _ := request["id"].(string)
	if id == "" || request["version"] != float64(2) || request["status"] != "pending" {
		t.Fatalf("submit: %v", submitted)
	}
	notices := map[string]int{}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		if notices = issue2494RequestNotices(t, admin, name, 2); len(notices) > 0 {
			break
		}
	}
	if len(notices) == 0 {
		t.Fatalf("no administrator was notified of the request")
	}
	for recipient, n := range notices {
		if n != 1 {
			t.Errorf("%s was notified %d times of one request", recipient, n)
		}
		if recipient == devOwnerEmail {
			t.Errorf("the requester was notified of their own request")
		}
	}
	// 8: a non-administrator deciding, the requester included, is refused.
	for who, c := range map[string]*client{"the requester": owner, "another person": peer} {
		res, text, err := c.callRaw("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve"})
		if err != nil || !res.IsError || !strings.Contains(text, "administrators decide requests") {
			t.Fatalf("%s deciding the request: %v %s", who, err, text)
		}
	}
	decided := admin.call("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve", "comment": "Reads well."})
	if decided["status"] != "approved" || decided["decided_by"] == "" {
		t.Fatalf("decide: %v", decided)
	}
	bindEndpoint(t, owner, name, "", public)
	spec, token := issue2494Spec(t, owner.base, "/endpoints/"+name, nil)
	if spec["confirmation"] != "Thanks from version 2." {
		t.Fatalf("the public form serves %v, want version 2", spec["confirmation"])
	}
	if status, out := issue2494Post(t, owner.base, "/endpoints/"+name, nil, map[string]any{"email": "anon@example.com", "form_token": token}); status != http.StatusOK {
		t.Fatalf("a caller with no session filling the public form: %d %v", status, out)
	}

	// 7: version 3 is served to signed-in people; the public keeps version 2.
	issue2494Edit(t, owner, name, 3)
	if spec, _ := issue2494Spec(t, owner.base, "/endpoints/"+name, nil); spec["confirmation"] != "Thanks from version 2." {
		t.Errorf("after version 3 the public is served %v, want version 2", spec["confirmation"])
	}
	if spec, _ := issue2494Spec(t, owner.base, "/endpoints/"+name, bearer(devOwnerAPIKey)); spec["confirmation"] != "Thanks from version 3." {
		t.Errorf("a signed-in person is served %v, want version 3", spec["confirmation"])
	}
	next := owner.call("manage_approval", map[string]any{"command": "submit", "script": name, "version": 3})
	nextReq, _ := next["request"].(map[string]any)
	changes, _ := nextReq["changes"].(map[string]any)
	if changes["approved_version"] != float64(2) || !strings.Contains(fmt.Sprint(changes["diff"]), "+CONFIRM = \"Thanks from version 3.\"") {
		t.Fatalf("the request for version 3 does not name the change since version 2: %v", nextReq)
	}
	mine := issue2494Approvals(t, owner, map[string]any{"status": "pending"})
	for _, r := range mine {
		if r["requester_email"] != devOwnerEmail {
			t.Errorf("the owner's list holds another person's request: %v", r)
		}
	}
}
