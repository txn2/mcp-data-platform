//go:build integration

package acceptance

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"
	"time"
)

// Issue #1568: a thumbnail is a property of a piece of content, not of a kind.
//
// Three things around one shared capture pipeline had been written a second
// time and each was wrong in a different way: a resource tile never asked for
// the dark capture, markdown and plain text got no capture at all, and the four
// copies of "what gets a thumbnail" had drifted apart.
//
// Nothing on a server rasterizes a document, so the capture itself happens in a
// browser and is not executed here. What is executed here is everything the
// browser talks to: what a generic declaration is stored as, what the platform
// then reports as needing a capture, and what clearing one does.
//
// Wire forms: manage_resource's action, filename, display_name, path,
// description, content, content_base64 and content_type are typed strings in
// its schema and tags is an array of strings, so each admits exactly one JSON
// form and each is sent below as a literal tools/call parameter of that form.
// The declaration is the parameter this ticket turns on, so all three of its
// meaningful values are sent through both write surfaces that carry one: as
// content_type on manage_resource ("text/plain" and "application/octet-stream"
// -- create refuses an empty one since #1508), and as the multipart part's
// Content-Type on POST /api/v1/resources ("text/plain",
// "application/octet-stream", and the part header omitted altogether, which is
// the third form only the HTTP surface admits). The REST reads take their limit
// as a query-string parameter, which has no second form, and the clear takes no
// body at all.

func unique1568() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// createResource1568 files a resource through manage_resource under the
// declaration given, and returns its id and the type it was stored as.
func createResource1568(t *testing.T, c *client, filename, declared, content string) (id, mime string) {
	t.Helper()
	name := "acceptance-1568-" + unique1568()
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     filename,
		"display_name": name,
		"path":         "acceptance-1568",
		"description":  "Acceptance #1568: what a generic declaration is stored as.",
		"content":      content,
		"content_type": declared,
		"tags":         []any{"acceptance-1568"},
	})
	id, _ = out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() {
		_, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
	})
	return id, storedType1568(t, c, id)
}

// uploadResource1568 files a resource the way the portal's upload dialog does:
// a multipart POST whose part carries the browser's own declaration. A declared
// of "" omits the part's Content-Type header entirely, which is the third form
// this surface admits and the one a non-browser client sends.
func uploadResource1568(t *testing.T, c *client, filename, declared string, content []byte) (id, mime string) {
	t.Helper()
	body := new(bytes.Buffer)
	w := multipart.NewWriter(body)

	head := make(textproto.MIMEHeader)
	head.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	if declared != "" {
		head.Set("Content-Type", declared)
	}
	part, err := w.CreatePart(head)
	if err != nil {
		t.Fatalf("building the upload part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("writing the upload part: %v", err)
	}
	for field, value := range map[string]string{
		"scope":        "global",
		"path":         "acceptance-1568",
		"display_name": "acceptance-1568-upload-" + unique1568(),
		"description":  "Acceptance #1568: what the portal upload dialog stores a .md as.",
	} {
		if err := w.WriteField(field, value); err != nil {
			t.Fatalf("writing %s: %v", field, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the upload body: %v", err)
	}

	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, baseURL()+"/api/v1/resources", body)
	if err != nil {
		t.Fatalf("building the upload: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("uploading: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		t.Fatalf("upload: status %d: %s", res.StatusCode, raw)
	}

	var out map[string]any
	decodeJSON1568(t, raw, &out)
	id, _ = out["id"].(string)
	if id == "" {
		t.Fatalf("upload returned no id: %s", raw)
	}
	t.Cleanup(func() {
		_, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
	})
	mime, _ = out["mime_type"].(string)
	return id, mime
}

// decodeJSON1568 unmarshals a response body, failing with the body in hand.
func decodeJSON1568(t *testing.T, raw []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decoding the response: %v\n%s", err, raw)
	}
}

// storedType1568 reads back the type the platform decided to store.
func storedType1568(t *testing.T, c *client, id string) string {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading resource %s: status %d: %v", id, status, body)
	}
	mime, _ := body["mime_type"].(string)
	return mime
}

// pendingIDs1568 returns the ids the platform reports as needing a capture.
func pendingIDs1568(t *testing.T, c *client) map[string]bool {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources/thumbnails/pending?limit=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET pending: status %d: %v", status, body)
	}
	ids := map[string]bool{}
	list, _ := body["resources"].([]any)
	for _, item := range list {
		r, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := r["id"].(string); id != "" {
			ids[id] = true
		}
	}
	return ids
}

// TestIssue1568_AGenericDeclarationIsResolvedByTheExtension is criterion 3
// through both write surfaces, in every form each of them admits.
//
// A .md declared text/plain, application/octet-stream, or nothing at all was
// stored as text/plain: the extension table holds the right answer and was
// consulted only where the declaration was already specific. Markdown has no
// content signature, so nothing else could ever recover it -- which is why the
// file opened in the plain-text viewer and got no thumbnail.
func TestIssue1568_AGenericDeclarationIsResolvedByTheExtension(t *testing.T) {
	c := connect(t)
	const markdown = "# Release notes\n\nA paragraph with a [link](https://example.com).\n"

	for _, declared := range []string{"text/plain", "application/octet-stream"} {
		_, mime := createResource1568(t, c, "notes-"+unique1568()+".md", declared, markdown)
		if mime != "text/markdown" {
			t.Errorf("manage_resource, declared %q: stored as %q, want text/markdown", declared, mime)
		}
	}

	// The portal's own dialog, including the form only it admits: no part
	// Content-Type at all, which is what a non-browser client sends.
	for _, declared := range []string{"text/plain", "application/octet-stream", ""} {
		_, mime := uploadResource1568(t, c, "notes-"+unique1568()+".md", declared, []byte(markdown))
		if mime != "text/markdown" {
			t.Errorf("upload, part Content-Type %q: stored as %q, want text/markdown", declared, mime)
		}
	}
}

