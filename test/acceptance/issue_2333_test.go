//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2333: a form's file field. A public submission with two
// images saves two managed resources and its row in webhook_form_<endpoint>
// carries their addresses; a file past max_bytes, a type the field does not
// take (judged from its bytes) and one file more than max_files are each
// refused on the field with nothing saved; a submission form_submit refuses
// keeps no file; and the form's owner opens each file from the submissions
// view.
//
// Wire forms: manage_script create and endpoint_set take their arguments as
// literal tools/call params; the form GET is a plain GET; a submission with
// files is multipart/form-data, the one form a file field takes, its values
// as text parts and each file as a part with a filename; the submissions
// route and a resource's content are GETs; trino_query takes string
// connection, sql and purpose.
//
// The criteria run against the local stack: the platform, Trino with the
// acme-scratch-resources scratch catalog, the store the receiver and the
// managed resources write to, and Postgres.

// issue2333Source is a form taking up to two images of at most 4 KiB, which
// refuses a submission whose note is "refuse".
const issue2333Source = `def form_spec(req):
    """The site content form: a note and up to two images."""
    return {
        "fields": [
            {"name": "note", "type": "text", "label": "Note"},
            {"name": "images", "type": "file", "label": "Images", "max_files": 2, "max_bytes": 4096, "accept": ["image/*"]},
        ],
        "confirmation": "Thanks, we have your files.",
    }

def form_submit(req, values):
    """Refuses one note, and confirms with the address of the first image."""
    if values["note"] == "refuse":
        return {"errors": {"note": "not this one"}}
    images = values["images"]
    return {"confirmation": images[0]["uri"] if images else "no images"}

def main():
    """Drafts the spec, a refusal and an acceptance."""
    spec = form_spec({"query": {}, "caller": None})
    refused = form_submit({"caller": None}, {"note": "refuse", "images": []})
    accepted = form_submit({"caller": None}, {"note": "ok", "images": []})
    platform.result({"fields": len(spec["fields"]), "refused": refused, "accepted": accepted})
`

// issue2333PNG is a PNG of n bytes: the signature and a header chunk, then
// padding, which is what its type is judged by.
func issue2333PNG(n int) []byte {
	head := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00")
	if n <= len(head) {
		return head
	}
	return append(head, bytes.Repeat([]byte{0}, n-len(head))...)
}

// issue2333Bind saves and binds the file form, and returns its name, table
// and script id.
func issue2333Bind(t *testing.T, admin *client) (form, table, scriptID string) {
	t.Helper()
	form = endpointName("acc-2333-files")
	admin.saveScript(map[string]any{"command": "create", "name": form, "source": issue2333Source}, nil)
	out := bindEndpoint(t, admin, form, "", map[string]any{
		"endpoint": form, "methods": []string{"GET", "POST"}, "connection": formsConn, "auth_mode": "public",
	})
	src, _ := out["form"].(map[string]any)
	table, _ = src["table"].(string)
	if table == "" {
		t.Fatalf("binding %s made no source: %v", form, out)
	}
	t.Cleanup(func() {
		admin.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+fmt.Sprint(src["source"]), http.NoBody)
	})
	return form, table, issue2179ScriptID(t, admin, form)
}

// issue2333File is one file of a submission.
type issue2333File struct {
	name string
	data []byte
}

