//go:build integration

package acceptance

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Issue #1879: an upstream that delivers its data as an archive -- a monthly
// .zip holding one large CSV -- could be downloaded into a managed resource and
// no further. manage_table reads CSV, JSON lines and Parquet, not archives, and
// a script has no decompression and could not hold the member anyway, so the
// feed could not move onto managed scripts at all.
//
// manage_resource action=extract writes an archive's members out as managed
// resources, streaming. What these hold, against the running platform:
//
//   - a zip whose one CSV member is about 500 MB extracts from a managed
//     script run, inside the run's memory budget (dev/platform.yaml sets
//     scripts.worker.max_run_memory to 128MiB), and the stored size is the
//     member's uncompressed size; the member is twice the dev stack's 250 MB
//     upload ceiling, which extraction is not held to;
//   - a members glob selects, a fixed filename with if_exists=replace records
//     the next version of one file, and a table registered over it with
//     follow on serves the new delivery with no re-registration;
//   - a zip-slip name, an encrypted member, a member past the ratio limit and
//     a corrupt archive each fail naming what is wrong, and nothing is left
//     at the destination unreported;
//   - extraction is reachable from platform.call in a run, and from a draft
//     with allow_writes (and refused by a draft without it).
//
// Wire forms: manage_resource's action, reference, path, members, filename and
// if_exists are typed strings, and content_base64 and content_type (used to
// store the small archives) are typed strings; each admits one JSON form and is
// sent in it. manage_script's allow_writes is a boolean, sent as true and
// omitted. manage_table's action, reference, connection and table_name are
// strings. The large archive is stored through POST /api/v1/resources, a
// multipart route whose file part is sent with a declared Content-Type.

const (
	// issue1879BigMember is the uncompressed size the large member is built
	// to, at least; the exact count is what the test compares against.
	issue1879BigMember = 500 << 20
	// issue1879Path is the folder this file's resources live under.
	issue1879Path = "acceptance-1879"
	// issue1879Timeout bounds the large upload and the run that extracts it.
	issue1879Timeout = 10 * time.Minute
)

// issue1879Zip builds a small zip in memory: each entry a name, a body, and
// whether its encryption flag is set or it is stored uncompressed.
type issue1879Entry struct {
	name      string
	body      string
	encrypted bool
	stored    bool
}

func issue1879Zip(t *testing.T, entries ...issue1879Entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.stored {
			h.Method = zip.Store
		}
		if e.encrypted {
			h.Flags |= 0x1
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// issue1879Store files an archive through manage_resource create, as an agent
// holding the bytes would, and removes it when the test ends.
func issue1879Store(t *testing.T, c *client, filename string, data []byte) string {
	t.Helper()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "path": issue1879Path + "/raw", "filename": filename,
		"display_name":   "Acceptance 1879 " + filename,
		"description":    "Acceptance #1879: an archive delivery.",
		"content_base64": base64.StdEncoding.EncodeToString(data), "content_type": "application/zip",
		"if_exists": "replace",
	})
	ref, _ := out["reference"].(string)
	if ref == "" {
		t.Fatalf("storing %s returned no reference: %v", filename, out)
	}
	issue1879Cleanup(t, c, ref)
	return ref
}

// issue1879Cleanup deletes a resource, and any table over it, when the test
// ends.
func issue1879Cleanup(t *testing.T, c *client, ref string) {
	t.Helper()
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref, "force": true})
	})
}

// issue1879Folder lists what is filed under a folder of this test's.
func issue1879Folder(t *testing.T, c *client, folder string) []string {
	t.Helper()
	out := c.call("manage_resource", map[string]any{"action": "list", "path": folder})
	var names []string
	rows, _ := out["resources"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		name, _ := row["filename"].(string)
		names = append(names, name)
	}
	return names
}

func issue1879Members(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, _ := out["members"].([]any)
	members := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, _ := r.(map[string]any)
		members = append(members, m)
	}
	return members
}

