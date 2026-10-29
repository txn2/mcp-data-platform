//go:build integration

package acceptance

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
)

// Acceptance for #2179: a form field marked sensitive is encrypted before the
// submission reaches the receiver's spool. The SSN a person typed reads as
// ciphertext in trino_query on the form's table, in the raw segment in the
// bucket and in the spool file of the replica that took it; the form's owner
// reads it in plaintext on the submissions route, and an audit row records the
// read; a script whose run's persona the form names decrypts it, and one whose
// persona it does not is refused; and after the key is rotated a submission
// made before the rotation still decrypts.
//
// Wire forms: manage_script create, endpoint_set (decrypt_personas as a list
// of strings) and run_script take their arguments as literal tools/call
// params; a submission is an application/json object; trino_query takes
// string connection, sql and purpose; the submissions and audit routes are
// GETs with query strings.
//
// The spool and the rotation are criteria about one replica, so they run on a
// replica this file starts (issue2121Start): its spool directory is the
// test's own, its object store can be stalled so a spool file stays on disk
// long enough to read, and its encryption keys are the ones the test sets.

const issue2179SSN = "123-45-6789"

// issue2179Fields are an application form's fields, the SSN sensitive.
const issue2179Fields = `[
    {"name": "name", "type": "text", "required": True},
    {"name": "ssn", "type": "text", "required": True, "sensitive": True},
]`

// issue2179Submit shows the form at base and submits name and ssn to it.
func issue2179Submit(t *testing.T, base, form, name, ssn string) {
	t.Helper()
	shown := showForm(t, base, form, "", nil)
	shown.waitToFill()
	status, out := submitForm(t, base, form, shown.fields(map[string]string{"name": name, "ssn": ssn}), true, nil)
	if status != http.StatusOK {
		t.Fatalf("submitting %s at %s: %d %v", form, base, status, out)
	}
}

// issue2179ScriptID is the id of the script named name.
func issue2179ScriptID(t *testing.T, c *client, name string) string {
	t.Helper()
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("get %s carried no id: %v", name, got)
	}
	return id
}

// issue2179Submissions reads the form's submissions at base as key until one
// is named who, or a minute passes, and returns the page.
func issue2179Submissions(t *testing.T, base, key, scriptID, who string) (int, map[string]any) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		got := endpointRequest(t, base, http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/submissions", bearer(key), "")
		var page map[string]any
		_ = json.Unmarshal([]byte(got.body), &page)
		if got.status != http.StatusOK {
			return got.status, page
		}
		subs, _ := page["submissions"].([]any)
		for _, s := range subs {
			m, _ := s.(map[string]any)
			values, _ := m["values"].(map[string]any)
			if values["name"] == who {
				return got.status, map[string]any{"page": page, "values": values}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no submission from %s on %s's submissions: %s", who, scriptID, got.body)
		}
		time.Sleep(time.Second)
	}
}

// issue2179SpoolPayloads are the payloads of every event in the spool files
// under dir, read as the spool frames them: a length and a checksum, then
// the record's JSON, whose events carry their payload base64-encoded.
func issue2179SpoolPayloads(t *testing.T, dir string) (payloads []string, raw []byte) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a file that went away is not one to read
		}
		data, readErr := os.ReadFile(path) // #nosec G304 -- the spool directory this test made
		if readErr != nil {
			return nil //nolint:nilerr // drained while walking
		}
		raw = append(raw, data...)
		for off := 0; off+8 <= len(data); {
			n := int(binary.BigEndian.Uint32(data[off : off+4]))
			if off+8+n > len(data) {
				break
			}
			var rec struct {
				Events []struct {
					Payload []byte `json:"Payload"`
				} `json:"Events"`
			}
			if json.Unmarshal(data[off+8:off+8+n], &rec) == nil {
				for _, e := range rec.Events {
					payloads = append(payloads, string(e.Payload))
				}
			}
			off += 8 + n
		}
		return nil
	})
	return payloads, raw
}

// issue2179Ciphertext fails unless payload holds the SSN as ciphertext and
// nowhere in plaintext.
func issue2179Ciphertext(t *testing.T, where, payload string) {
	t.Helper()
	if strings.Contains(payload, issue2179SSN) {
		t.Fatalf("%s holds the SSN in plaintext: %s", where, payload)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("%s: %v %s", where, err, payload)
	}
	values, _ := p["values"].(map[string]any)
	ssn, _ := values["ssn"].(string)
	if !strings.HasPrefix(ssn, "encb:") || fmt.Sprint(p["sensitive"]) != "[ssn]" {
		t.Fatalf("%s holds ssn %q, sensitive %v; want ciphertext", where, ssn, p["sensitive"])
	}
}

