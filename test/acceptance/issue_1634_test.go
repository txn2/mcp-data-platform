//go:build integration

package acceptance

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1634: a table registration read the object it was pointed at by a
// compiled-in 100 MB (`tableregister.DefaultMaxBytes`), while the write routes
// read by resources.managed.max_upload_bytes. #1628 made that ceiling
// configurable and the constant did not follow, so a deployment that raised it
// stored a CSV and then refused to register a table over it with "the file is
// larger than the 100 MB a registration reads" -- a number the operator never
// set. Registering is what a large CSV is stored for, so a file that uploads
// and cannot be registered has not reached what it was uploaded for.
//
// Wire forms: this ticket touches no MCP tool parameter and adds none. The
// surfaces are the multipart upload route (whose `file` part is bytes and
// whose metadata fields are form-encoded strings, one form each) and
// `manage_table action=register`, whose `reference`, `connection` and
// `table_name` are strings and whose `follow` is a bool -- one JSON form each,
// all sent below as literal tools/call params. The one number under test is
// read from the server (`GET /api/v1/portal/me`) rather than assumed, so the
// suite states the deployment's ceiling instead of hard-coding one.

const (
	// issue1634RegistrationDefault is the compiled-in bound a registration
	// used, and the number a refusal must NOT name on a deployment that
	// configured something else.
	issue1634RegistrationDefault = "100 MB"
	// issue1634Timeout bounds one upload plus the registration that reads it.
	issue1634Timeout = 5 * time.Minute
)

// TestIssue1634_AFileAboveTheOldConstantRegisters is the criterion the ticket
// exists for. The file is larger than the compiled-in 100 MB and smaller than
// this deployment's ceiling, which is the whole gap the defect lived in.
func TestIssue1634_AFileAboveTheOldConstantRegisters(t *testing.T) {
	c := connect(t)
	ceiling := deploymentCeiling1634(t, c)
	const past = 120 << 20 // past the old constant
	if ceiling <= past {
		t.Fatalf("this deployment's ceiling is %d, which is not above the %d-byte constant: "+
			"the gap the defect lived in does not exist here, so this criterion cannot run",
			ceiling, past)
	}

	id, reference := uploadCSV1634(t, c, past)
	if id == "" {
		t.Fatalf("the upload returned no id")
	}

	table := fmt.Sprintf("acc_1634_%d", time.Now().UnixNano())
	out := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference,
		"connection": scratchResourceConnection, "table_name": table,
	})
	t.Cleanup(func() {
		if rid, _ := out["registration_id"].(string); rid != "" {
			_, _, _ = c.callRaw("manage_table",
				map[string]any{"action": "unregister", "registration_id": rid})
		}
	})
	if rid, _ := out["registration_id"].(string); rid == "" {
		t.Fatalf("registering a %d-byte CSV returned no registration: %v", past, out)
	}
}

// TestIssue1634_TheRefusalNamesTheDeploymentsNumber is the criterion in the
// other direction. A file past this deployment's own ceiling is still refused,
// and by the number the operator set rather than by the constant.
func TestIssue1634_TheRefusalNamesTheDeploymentsNumber(t *testing.T) {
	c := connect(t)
	ceiling := deploymentCeiling1634(t, c)

	// Past the ceiling, so the WRITE route refuses it and names the same
	// number. Upload and registration are one bound now, which is the property
	// that stops a file being taken by one surface and refused by the other.
	status, body := sendUpload1634(t, c, ceiling+(1<<20))
	if status != http.StatusBadRequest {
		t.Fatalf("uploading past the ceiling: status %d, want 400: %v", status, body)
	}
	message, _ := body["error"].(string)
	named := describeCeiling1634(ceiling)
	if !strings.Contains(message, named) {
		t.Fatalf("refusal = %q, want it to name this deployment's ceiling (%s)", message, named)
	}
	if named != issue1634RegistrationDefault && strings.Contains(message, issue1634RegistrationDefault) {
		t.Fatalf("refusal = %q, but this deployment does not refuse at %s",
			message, issue1634RegistrationDefault)
	}
}

// --- helpers ---

// deploymentCeiling1634 reads the ceiling from the server rather than assuming
// one, so the suite states the deployment it is pointed at.
func deploymentCeiling1634(t *testing.T, c *client) int64 {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/portal/me: status %d: %v", status, body)
	}
	got, ok := body["max_upload_bytes"].(float64)
	if !ok || got <= 0 {
		t.Fatalf("/me carries no usable max_upload_bytes: %v", body)
	}
	return int64(got)
}

// describeCeiling1634 renders a ceiling the way a refusal names it, matching
// resource.DescribeUploadLimit for the whole-megabyte values a deployment sets.
func describeCeiling1634(limit int64) string {
	const mb = 1 << 20
	if limit%mb == 0 {
		return fmt.Sprintf("%d MB", limit/mb)
	}
	return fmt.Sprintf("%.1f MB", float64(limit)/float64(mb))
}

// uploadCSV1634 stores a CSV of about size bytes and returns its id and the
// mcp:resource reference a registration is made against.
func uploadCSV1634(t *testing.T, c *client, size int64) (id, reference string) {
	t.Helper()
	status, body := sendUpload1634(t, c, size)
	if status != http.StatusCreated {
		t.Fatalf("uploading a %d-byte CSV: status %d: %v", size, status, body)
	}
	id, _ = body["id"].(string)
	if id == "" {
		t.Fatalf("the upload returned no id: %v", body)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	// The registration takes the reference form, not the canonical URI: the
	// URI addresses the resource for a reader, `mcp:resource:<id>` names the
	// record a registration is made against.
	return id, "mcp:resource:" + id
}

// sendUpload1634 posts a CSV of about size bytes, streamed through a pipe so
// the test costs a fixed buffer. The metadata fields go first: the route reads
// the form in order and streams the file part where it finds it (#1631).
func sendUpload1634(t *testing.T, c *client, size int64) (int, map[string]any) {
	t.Helper()
	name := fmt.Sprintf("acceptance-1634-%d", time.Now().UnixNano())
	return send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		for field, value := range map[string]string{
			"scope":        "global",
			"path":         "acceptance-1634",
			"display_name": name,
			"description":  "Acceptance #1634: a CSV past the old registration constant.",
		} {
			if err := w.WriteField(field, value); err != nil {
				return err
			}
		}
		part, err := filePart1631(w, name+".csv", "text/csv")
		if err != nil {
			return err
		}
		if err := writeCSVBody1634(part, size); err != nil {
			return err
		}
		return w.Close()
	})
}

// writeCSVBody1634 writes a real CSV of about size bytes: a header row and
// then whole records. It has to be a readable CSV rather than filler, because
// a registration takes the header row from it and asks whether a line-based
// reader can read the rest -- which is the read this ticket is about.
func writeCSVBody1634(part io.Writer, size int64) error {
	if _, err := io.WriteString(part, "store_id,units,region\n"); err != nil {
		return err
	}
	var chunk strings.Builder
	for i := range 4096 {
		fmt.Fprintf(&chunk, "%d,%d,region-%d\n", i, i%997, i%17)
	}
	block := chunk.String()
	for written := int64(len("store_id,units,region\n")); written < size; {
		if _, err := io.WriteString(part, block); err != nil {
			return err
		}
		written += int64(len(block))
	}
	return nil
}
