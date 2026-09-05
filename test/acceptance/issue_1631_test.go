//go:build integration

package acceptance

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #1631: the managed-resource blob write was the one upload path in the
// platform without multipart. The whole object went to storage as a single
// PutObject, and an S3-compatible backend is free to bound one: MinIO refuses
// a PutObject above 16 MiB under the aws-chunked encoding the AWS SDK uses
// over HTTPS, so a file above that could not be filed at all, whatever
// resources.managed.max_upload_bytes said. The same file stored as a portal
// asset, which had always written through the transfer manager.
//
// The dev stack this suite runs against carries MinIO over TLS for exactly
// this reason (dev/docker-compose.yml, dev/platform.yaml): managed resources
// are stored on it, portal assets stay on SeaweedFS, and SeaweedFS accepts an
// unbounded single PUT, so it could not stand in for the backend these
// criteria are about.
//
// Wire forms. This ticket touches no MCP tool parameter. Its surfaces are two
// multipart HTTP routes, whose forms are enumerated and sent below:
//
//   - the file part WITH a declared Content-Type (what a browser sends) and
//     WITHOUT one (what a script sends) -- both sent, on both routes;
//   - the metadata fields before the file part (the order the routes read) and
//     a part behind it (refused, and nothing stored);
//   - a file above the single-PUT bound and one below it.
//
// The `file` part is bytes and the metadata fields are form-encoded strings,
// so those are the whole set the two forms admit. The tool path into the same
// write (manage_resource, whose content arrives as text in `content` or as
// base64 in `content_base64`) has an unchanged schema and its own inline size
// cap far below the bound here; both of its forms are exercised by
// TestIssue1631_TheToolPathStillStoresWhatItHolds.

const (
	// issue1631SinglePutBound is what MinIO refuses a single PutObject above.
	// Every size below is stated against it.
	issue1631SinglePutBound = 16 << 20
	// issue1631Size is comfortably past that bound and far under the dev
	// stack's 250 MB ceiling, so what it exercises is the write path and not
	// the ceiling.
	issue1631Size = 24 << 20
	// issue1631SpoolBound is the memory budget the old multipart parser
	// spooled a part to disk above. A file past it is what needed a writable
	// temporary directory.
	issue1631SpoolBound = 10 << 20
	// issue1631Timeout bounds one upload across the loopback and on to
	// storage.
	issue1631Timeout = 3 * time.Minute
)

// TestIssue1631_AFileAboveTheSinglePutBoundIsStored is the criterion the
// ticket exists for. Before the change this answered 503 with "The storage
// backend did not accept the file. Nothing was saved." and the log carried
// MinIO's "chunk too big: choose chunk size <= 16MiB".
func TestIssue1631_AFileAboveTheSinglePutBoundIsStored(t *testing.T) {
	c := connect(t)

	status, body := create1631(t, c, "above-the-bound.txt", "text/plain", issue1631Size)
	if status != http.StatusCreated {
		t.Fatalf("uploading %d bytes, which is past the %d-byte single-PUT bound: status %d: %v",
			issue1631Size, issue1631SinglePutBound, status, body)
	}
	if got, _ := body["size_bytes"].(float64); int64(got) != issue1631Size {
		t.Fatalf("size_bytes = %d, want %d (the whole file has to be stored)", int64(got), issue1631Size)
	}
}