// TestIssue2179_ASensitiveFieldIsCiphertextInTheTableTheBucketAndTheSpool.
func TestIssue2179_ASensitiveFieldIsCiphertextInTheTableTheBucketAndTheSpool(t *testing.T) {
	admin := connect(t)
	form, table := bindForm(t, admin, "acc-2179-store", issue2179Fields, map[string]any{"auth_mode": "public"})
	root, _ := filepath.Abs("../..")
	dir := filepath.Join(root, "build", "2179", "spool-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	store := issue2121NewStore(t)
	store.pause()
	r := issue2121Start(t, dir, store)

	issue2179Submit(t, r.base, form, "Ana", issue2179SSN)
	payloads, raw := issue2179SpoolPayloads(t, dir)
	if len(payloads) != 1 {
		t.Fatalf("the replica's spool holds %d events with its store stalled, want the one submission", len(payloads))
	}
	if strings.Contains(string(raw), issue2179SSN) {
		t.Fatal("the spool file holds the SSN in plaintext")
	}
	issue2179Ciphertext(t, "the spool file", payloads[0])

	store.resume()
	s3 := issue1870S3(t)
	prefix := whlayout.RawPrefix("form-" + form)
	deadline := time.Now().Add(2 * time.Minute)
	keys := issue1870Keys(t, s3, prefix)
	for len(keys) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("nothing was written under %s once the store answered", prefix)
		}
		time.Sleep(time.Second)
		keys = issue1870Keys(t, s3, prefix)
	}
	obj, err := s3.GetObject(context.Background(), issue1870Bucket, keys[0])
	if err != nil {
		t.Fatalf("reading %s: %v", keys[0], err)
	}
	stored, err := whevent.DecodeSegment(obj.Body)
	if err != nil || len(stored) == 0 {
		t.Fatalf("reading the raw segment %s: %v (%d events)", keys[0], err, len(stored))
	}
	issue2179Ciphertext(t, "the raw segment "+keys[0], string(stored[0].Payload))

	rows := formRows(t, admin, table, 1)
	raw2, _ := json.Marshal(rows[0].payload)
	issue2179Ciphertext(t, "trino_query on "+table, string(raw2))
}

// TestIssue2179_TheOwnerReadsThePlaintextAndTheReadIsAudited.
func TestIssue2179_TheOwnerReadsThePlaintextAndTheReadIsAudited(t *testing.T) {
	admin := connect(t)
	form, _ := bindForm(t, admin, "acc-2179-owner", issue2179Fields, map[string]any{"auth_mode": "public"})
	issue2179Submit(t, admin.base, form, "Bo", issue2179SSN)
	status, got := issue2179Submissions(t, admin.base, devAPIKey(), issue2179ScriptID(t, admin, form), "Bo")
	if status != http.StatusOK {
		t.Fatalf("the owner's submissions: %d %v", status, got)
	}
	values, _ := got["values"].(map[string]any)
	page, _ := got["page"].(map[string]any)
	if values["ssn"] != issue2179SSN || fmt.Sprint(page["sensitive"]) != "[ssn]" {
		t.Fatalf("the owner reads ssn %v (sensitive %v), want the plaintext", values["ssn"], page["sensitive"])
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		for _, row := range admin.list("/api/v1/admin/audit/events?event_kind=form_decrypt&per_page=100") {
			m, _ := row.(map[string]any)
			params, _ := m["parameters"].(map[string]any)
			if params["form"] == form && fmt.Sprint(params["fields"]) == "[ssn]" && m["user_email"] == "admin@example.com" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no form_decrypt audit row names %s's ssn read by the owner", form)
		}
		time.Sleep(time.Second)
	}
}

// issue2179Reader is a script, owned by whoever saves it, that decrypts the
// newest submission of form. Its test answers platform.decrypt itself, as an
// author's test does: a recording keeps "[encrypted]" for the value a run
// decrypted, so a test replaying one reads that.
func issue2179Reader(form, table string) string {
	return fmt.Sprintf(`FORM = %q
CONN = %q
SQL = "SELECT payload FROM %s.%s ORDER BY received_at DESC LIMIT 1"

def newest():
    """The newest submission, decrypted."""
    rows = platform.query(connection = CONN, sql = SQL)
    return platform.decrypt(FORM, rows["rows"][0]["payload"])

def main():
    """Decrypts the form's newest submission."""
    platform.result(newest())

def test_newest():
    """Decrypts the payload the query returned."""
    stored = {"values": {"name": "Cy", "ssn": "enc:x"}, "sensitive": ["ssn"]}
    testing.answer("trino_query", {"sql": SQL}, {"columns": ["payload"], "rows": [{"payload": stored}], "stats": {"row_count": 1}}, times = 2)
    testing.answer("platform.decrypt", {"form": FORM, "payload": stored}, {"values": {"name": "Cy", "ssn": "123-45-6789"}, "opened": ["ssn"]}, times = 2)
    assert.eq(newest()["ssn"], "123-45-6789")
    main()
    assert.eq(testing.outputs().result["name"], "Cy")
`, form, formsConn, formsSchema, table)
}