// TestIssue1879_ALargeMemberExtractsInsideARun is the first criterion: the
// member is about 500 MB, the run's budget is 128 MiB, and the stored file is
// the member's uncompressed size to the byte.
func TestIssue1879_ALargeMemberExtractsInsideARun(t *testing.T) {
	c := connectFor(t, issue1879Timeout)
	stamp := issue1879Stamp()

	var memberSize int64
	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		for field, value := range map[string]string{
			"scope": "global", "path": issue1879Path + "/raw",
			"display_name": "Acceptance 1879 large delivery " + stamp,
			"description":  "Acceptance #1879: a zip holding one ~500 MB CSV.",
		} {
			if err := w.WriteField(field, value); err != nil {
				return err
			}
		}
		part, err := filePart1631(w, "large-"+stamp+".zip", "application/zip")
		if err != nil {
			return err
		}
		memberSize, err = issue1879WriteLargeZip(part)
		if err != nil {
			return err
		}
		return w.Close()
	})
	if status != http.StatusCreated {
		t.Fatalf("storing the large archive: status %d: %v", status, body)
	}
	archiveID, _ := body["id"].(string)
	archiveRef := "mcp:resource:" + archiveID
	issue1879Cleanup(t, c, archiveRef)
	stored, _ := body["size_bytes"].(float64)
	t.Logf("archive %s: %.0f bytes compressed, member %d bytes", archiveRef, stored, memberSize)

	name := "acc-1879-large-" + stamp
	c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1879: extract a large archive member from a run.",
		"source": fmt.Sprintf(`ext = platform.call("manage_resource", {
    "action": "extract", "reference": %q, "path": %q,
    "members": "*.csv", "filename": "large.csv", "if_exists": "replace",
})
m = ext["members"][0]
print("size=" + str(m["size_bytes"]))
print("reference=" + m["reference"])
print("content_type=" + m["content_type"])
`, archiveRef, issue1879Path+"/staging-"+stamp),
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 540})
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	log, _ := got["log"].(string)
	if got["status"] != "succeeded" {
		t.Fatalf("the run extracting a %d-byte member did not succeed: %v", memberSize, got)
	}
	if ref := issue1879LogValue(log, "reference="); ref != "" {
		issue1879Cleanup(t, c, ref)
	}
	if want := "size=" + strconv.FormatInt(memberSize, 10); !strings.Contains(log, want) {
		t.Errorf("the stored member is not the member's uncompressed size; want %q in the log:\n%s", want, log)
	}
	if !strings.Contains(log, "content_type=text/csv") {
		t.Errorf("a .csv member is stored as text/csv; log:\n%s", log)
	}
	metrics, _ := got["metrics"].(map[string]any)
	peak, _ := metrics["peak_memory_bytes"].(float64)
	t.Logf("run peak_memory_bytes = %.0f", peak)
	if peak >= 128<<20 {
		t.Errorf("the run reports a peak of %.0f bytes, at or past the 128 MiB budget", peak)
	}
}

