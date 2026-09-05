//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// Issue #1628: the resource upload ceiling was the compiled-in constant
// resource.MaxUploadBytes (100 MB), duplicated in the browser as its own
// MAX_BYTES plus two strings. A deployment whose reference material runs larger
// had no way to say so, and raising it meant editing the constant -- which
// raises it for every deployment -- and hand-editing the browser's copy.
//
// It is now resources.managed.max_upload_bytes. This suite runs against the dev
// stack, which sets it to 262144000 (250 MB) so these criteria execute the
// configured path rather than the default. The default path -- absent, zero and
// negative all selecting 100 MB -- is pinned by TestNormalizeMaxUploadBytes and
// TestCreate_UnsetCeilingRefusesByTheDefault in pkg/resource, since one running
// deployment cannot be two deployments at once.
//
// Wire forms. This ticket touches no MCP tool parameter: the surfaces are two
// multipart HTTP routes, whose `file` part is bytes and whose metadata fields
// are form-encoded strings with exactly one form each, and one JSON GET
// (/api/v1/portal/me) that takes no parameters. The forms exercised below are
// therefore the whole set the schemas admit: the file part with a declared
// Content-Type and without one, at the ceiling, over it, and far over it.

const (
	// issue1628Ceiling is what dev/platform.yaml configures, and what every
	// criterion below is stated against.
	issue1628Ceiling = 262144000 // 250 MB
	// issue1628Named is how the ceiling reads in a refusal and in the portal's
	// file chooser.
	issue1628Named = "250 MB"
	// issue1628NotNamed is the compiled-in default. A deployment configured for
	// 250 MB that still says this is the defect, in the other direction.
	issue1628NotNamed = "100 MB"
	// issue1628UploadTimeout bounds one large upload. Generous: a quarter of a
	// gigabyte crosses the loopback and then goes to blob storage.
	issue1628UploadTimeout = 5 * time.Minute
)

// TestIssue1628_MeReportsTheDeploymentsCeiling is the criterion behind the
// portal's file chooser: the number arrives from the server. The browser holds
// no copy of it, so this payload is the only thing that can make the dialog
// say 250 MB.
func TestIssue1628_MeReportsTheDeploymentsCeiling(t *testing.T) {
	c := connect(t)

	status, body := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/portal/me: status %d: %v", status, body)
	}

	got, ok := body["max_upload_bytes"].(float64)
	if !ok {
		t.Fatalf("/me carries no max_upload_bytes, so the upload dialog has nothing to read: %v", body)
	}
	if int64(got) != issue1628Ceiling {
		t.Fatalf("max_upload_bytes = %d, want %d (the deployment's configured ceiling)",
			int64(got), issue1628Ceiling)
	}
}

// TestIssue1628_AFileAtTheCeilingIsStored is the criterion the ticket exists
// for: a deployment that raised the ceiling can actually file something that
// size, and the whole file is what it stored.
//
// A file of exactly the ceiling is the case that failed before this change for
// a second reason: the request body was bounded at exactly the ceiling, which
// the multipart framing around the file already exceeds, so the upload was
// refused as a malformed form.
func TestIssue1628_AFileAtTheCeilingIsStored(t *testing.T) {
	c := connect(t)

	status, body := uploadSized1628(t, c, "at-the-ceiling.txt", "text/plain", issue1628Ceiling)
	if status != http.StatusCreated {
		t.Fatalf("uploading a file of exactly the ceiling: status %d: %v", status, body)
	}
	if got, _ := body["size_bytes"].(float64); int64(got) != issue1628Ceiling {
		t.Fatalf("size_bytes = %d, want %d (the whole file must be stored)", int64(got), issue1628Ceiling)
	}
}

// TestIssue1628_PastTheCeilingIsRefusedByThisDeploymentsNumber pins the message.
// A refusal reading "file exceeds 100 MB limit" on a deployment configured for
// 250 MB is the defect this ticket exists to prevent, in the other direction.
func TestIssue1628_PastTheCeilingIsRefusedByThisDeploymentsNumber(t *testing.T) {
	c := connect(t)

	status, body := uploadSized1628(t, c, "over-the-ceiling.txt", "text/plain", issue1628Ceiling+(1<<20))
	if status != http.StatusBadRequest {
		t.Fatalf("uploading past the ceiling: status %d, want 400: %v", status, body)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, issue1628Named) {
		t.Fatalf("refusal = %q, want it to name this deployment's ceiling (%s)", message, issue1628Named)
	}
	if strings.Contains(message, issue1628NotNamed) {
		t.Fatalf("refusal = %q, but this deployment does not refuse at %s", message, issue1628NotNamed)
	}
}