// issue2333Submit posts note and files to the form as multipart/form-data
// with the token of a fresh GET, once its fill time has passed.
func issue2333Submit(t *testing.T, form, note string, files ...issue2333File) (int, map[string]any) {
	t.Helper()
	shown := showForm(t, baseURL(), form, "", nil)
	shown.waitToFill()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range map[string]string{"note": note, "form_token": shown.token} {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		part, err := w.CreateFormFile("images", f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := endpointRequest(t, baseURL(), http.MethodPost, "/endpoints/"+form, http.Header{"Content-Type": {w.FormDataContentType()}}, buf.String())
	var out map[string]any
	_ = json.Unmarshal([]byte(got.body), &out)
	return got.status, out
}

// issue2333Saved lists the files saved for the form's source.
func issue2333Saved(t *testing.T, admin *client, form string) []any {
	t.Helper()
	q := url.Values{"path": {"webhooks/form-" + form + "/attachments"}, "limit": {"200"}}
	status, out := admin.rest(http.MethodGet, "/api/v1/resources?"+q.Encode(), nil)
	if status != http.StatusOK {
		t.Fatalf("listing the form's files: %d %v", status, out)
	}
	list, _ := out["data"].([]any)
	if list == nil {
		list, _ = out["resources"].([]any)
	}
	return list
}

// TestIssue2333_TwoImagesAreSavedAndTheRowCarriesTheirAddresses is criterion
// 1, with the owner's submissions view (criterion 4): two images become two
// managed resources, the stored row names them, form_submit was handed the
// first one's address, and the owner reads each file from the view's links.
func TestIssue2333_TwoImagesAreSavedAndTheRowCarriesTheirAddresses(t *testing.T) {
	admin := connect(t)
	form, table, scriptID := issue2333Bind(t, admin)
	status, out := issue2333Submit(t, form, "Homepage images",
		issue2333File{"Hero Banner.png", issue2333PNG(1500)}, issue2333File{"logo.png", issue2333PNG(800)})
	if status != http.StatusOK {
		t.Fatalf("the submission with two images: %d %v", status, out)
	}
	confirmed, _ := out["confirmation"].(string)
	if !strings.HasPrefix(confirmed, "mcp://") || !strings.HasSuffix(confirmed, "/hero-banner.png") {
		t.Fatalf("form_submit was not handed the first image's address: %v", out)
	}

	rows := formRows(t, admin, table, 1)
	images, _ := rows[0].values()["images"].([]any)
	if len(images) != 2 {
		t.Fatalf("the row's images: %v", rows[0].values())
	}
	first, _ := images[0].(map[string]any)
	if first["uri"] != confirmed || first["name"] != "Hero Banner.png" || first["content_type"] != "image/png" || first["resource_id"] == nil {
		t.Fatalf("the row's first image: %v", first)
	}
	if saved := issue2333Saved(t, admin, form); len(saved) != 2 {
		t.Fatalf("the form saved %d files, want 2: %v", len(saved), saved)
	}

	// The submissions view, read until the new submission shows, links each
	// file; the owner opens each.
	var subs []any
	deadline := time.Now().Add(time.Minute)
	for len(subs) == 0 && time.Now().Before(deadline) {
		got := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/submissions?limit=20", bearer(devAPIKey()), "")
		var page map[string]any
		_ = json.Unmarshal([]byte(got.body), &page)
		subs, _ = page["submissions"].([]any)
		if len(subs) == 0 {
			time.Sleep(time.Second)
		}
	}
	if len(subs) != 1 {
		t.Fatalf("the submissions view holds %d submissions", len(subs))
	}
	values, _ := subs[0].(map[string]any)["values"].(map[string]any)
	listed, _ := values["images"].([]any)
	for i, f := range listed {
		id := fmt.Sprint(f.(map[string]any)["resource_id"])
		content := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/resources/"+id+"/content", bearer(devAPIKey()), "")
		if content.status != http.StatusOK || !strings.HasPrefix(content.body, "\x89PNG") {
			t.Errorf("opening file %d (%s) from the submissions view: %d", i, id, content.status)
		}
	}
}

// TestIssue2333_RefusedFilesSaveNothing is criteria 2 and 3: a file past
// max_bytes, one whose bytes are not an image though it is named .png, and a
// third file are each the field's error, and a submission form_submit refuses
// keeps none of its files.
func TestIssue2333_RefusedFilesSaveNothing(t *testing.T) {
	admin := connect(t)
	form, _, _ := issue2333Bind(t, admin)
	small := issue2333File{"small.png", issue2333PNG(100)}
	for name, files := range map[string][]issue2333File{
		"past max_bytes": {{"big.png", issue2333PNG(5000)}},
		"not an image":   {{"notes.png", []byte("these are plain words, not an image")}},
		"past max_files": {small, small, small},
	} {
		status, out := issue2333Submit(t, form, "ok", files...)
		errs, _ := out["errors"].(map[string]any)
		if status != http.StatusUnprocessableEntity || errs["images"] == nil {
			t.Errorf("%s: %d %v, want 422 naming the images field", name, status, out)
		}
	}
	if status, out := issue2333Submit(t, form, "refuse", small); status != http.StatusUnprocessableEntity {
		t.Fatalf("a submission form_submit refuses: %d %v", status, out)
	}
	if saved := issue2333Saved(t, admin, form); len(saved) != 0 {
		t.Fatalf("refused submissions kept %d files: %v", len(saved), saved)
	}
}