// issue1879WriteLargeZip streams a zip holding one CSV of at least
// issue1879BigMember bytes and returns its exact uncompressed size. The rows
// vary, so the member compresses the way a real export does rather than past
// the ratio limit.
func issue1879WriteLargeZip(out io.Writer) (int64, error) {
	zw := zip.NewWriter(out)
	f, err := zw.Create("export_2026_10.csv")
	if err != nil {
		return 0, err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	var n int64
	head := "row_id,amount,label\n"
	if _, err := bw.WriteString(head); err != nil {
		return 0, err
	}
	n += int64(len(head))
	line := make([]byte, 0, 64)
	for i := int64(0); n < issue1879BigMember; i++ {
		line = line[:0]
		line = strconv.AppendInt(line, i, 10)
		line = append(line, ',')
		line = strconv.AppendInt(line, (i*7919)%100000, 10)
		line = append(line, '.')
		line = strconv.AppendInt(line, i%97, 10)
		line = append(line, ",label-"...)
		line = strconv.AppendInt(line, (i*31)%1000003, 36)
		line = append(line, '\n')
		if _, err := bw.Write(line); err != nil {
			return 0, err
		}
		n += int64(len(line))
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return n, zw.Close()
}

func issue1879LogValue(log, prefix string) string {
	for _, line := range strings.Split(log, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			return rest
		}
	}
	return ""
}

// TestIssue1879_ARollingDeliveryMovesItsTable is the second criterion: the
// glob picks the CSV and leaves the README, the fixed filename and replace
// record the next version of one file, and the table registered over it once
// serves the new month.
func TestIssue1879_ARollingDeliveryMovesItsTable(t *testing.T) {
	c := connect(t)
	stamp := issue1879Stamp()
	staging := issue1879Path + "/rolling-" + stamp
	extract := func(archiveRef string) map[string]any {
		return c.call("manage_resource", map[string]any{
			"action": "extract", "reference": archiveRef, "path": staging,
			"members": "*.csv", "filename": "delivery.csv", "if_exists": "replace",
		})
	}

	september := issue1879Store(t, c, "september-"+stamp+".zip", issue1879Zip(t,
		issue1879Entry{name: "export_2026_09.csv", body: "month,amount\n9,100\n9,200\n"},
		issue1879Entry{name: "README.txt", body: "not data"},
	))
	first := extract(september)
	members := issue1879Members(t, first)
	if len(members) != 1 {
		t.Fatalf("*.csv selected %d members; want the CSV alone: %v", len(members), first)
	}
	m := members[0]
	ref, _ := m["reference"].(string)
	issue1879Cleanup(t, c, ref)
	if m["member"] != "export_2026_09.csv" || m["filename"] != "delivery.csv" || m["created"] != true {
		t.Fatalf("the first extraction reported %v", m)
	}
	if got := issue1879Folder(t, c, staging); len(got) != 1 || got[0] != "delivery.csv" {
		t.Fatalf("the staging folder holds %v; want delivery.csv alone", got)
	}

	table := "acc_1879_delivery_" + stamp
	reg := c.call("manage_table", map[string]any{
		"action": "register", "reference": ref, "connection": scratchResourceConnection, "table_name": table,
	})
	query, _ := reg["query_table"].(string)
	if query == "" {
		t.Fatalf("registering the extracted CSV returned no query table: %v", reg)
	}
	sum := func() float64 {
		out := c.call("trino_query", map[string]any{
			"connection": scratchResourceConnection,
			"purpose":    "Acceptance #1879: the table over the extracted delivery.",
			"sql":        "SELECT count(*) AS n, max(CAST(month AS integer)) AS month FROM " + query,
		})
		rows, _ := out["rows"].([]any)
		if len(rows) != 1 {
			t.Fatalf("the count returned %v", out)
		}
		row, _ := rows[0].(map[string]any)
		return number(t, row, "month")*1000 + number(t, row, "n")
	}
	if got := sum(); got != 9002 {
		t.Fatalf("the table over September reads month*1000+rows = %v; want 9002", got)
	}

	october := issue1879Store(t, c, "october-"+stamp+".zip", issue1879Zip(t,
		issue1879Entry{name: "export_2026_10.csv", body: "month,amount\n10,1\n10,2\n10,3\n"},
	))
	second := issue1879Members(t, extract(october))
	if len(second) != 1 {
		t.Fatalf("the October extraction wrote %d members", len(second))
	}
	n := second[0]
	if n["reference"] != ref || n["version"] != float64(2) || n["created"] != false {
		t.Fatalf("October was not recorded as version 2 of the same file: %v", n)
	}
	changes, _ := n["table_changes"].([]any)
	if len(changes) == 0 || !strings.Contains(fmt.Sprint(changes), table) {
		t.Errorf("the extraction did not report the table following it: %v", n["table_changes"])
	}
	if got := sum(); got != 10003 {
		t.Errorf("the table did not move to October: month*1000+rows = %v; want 10003", got)
	}
}

// TestIssue1879_BadArchivesAreRefusedAndLeaveNothing is the third criterion.
// Each refusal names what is wrong, and the destination folder is empty after
// it -- except the corrupt second member, where the first member written is
// named in the error and is the only file there.
func TestIssue1879_BadArchivesAreRefusedAndLeaveNothing(t *testing.T) {
	c := connect(t)
	stamp := issue1879Stamp()
	longCSV := strings.Repeat("id,value\n1,2\n", 400)
	for _, tc := range []struct {
		name    string
		archive []byte
		want    string
	}{
		{"zip-slip", issue1879Zip(t,
			issue1879Entry{name: "ok.csv", body: "a\n1\n"},
			issue1879Entry{name: "../../outside.csv", body: "x"}), "climbs out of its folder"},
		{"encrypted", issue1879Zip(t,
			issue1879Entry{name: "secret.csv", body: "a\n1\n", encrypted: true, stored: true}), `"secret.csv" is encrypted`},
		{"ratio", issue1879Zip(t,
			issue1879Entry{name: "zeros.csv", body: strings.Repeat("0", 8<<20)}), "max_ratio"},
		{"truncated", issue1879Truncated(t, issue1879Zip(t,
			issue1879Entry{name: "a.csv", body: longCSV})), "corrupt archive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := issue1879Store(t, c, tc.name+"-"+stamp+".zip", tc.archive)
			dest := issue1879Path + "/refused-" + tc.name + "-" + stamp
			_, text, err := c.callRaw("manage_resource", map[string]any{
				"action": "extract", "reference": ref, "path": dest,
			})
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if !strings.Contains(text, tc.want) || !strings.Contains(text, "Nothing was written.") {
				t.Errorf("the refusal does not say %q and that nothing was written: %s", tc.want, text)
			}
			if got := issue1879Folder(t, c, dest); len(got) != 0 {
				t.Errorf("a refused extraction left %v at the destination", got)
			}
		})
	}

	// A damaged second member: the first is written and named, the second is
	// not stored.
	data := issue1879Zip(t,
		issue1879Entry{name: "first.csv", body: "a\n1\n", stored: true},
		issue1879Entry{name: "second.csv", body: longCSV, stored: true})
	data[bytes.Index(data, []byte(longCSV))+100] ^= 0xff
	ref := issue1879Store(t, c, "damaged-"+stamp+".zip", data)
	dest := issue1879Path + "/damaged-" + stamp
	_, text, err := c.callRaw("manage_resource", map[string]any{"action": "extract", "reference": ref, "path": dest})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got := issue1879Folder(t, c, dest)
	for _, name := range got {
		out := c.call("manage_resource", map[string]any{
			"action": "get", "path": dest, "filename": name,
		})
		rec, _ := out["resource"].(map[string]any)
		if id, _ := rec["resource_id"].(string); id != "" {
			issue1879Cleanup(t, c, "mcp:resource:"+id)
		}
	}
	if !strings.Contains(text, "second.csv") || !strings.Contains(text, "checksum") {
		t.Errorf("the failure does not name the damaged member and the checksum: %s", text)
	}
	if !strings.Contains(text, "first.csv as mcp://") {
		t.Errorf("the failure does not name the member written before it: %s", text)
	}
	if len(got) != 1 || got[0] != "first.csv" {
		t.Errorf("the destination holds %v; want first.csv alone, named in the error", got)
	}
}