// TestIssue2179_AScriptDecryptsOnlyAsAPersonaTheFormNames: the owner's script
// runs as the collaborator persona; the form naming it, the run reads the SSN,
// and once the form names another persona, the run is refused.
func TestIssue2179_AScriptDecryptsOnlyAsAPersonaTheFormNames(t *testing.T) {
	admin := connect(t)
	form, table := bindForm(t, admin, "acc-2179-script", issue2179Fields, map[string]any{
		"auth_mode": "public", "decrypt_personas": []string{"collaborator"},
	})
	issue2179Submit(t, admin.base, form, "Cy", issue2179SSN)
	formRows(t, admin, table, 1)

	owner := connectAs(t, devOwnerAPIKey)
	reader := endpointName("acc-2179-reader")
	if saved := owner.call("manage_script", map[string]any{"command": "create", "name": reader, "source": issue2179Reader(form, table)}); saved["status"] != "created" {
		t.Fatalf("the reader script was not saved: %v", saved)
	}
	out := owner.call("run_script", map[string]any{"name": reader, "wait_seconds": 120})
	result, _ := out["result"].(map[string]any)
	if out["status"] != "succeeded" || result["ssn"] != issue2179SSN || result["name"] != "Cy" {
		t.Fatalf("a run as a persona the form names: %v", out)
	}

	bindEndpoint(t, admin, form, "", map[string]any{"decrypt_personas": []string{"inventory-analyst"}})
	out = owner.call("run_script", map[string]any{"name": reader, "wait_seconds": 120})
	if out["status"] != "failed" || !strings.Contains(fmt.Sprint(out["error"]), `persona "collaborator"`) {
		t.Fatalf("a run as a persona the form no longer names: want it refused, got %v", out)
	}
}

// issue2179Key is a fresh field-encryption key, as hex.
func issue2179Key(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// issue2179DevKey is the field-encryption key the stack was started with,
// which dev/start.sh records in dev/.dev-ports.env and `make acceptance`
// sources into the environment.
func issue2179DevKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("ENCRYPTION_KEY")
	if key == "" {
		t.Fatal("ENCRYPTION_KEY is not set; `make acceptance` loads it from dev/.dev-ports.env, which `make dev` writes")
	}
	return key
}

// TestIssue2179_ASubmissionMadeBeforeARotationStillDecrypts: a replica whose
// ENCRYPTION_KEY is a new key and whose ENCRYPTION_KEY_PREVIOUS is the stack's
// reads a submission the stack stored, and a submission it stores is under
// the new key, which the stack alone does not open.
func TestIssue2179_ASubmissionMadeBeforeARotationStillDecrypts(t *testing.T) {
	admin := connect(t)
	form, _ := bindForm(t, admin, "acc-2179-rotate", issue2179Fields, map[string]any{"auth_mode": "public"})
	scriptID := issue2179ScriptID(t, admin, form)
	issue2179Submit(t, admin.base, form, "Before", issue2179SSN)
	if status, got := issue2179Submissions(t, admin.base, devAPIKey(), scriptID, "Before"); status != http.StatusOK {
		t.Fatalf("the stack reads its own submission: %d %v", status, got)
	}

	root, _ := filepath.Abs("../..")
	dir := filepath.Join(root, "build", "2179", "spool-rotate-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	r := issue2121Start(t, dir, issue2121NewStore(t), "ENCRYPTION_KEY="+issue2179Key(t), "ENCRYPTION_KEY_PREVIOUS="+issue2179DevKey(t))
	status, got := issue2179Submissions(t, r.base, devAPIKey(), scriptID, "Before")
	values, _ := got["values"].(map[string]any)
	if status != http.StatusOK || values["ssn"] != issue2179SSN {
		t.Fatalf("after the rotation the submission made before it reads %d %v, want the plaintext", status, got)
	}

	issue2179Submit(t, r.base, form, "After", "987-65-4321")
	deadline := time.Now().Add(time.Minute)
	for {
		got := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/submissions", bearer(devAPIKey()), "")
		if got.status == http.StatusInternalServerError && strings.Contains(got.body, "not with a key this deployment holds") {
			return // the new submission is under the new key, which the stack does not hold
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stack, holding only the old key, still read every submission: %d %s", got.status, got.body)
		}
		time.Sleep(time.Second)
	}
}