// TestIssue1628_ReplaceContentHonorsTheSameCeiling covers the second write
// route. It carried its own copy of the constant, so a ceiling reaching only
// create would let a file be filed and then refused on replacement.
func TestIssue1628_ReplaceContentHonorsTheSameCeiling(t *testing.T) {
	c := connect(t)

	// A small resource to revise, filed with no declared Content-Type on the
	// part -- the other form this route admits.
	status, created := uploadSized1628(t, c, "to-be-replaced.txt", "", 1<<10)
	if status != http.StatusCreated {
		t.Fatalf("filing the resource to revise: status %d: %v", status, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create returned no id: %v", created)
	}

	status, body := replaceSized1628(t, c, id, "at-the-ceiling.txt", issue1628Ceiling)
	if status != http.StatusOK {
		t.Fatalf("replacing content with a file at the ceiling: status %d: %v", status, body)
	}
	if got, _ := body["size_bytes"].(float64); int64(got) != issue1628Ceiling {
		t.Fatalf("revised size_bytes = %d, want %d", int64(got), issue1628Ceiling)
	}

	status, versions := c.rest(http.MethodGet, "/api/v1/resources/"+id+"/versions", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET versions: status %d: %v", status, versions)
	}
	list, _ := versions["versions"].([]any)
	if len(list) < 2 {
		t.Fatalf("recorded versions = %d, want at least 2 (the original and the replacement): %v",
			len(list), versions)
	}
}

// uploadSized1628 files a resource of exactly size bytes, streaming the body so
// the test process never holds a quarter of a gigabyte of its own. A declared
// of "" omits the part's Content-Type header, which is what a non-browser
// client sends.
func uploadSized1628(t *testing.T, c *client, filename, declared string, size int64) (int, map[string]any) {
	t.Helper()
	fields := map[string]string{
		"scope":        "global",
		"path":         "acceptance-1628",
		"display_name": fmt.Sprintf("acceptance-1628-%s-%d", filename, time.Now().UnixNano()),
		"description":  "Acceptance #1628: what this deployment's upload ceiling accepts.",
	}
	status, body := sendUpload1628(t, c, http.MethodPost, "/api/v1/resources",
		filename, declared, size, fields)
	if id, _ := body["id"].(string); id != "" {
		t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	}
	return status, body
}

// replaceSized1628 uploads a replacement of exactly size bytes for an existing
// resource. The route ignores the metadata fields, so only the file goes.
func replaceSized1628(t *testing.T, c *client, id, filename string, size int64) (int, map[string]any) {
	t.Helper()
	return sendUpload1628(t, c, http.MethodPost, "/api/v1/resources/"+id+"/content",
		filename, "text/plain", size, nil)
}

// sendUpload1628 writes the multipart body through a pipe as the request is
// sent, so a quarter-gigabyte upload costs the test a fixed buffer rather than
// the whole file.
func sendUpload1628(
	t *testing.T, c *client, method, path, filename, declared string,
	size int64, fields map[string]string,
) (int, map[string]any) {
	t.Helper()

	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	go func() {
		_ = pw.CloseWithError(writeUpload1628(w, filename, declared, size, fields))
	}()

	req, err := http.NewRequestWithContext(c.ctx, method, baseURL()+path, pr)
	if err != nil {
		t.Fatalf("building the upload: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{Timeout: issue1628UploadTimeout}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the body: %v", method, path, err)
	}
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out) //nolint:errcheck // a non-object body leaves out nil
	}
	return res.StatusCode, out
}

// writeUpload1628 composes the multipart body: the metadata fields, then the
// file part of exactly size bytes.
//
// The file goes last because the routes stream that part to blob storage where
// they find it and read no part behind it (#1631). This helper sent it first
// while the form was parsed whole, which is the client that has to reorder.
func writeUpload1628(
	w *multipart.Writer, filename, declared string, size int64, fields map[string]string,
) error {
	for field, value := range fields {
		if err := w.WriteField(field, value); err != nil {
			return err
		}
	}
	part, err := createPart1628(w, filename, declared)
	if err != nil {
		return err
	}
	if err := writeFiller1628(part, size); err != nil {
		return err
	}
	return w.Close()
}

// createPart1628 opens the file part. A declared of "" omits the part's
// Content-Type header entirely, which is what a non-browser client sends and
// the second of the two forms this part admits.
func createPart1628(w *multipart.Writer, filename, declared string) (io.Writer, error) {
	head := make(textproto.MIMEHeader)
	head.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	if declared != "" {
		head.Set("Content-Type", declared)
	}
	return w.CreatePart(head)
}

// writeFiller1628 writes exactly size bytes in fixed-size chunks.
func writeFiller1628(part io.Writer, size int64) error {
	const chunk = 1 << 20
	buf := make([]byte, chunk)
	for i := range buf {
		buf[i] = 'a'
	}
	for remaining := size; remaining > 0; {
		n := int64(chunk)
		if remaining < n {
			n = remaining
		}
		if _, err := part.Write(buf[:n]); err != nil {
			return err
		}
		remaining -= n
	}
	return nil
}