// issue1879Stamp is a short run-unique suffix: a folder name is at most 31
// characters, and the stamp goes into several.
func issue1879Stamp() string {
	return strconv.FormatInt(time.Now().UnixNano()%1_000_000_000_000, 36)
}

// issue1879Truncated cuts the end of directory off a zip, which is what a
// delivery interrupted in transit looks like.
func issue1879Truncated(t *testing.T, data []byte) []byte {
	t.Helper()
	return data[:len(data)-30]
}

// TestIssue1879_ADraftExtractsOnlyWithAllowWrites is the fourth criterion's
// draft half: extract is a write, refused by a draft by default and done by
// one that asks.
func TestIssue1879_ADraftExtractsOnlyWithAllowWrites(t *testing.T) {
	c := connect(t)
	stamp := issue1879Stamp()
	ref := issue1879Store(t, c, "draft-"+stamp+".zip", issue1879Zip(t,
		issue1879Entry{name: "draft.csv", body: "a\n1\n"}))
	dest := issue1879Path + "/draft-" + stamp
	name := "acc-1879-draft-" + stamp
	source := fmt.Sprintf(`ext = platform.call("manage_resource", {"action": "extract", "reference": %q, "path": %q})
print("wrote=" + ext["members"][0]["uri"])
`, ref, dest)
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1879: extract from a draft.",
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	barred := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": source})
	if barred["status"] != "failed" || !strings.Contains(fmt.Sprint(barred["error"]), "manage_resource action=extract") {
		t.Fatalf("a draft without allow_writes must stop at the extract and name it: %v", barred)
	}
	if got := issue1879Folder(t, c, dest); len(got) != 0 {
		t.Fatalf("the barred draft wrote %v", got)
	}

	allowed := c.call("manage_script", map[string]any{
		"command": "run_draft", "name": name, "source": source, "allow_writes": true,
	})
	if allowed["status"] != "succeeded" {
		t.Fatalf("run_draft allow_writes=true did not extract: %v", allowed)
	}
	got := issue1879Folder(t, c, dest)
	if len(got) != 1 || got[0] != "draft.csv" {
		t.Fatalf("the allowed draft left %v at the destination; want draft.csv", got)
	}
	out := c.call("manage_resource", map[string]any{"action": "get", "path": dest, "filename": "draft.csv"})
	rec, _ := out["resource"].(map[string]any)
	if id, _ := rec["resource_id"].(string); id != "" {
		issue1879Cleanup(t, c, "mcp:resource:"+id)
	}
}
