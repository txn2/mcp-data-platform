//go:build integration

package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"testing"
	"time"
)

// Issue #1862: loading a library of reference files was one upload dialog per
// file. The portal now sends a batch through the create route one file at a
// time, and the route learned what a re-sent folder needs: if_exists=
// skip_unchanged leaves a file whose bytes it already holds alone and records
// a changed one as that file's next version, and every create says which of
// created, revised or unchanged it did.
//
// The criteria run against the dev stack, whose managed resources are stored
// on MinIO over TLS (dev/platform.yaml), the backend a deployment runs.
//
// Wire forms. This ticket touches no MCP tool parameter. Its surface is the
// multipart create route, and every form the fields it reads admit is sent:
//
//   - the file part with a declared Content-Type (what a browser sends for a
//     type it knows) and without one (what it sends for a type it does not,
//     such as EPS);
//   - if_exists absent, "fail", "skip_unchanged", and a value the route does
//     not take;
//   - the shared fields a batch repeats on every file (scope, path, tags,
//     description), with a per-file display_name.

// folder1862 is a folder no earlier run used, so a criterion's files are the
// only ones in it. A folder segment starts with a letter.
func folder1862() string {
	return "a1862-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// file1862 is one file of a batch: its name, its declared type (empty sends
// none) and its bytes.
type file1862 struct {
	name, declared string
	content        []byte
}

// upload1862 sends one file to the create route as the bulk upload does:
// every field ahead of the file part, if_exists included when set.
func upload1862(t *testing.T, c *client, folder, ifExists, displayName string, f file1862) (int, map[string]any) {
	t.Helper()
	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		fields := [][2]string{
			{"scope", "global"}, {"path", folder}, {"display_name", displayName},
			{"description", "Acceptance #1862: " + displayName}, {"tags", "brand"}, {"tags", "acceptance-1862"},
		}
		if ifExists != "" {
			fields = append(fields, [2]string{"if_exists", ifExists})
		}
		for _, kv := range fields {
			if err := w.WriteField(kv[0], kv[1]); err != nil {
				return err
			}
		}
		head := make(textproto.MIMEHeader)
		head.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, f.name))
		if f.declared != "" {
			head.Set("Content-Type", f.declared)
		}
		part, err := w.CreatePart(head)
		if err != nil {
			return err
		}
		if _, err := part.Write(f.content); err != nil {
			return err
		}
		return w.Close()
	})
	if id, _ := body["id"].(string); id != "" && status == http.StatusCreated {
		t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	}
	return status, body
}

func versions1862(t *testing.T, c *client, id string) []map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources/"+id+"/versions", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the versions of %s: status %d: %v", id, status, body)
	}
	raw, _ := body["versions"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		m, _ := v.(map[string]any)
		out = append(out, m)
	}
	return out
}