// TestIssue1631_AReplacementAboveTheBoundRecordsAVersion covers the second
// write route. It carried its own copy of the single-PUT write, so a file that
// could be created could still fail to be replaced.
func TestIssue1631_AReplacementAboveTheBoundRecordsAVersion(t *testing.T) {
	c := connect(t)

	// Created small, and with no declared Content-Type on the part -- the
	// other form the file part admits.
	status, body := create1631(t, c, "to-be-replaced.txt", "", 64)
	if status != http.StatusCreated {
		t.Fatalf("creating the resource to replace: status %d: %v", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("the create returned no id: %v", body)
	}

	status, body = replace1631(t, c, id, "replacement.txt", "text/plain", issue1631Size)
	if status != http.StatusOK {
		t.Fatalf("replacing with %d bytes: status %d: %v", issue1631Size, status, body)
	}
	if got, _ := body["size_bytes"].(float64); int64(got) != issue1631Size {
		t.Fatalf("size_bytes = %d, want %d", int64(got), issue1631Size)
	}

	status, versions := c.rest(http.MethodGet, "/api/v1/resources/"+id+"/versions", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the version history: status %d: %v", status, versions)
	}
	list, _ := versions["versions"].([]any)
	if len(list) != 2 {
		t.Fatalf("version history has %d entries, want 2 (the upload and the replacement): %v",
			len(list), versions)
	}
}

// TestIssue1631_TheStoredObjectIsTheFileUploaded is the criterion that
// multipart did not change the bytes. The file is read back through the route
// a reader uses and compared by digest, since streaming a file through parts
// is exactly where a lost or duplicated chunk would show.
func TestIssue1631_TheStoredObjectIsTheFileUploaded(t *testing.T) {
	c := connect(t)

	status, body := create1631(t, c, "round-trip.txt", "text/plain", issue1631Size)
	if status != http.StatusCreated {
		t.Fatalf("uploading: status %d: %v", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("the create returned no id: %v", body)
	}

	want := digestFiller1631(issue1631Size)
	got, size := readContentDigest1631(t, c, id)
	if size != issue1631Size {
		t.Fatalf("read back %d bytes, want %d", size, issue1631Size)
	}
	if got != want {
		t.Fatalf("the object read back is not the file uploaded (sha256 %s, want %s)", got, want)
	}
}

// TestIssue1631_TheUploadNeedsNoTemporaryDirectory is the criterion the
// published image runs under. ParseMultipartForm spooled any part past its
// memory budget to os.CreateTemp, and the published image is built FROM
// scratch and has no /tmp, so every upload above that budget failed on it
// before storage was reached at all.
//
// The dev stack runs the platform with TMPDIR pointed at a path that does not
// exist (dev/.air.toml, recorded by dev/start.sh in dev/.dev-ports.env), which
// is that condition. The test states it rather than assuming it: an upload
// that succeeded where a temporary directory existed would prove nothing.
func TestIssue1631_TheUploadNeedsNoTemporaryDirectory(t *testing.T) {
	tmp := platformTempDir1631(t)
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("the platform's TMPDIR (%s) exists, so this run cannot exercise the criterion: "+
			"the dev stack has to start the server with a TMPDIR that is not there (stat: %v)", tmp, err)
	}

	c := connect(t)
	status, body := create1631(t, c, "no-temp-directory.txt", "text/plain", issue1631SpoolBound+(2<<20))
	if status != http.StatusCreated {
		t.Fatalf("uploading %d bytes with no temporary directory (%s): status %d: %v",
			issue1631SpoolBound+(2<<20), tmp, status, body)
	}
	if got, _ := body["size_bytes"].(float64); int64(got) != issue1631SpoolBound+(2<<20) {
		t.Fatalf("size_bytes = %d, want %d", int64(got), issue1631SpoolBound+(2<<20))
	}
}

// TestIssue1631_TheFilePartMustBeLast pins the order the routes read a form
// in. The file part is handed to the uploader where the walk finds it, so a
// part behind it is metadata that would be dropped without a word. It is
// refused instead, and because the refusal happens while the file is being
// read, the uploader aborts and no resource exists afterwards.
func TestIssue1631_TheFilePartMustBeLast(t *testing.T) {
	c := connect(t)

	name := fmt.Sprintf("acceptance-1631-out-of-order-%d", time.Now().UnixNano())
	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		for field, value := range fields1631(name) {
			if err := w.WriteField(field, value); err != nil {
				return err
			}
		}
		part, err := filePart1631(w, "out-of-order.txt", "text/plain")
		if err != nil {
			return err
		}
		if err := writeFiller1631(part, 4<<20); err != nil {
			return err
		}
		// The part the walk will never reach.
		if err := w.WriteField("tags", "acceptance"); err != nil {
			return err
		}
		return w.Close()
	})

	if status != http.StatusBadRequest {
		t.Fatalf("a form with a part behind the file: status %d, want 400: %v", status, body)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "must be the last part") {
		t.Fatalf("refusal = %q, want it to name the order to send", message)
	}
	if found := findByDisplayName1631(t, c, name); found != "" {
		t.Fatalf("a refused upload created resource %s; nothing may survive it", found)
	}
}