// TestIssue1568_TheNameNeverOverridesTheBytesOrASpecificDeclaration is
// criterion 4: the name is consulted only where nothing else has an answer.
func TestIssue1568_TheNameNeverOverridesTheBytesOrASpecificDeclaration(t *testing.T) {
	c := connect(t)

	// A .md holding a PNG. The bytes are recognized as a binary family before
	// the name is ever asked, so the name cannot promote a mislabeled binary.
	png, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatalf("decoding the fixture PNG: %v", err)
	}
	if _, mime := uploadResource1568(t, c, "picture-"+unique1568()+".md", "", png); mime != "image/png" {
		t.Errorf("a .md holding a PNG stored as %q, want image/png", mime)
	}

	// A specific declaration still wins over a name that disagrees with it.
	_, mime := createResource1568(t, c, "notes-"+unique1568()+".md", "text/csv",
		"# Not a CSV\n\nProse, declared as CSV.\n")
	if mime != "text/csv" {
		t.Errorf("a specific declaration was overridden: stored as %q, want text/csv", mime)
	}
}

// TestIssue1568_EveryCapturableFamilyIsOfferedTheWork is criteria 5 and 6 on
// the resource side: the families this store had lost against the other three
// copies of the rule are on the pending list, and the ones nothing draws are
// still not.
func TestIssue1568_EveryCapturableFamilyIsOfferedTheWork(t *testing.T) {
	c := connect(t)

	// Plain text, which is one of the commonest things anyone uploads and got
	// no thumbnail of either kind, and JSX, which the capturer renders and was
	// never offered.
	textID, textMIME := createResource1568(t, c, "log-"+unique1568()+".txt", "text/plain",
		"Rows written: 41208\nRows rejected: 0\nElapsed: 4.2s\n")
	if textMIME != "text/plain" {
		t.Fatalf("the .txt fixture stored as %q, want text/plain", textMIME)
	}
	jsxID, jsxMIME := createResource1568(t, c, "panel-"+unique1568()+".jsx", "text/jsx",
		"export default function Panel() { return <p>Panel</p>; }\n")
	if jsxMIME != "text/jsx" {
		t.Fatalf("the .jsx fixture stored as %q, want text/jsx", jsxMIME)
	}
	// A family nothing rasterizes, which must still never be offered: an item
	// no browser will ever accept is offered forever and crowds out the rest.
	pdfID, _ := createResource1568(t, c, "report-"+unique1568()+".pdf", "application/pdf",
		"%PDF-1.7\nnot really a PDF, but declared as one\n")

	pending := pendingIDs1568(t, c)
	if !pending[textID] {
		t.Errorf("a plain-text resource is not offered for capture")
	}
	if !pending[jsxID] {
		t.Errorf("a JSX resource is not offered for capture")
	}
	if pending[pdfID] {
		t.Errorf("a PDF is offered for capture, which nothing can draw")
	}
}

// TestIssue1568_ClearingAResourcesThumbnailTakesBothVariants is the server half
// of criterion 8: the control the resource viewer now carries sends this, and
// both views of one file go together.
func TestIssue1568_ClearingAResourcesThumbnailTakesBothVariants(t *testing.T) {
	c := connect(t)
	id, mime := createResource1568(t, c, "notes-"+unique1568()+".md", "text/markdown",
		"# Notes\n\nProse for the capture to render.\n")
	if mime != "text/markdown" {
		t.Fatalf("the fixture stored as %q, want text/markdown", mime)
	}

	png, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatalf("decoding the fixture PNG: %v", err)
	}
	for _, q := range []string{"", "?variant=dark"} {
		if status := putCapture1554(t, c, id, q, "image/png", png); status != http.StatusOK {
			t.Fatalf("uploading the capture %q: status %d, want 200", q, status)
		}
	}
	if pendingIDs1568(t, c)[id] {
		t.Fatalf("a resource with both captures is still offered for capture")
	}

	// The clear takes no variant: asking for the tile to be taken again means
	// the tile, not the half of it the reader's color mode happens to show.
	status, body := c.rest(http.MethodDelete, "/api/v1/resources/"+id+"/thumbnail", http.NoBody)
	if status != http.StatusNoContent {
		t.Fatalf("clearing the captures: status %d: %v", status, body)
	}

	for _, q := range []string{"", "?variant=dark"} {
		if got, _ := getCapture1554(t, c, id, q); got != http.StatusNotFound {
			t.Errorf("reading the %q capture after the clear: status %d, want 404", q, got)
		}
	}
	if !pendingIDs1568(t, c)[id] {
		t.Errorf("a cleared resource is not offered for capture again")
	}
}