func hex1862(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// A mixed batch, some types declared and some not, lands in one folder with
// the shared tags, each file reported created and hashed.
func TestIssue1862_AMixedBatchIsFiledInOneFolder(t *testing.T) {
	c := connect(t)
	folder := folder1862()
	batch := []file1862{
		{"logo-color.png", "image/png", []byte("\x89PNG\r\n\x1a\n-acceptance-1862-color")},
		{"logo-reversed.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)},
		{"logo-source.eps", "", []byte("%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 0 0 1 1\n")},
	}
	for _, f := range batch {
		status, body := upload1862(t, c, folder, "skip_unchanged", f.name, f)
		if status != http.StatusCreated || body["outcome"] != "created" {
			t.Fatalf("%s: status %d outcome %v, want 201 created: %v", f.name, status, body["outcome"], body)
		}
		id, _ := body["id"].(string)
		vs := versions1862(t, c, id)
		if len(vs) != 1 || vs[0]["content_sha256"] != hex1862(f.content) {
			t.Fatalf("%s: versions %v, want one carrying sha256 %s", f.name, vs, hex1862(f.content))
		}
	}

	status, list := c.rest(http.MethodGet, "/api/v1/resources?path="+folder+"&tag=acceptance-1862", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("listing the folder: status %d: %v", status, list)
	}
	if total, _ := list["total"].(float64); int(total) != len(batch) {
		t.Fatalf("the folder holds %v resources tagged acceptance-1862, want %d: %v", list["total"], len(batch), list)
	}
}

// Re-sending a file whose bytes are stored writes nothing and says so.
func TestIssue1862_AnUnchangedFileIsSkipped(t *testing.T) {
	c := connect(t)
	folder := folder1862()
	f := file1862{"product-001.png", "image/png", []byte("\x89PNG\r\n\x1a\n-product-001")}
	_, first := upload1862(t, c, folder, "skip_unchanged", "Product 001", f)
	id, _ := first["id"].(string)
	if id == "" {
		t.Fatalf("the first upload returned no id: %v", first)
	}

	status, body := upload1862(t, c, folder, "skip_unchanged", "Product 001 again", f)
	if status != http.StatusOK || body["outcome"] != "unchanged" || body["id"] != id {
		t.Fatalf("re-upload: status %d %v, want 200 unchanged for %s", status, body, id)
	}
	if body["display_name"] != "Product 001" {
		t.Errorf("display_name = %v, want the stored one", body["display_name"])
	}
	if vs := versions1862(t, c, id); len(vs) != 1 {
		t.Fatalf("versions = %d, want 1: nothing is recorded for an unchanged file", len(vs))
	}
}

// Re-sending a file whose bytes changed records them as its next version,
// keeping the id, and a later identical send is unchanged.
func TestIssue1862_AChangedFileBecomesTheNextVersion(t *testing.T) {
	c := connect(t)
	folder := folder1862()
	_, first := upload1862(t, c, folder, "", "Product 002",
		file1862{"product-002.png", "image/png", []byte("\x89PNG\r\n\x1a\n-product-002-v1")})
	id, _ := first["id"].(string)
	changed := file1862{"product-002.png", "image/png", []byte("\x89PNG\r\n\x1a\n-product-002-v2")}

	status, body := upload1862(t, c, folder, "skip_unchanged", "Product 002", changed)
	if status != http.StatusOK || body["outcome"] != "revised" || body["id"] != id {
		t.Fatalf("changed re-upload: status %d %v, want 200 revised for %s", status, body, id)
	}
	vs := versions1862(t, c, id)
	if len(vs) != 2 || vs[0]["content_sha256"] != hex1862(changed.content) {
		t.Fatalf("versions = %v, want two, the newest carrying sha256 %s", vs, hex1862(changed.content))
	}

	status, body = upload1862(t, c, folder, "skip_unchanged", "Product 002", changed)
	if status != http.StatusOK || body["outcome"] != "unchanged" {
		t.Fatalf("repeat: status %d %v, want 200 unchanged", status, body)
	}
}

// One refused file in a batch does not take the others with it.
func TestIssue1862_ARefusedFileLeavesTheBatchStored(t *testing.T) {
	c := connect(t)
	folder := folder1862()
	status, body := upload1862(t, c, folder, "skip_unchanged", "Installer",
		file1862{"setup.exe", "application/octet-stream", []byte("MZ")})
	if status != http.StatusBadRequest {
		t.Fatalf("an .exe: status %d %v, want 400", status, body)
	}
	status, body = upload1862(t, c, folder, "skip_unchanged", "Product 003",
		file1862{"product-003.png", "image/png", []byte("\x89PNG\r\n\x1a\n-product-003")})
	if status != http.StatusCreated {
		t.Fatalf("the next file: status %d %v, want 201", status, body)
	}
}

// Without skip_unchanged a taken address is refused as it always was, and a
// value the route does not take is refused rather than read as the default.
func TestIssue1862_TheDefaultAndUnknownValuesRefuse(t *testing.T) {
	c := connect(t)
	folder := folder1862()
	f := file1862{"product-004.png", "image/png", []byte("\x89PNG\r\n\x1a\n-product-004")}
	if status, body := upload1862(t, c, folder, "", "Product 004", f); status != http.StatusCreated {
		t.Fatalf("creating: status %d %v", status, body)
	}
	for _, v := range []string{"", "fail"} {
		if status, body := upload1862(t, c, folder, v, "Product 004", f); status != http.StatusConflict {
			t.Errorf("if_exists %q on a taken address: status %d %v, want 409", v, status, body)
		}
	}
	if status, body := upload1862(t, c, folder, "overwrite", "Product 004", f); status != http.StatusBadRequest {
		t.Errorf("if_exists overwrite: status %d %v, want 400", status, body)
	}
}