// TestIssue1631_TheToolPathStillStoresWhatItHolds covers the other caller of
// the same write: an agent filing a resource through manage_resource, which is
// holding the bytes rather than streaming them. Both forms its content
// parameter admits are sent -- text in `content`, base64 in `content_base64`.
func TestIssue1631_TheToolPathStillStoresWhatItHolds(t *testing.T) {
	c := connect(t)

	tests := []struct {
		name  string
		args  map[string]any
		bytes int
	}{
		{
			name:  "content as text",
			args:  map[string]any{"content": "day,high\nmon,71\n"},
			bytes: len("day,high\nmon,71\n"),
		},
		{
			name: "content as base64",
			// "day,high\ntue,68\n"
			args:  map[string]any{"content_base64": "ZGF5LGhpZ2gKdHVlLDY4Cg=="},
			bytes: len("day,high\ntue,68\n"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"action":       "create",
				"scope":        "global",
				"path":         "acceptance-1631",
				"filename":     fmt.Sprintf("tool-%d.csv", time.Now().UnixNano()),
				"display_name": fmt.Sprintf("acceptance-1631-tool-%d", time.Now().UnixNano()),
				"description":  "Acceptance #1631: the byte-holding caller of the streaming write.",
				"content_type": "text/csv",
			}
			for k, v := range tt.args {
				args[k] = v
			}
			out := c.call("manage_resource", args)

			id, _ := out["resource_id"].(string)
			if id == "" {
				t.Fatalf("manage_resource create returned no resource_id: %v", out)
			}
			t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
			if got, _ := out["size_bytes"].(float64); int(got) != tt.bytes {
				t.Fatalf("size_bytes = %d, want %d", int(got), tt.bytes)
			}
		})
	}
}

// --- helpers ---

// fields1631 is the metadata a create carries, keyed by form field.
func fields1631(displayName string) map[string]string {
	return map[string]string{
		"scope":        "global",
		"path":         "acceptance-1631",
		"display_name": displayName,
		"description":  "Acceptance #1631: what the streaming upload path stores.",
	}
}

// create1631 uploads a file of exactly size bytes and removes the resource
// afterwards. A declared of "" omits the part's Content-Type header, which is
// the second of the two forms the file part admits.
func create1631(t *testing.T, c *client, filename, declared string, size int64) (int, map[string]any) {
	t.Helper()
	name := fmt.Sprintf("acceptance-1631-%s-%d", filename, time.Now().UnixNano())
	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		return writeUpload1631(w, filename, declared, size, fields1631(name))
	})
	if id, _ := body["id"].(string); id != "" {
		t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	}
	return status, body
}

// replace1631 uploads a replacement of exactly size bytes. The route reads
// only the file, so the form carries nothing else.
func replace1631(t *testing.T, c *client, id, filename, declared string, size int64) (int, map[string]any) {
	t.Helper()
	return send1631(t, c, http.MethodPost, "/api/v1/resources/"+id+"/content", func(w *multipart.Writer) error {
		return writeUpload1631(w, filename, declared, size, nil)
	})
}

// writeUpload1631 composes the multipart body: the metadata fields, then the
// file. The file goes last because that is the order both routes read.
func writeUpload1631(
	w *multipart.Writer, filename, declared string, size int64, fields map[string]string,
) error {
	for field, value := range fields {
		if err := w.WriteField(field, value); err != nil {
			return err
		}
	}
	part, err := filePart1631(w, filename, declared)
	if err != nil {
		return err
	}
	if err := writeFiller1631(part, size); err != nil {
		return err
	}
	return w.Close()
}

// filePart1631 opens the file part, with a declared Content-Type only when one
// is named.
func filePart1631(w *multipart.Writer, filename, declared string) (io.Writer, error) {
	head := make(textproto.MIMEHeader)
	head.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	if declared != "" {
		head.Set("Content-Type", declared)
	}
	return w.CreatePart(head) //nolint:wrapcheck // the caller reports the whole body write
}

// send1631 streams a multipart body through a pipe as the request is sent, so
// a large upload costs the test a fixed buffer rather than the whole file --
// which is also what makes it a real client of a streaming route.
func send1631(
	t *testing.T, c *client, method, path string, body func(*multipart.Writer) error,
) (int, map[string]any) {
	t.Helper()

	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	go func() { _ = pw.CloseWithError(body(w)) }()

	req, err := http.NewRequestWithContext(c.ctx, method, baseURL()+path, pr)
	if err != nil {
		t.Fatalf("building the upload: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	res, err := (&http.Client{Timeout: issue1631Timeout}).Do(req)
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

// filler1631 is the byte pattern every sized upload is made of. It repeats
// over a period that does not divide the transfer manager's part size, so a
// part written twice or dropped changes the digest.
var filler1631 = []byte("mcp-data-platform-1631-")

// writeFiller1631 writes exactly size bytes of that pattern.
func writeFiller1631(part io.Writer, size int64) error {
	const chunk = 1 << 20
	buf := make([]byte, chunk)
	for i := range buf {
		buf[i] = filler1631[i%len(filler1631)]
	}
	for written := int64(0); written < size; {
		n := int64(chunk)
		if remaining := size - written; remaining < n {
			n = remaining
		}
		// The pattern is continuous across chunks, so the offset into it is
		// carried rather than restarted: a chunk boundary must not be a place
		// the file could differ from itself.
		start := int(written % int64(len(filler1631)))
		if _, err := part.Write(rotate1631(buf, start)[:n]); err != nil {
			return err
		}
		written += n
	}
	return nil
}

// rotate1631 returns buf starting at offset within the repeating pattern.
func rotate1631(buf []byte, start int) []byte {
	if start == 0 {
		return buf
	}
	out := make([]byte, len(buf))
	for i := range out {
		out[i] = filler1631[(start+i)%len(filler1631)]
	}
	return out
}

// digestFiller1631 is the sha256 of what writeFiller1631 produces, computed
// the same way the file was written.
func digestFiller1631(size int64) string {
	sum := sha256.New()
	_ = writeFiller1631(sum, size)
	return hex.EncodeToString(sum.Sum(nil))
}

// readContentDigest1631 reads a resource's content route and returns the
// digest and length of what it served, without holding the object.
func readContentDigest1631(t *testing.T, c *client, id string) (string, int64) {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet,
		baseURL()+"/api/v1/resources/"+id+"/content", http.NoBody)
	if err != nil {
		t.Fatalf("building the read: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := (&http.Client{Timeout: issue1631Timeout}).Do(req)
	if err != nil {
		t.Fatalf("reading the content: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reading the content: status %d", res.StatusCode)
	}
	sum := sha256.New()
	n, err := io.Copy(sum, res.Body)
	if err != nil {
		t.Fatalf("reading the content: %v", err)
	}
	return hex.EncodeToString(sum.Sum(nil)), n
}

// findByDisplayName1631 reports the id of a resource with that display name,
// or "" when there is none.
func findByDisplayName1631(t *testing.T, c *client, name string) string {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources?scope=global&limit=200&q="+name, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("listing resources: status %d: %v", status, body)
	}
	rows, _ := body["resources"].([]any)
	for _, row := range rows {
		item, ok := row.(map[string]any)
		if !ok {
			continue
		}
		if display, _ := item["display_name"].(string); display == name {
			id, _ := item["id"].(string)
			return id
		}
	}
	return ""
}

// platformTempDir1631 reads where the running platform's TMPDIR points, from
// the resolved values dev/start.sh records. A run pointed at another
// deployment supplies the same fact through PLATFORM_TMPDIR.
func platformTempDir1631(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("PLATFORM_TMPDIR"); v != "" {
		return v
	}
	file, err := os.Open("../../dev/.dev-ports.env")
	if err != nil {
		t.Fatalf("the dev stack's resolved values are not readable (%v). "+
			"This criterion is about the temporary directory the SERVER has, so a run against another "+
			"deployment has to name it with PLATFORM_TMPDIR", err)
	}
	defer file.Close() //nolint:errcheck // best-effort close after read

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if found && key == "DEV_PLATFORM_TMPDIR" {
			return value
		}
	}
	t.Fatalf("dev/.dev-ports.env carries no DEV_PLATFORM_TMPDIR, so what the server's temporary " +
		"directory is cannot be stated; restart the stack with `make dev`")
	return ""
}
