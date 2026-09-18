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

// Issue #1775: registering an uploaded CSV as a queryable table failed on two
// files and two accounts during a live customer session. One was the
// byte-order mark #1774 is filed for. The other answered "The registration
// could not be completed" -- no field, no reason, no code -- which is nothing
// for a person to act on and nothing for support to triage from.
//
// Two things are wrong with that answer. The 500 detail named neither the
// stage nor anything else, and behind it the audit trail held no record of the
// attempt at all: a registration that failed before it had corrected the
// person's file was not audited, so a deployment where registrations were
// failing had a table_register history in which every attempt succeeded.
//
// A third thing was reported under the same heading and is the same session's
// experience of the same file: the Edit action did not let the reporter edit
// the source. It did not, and could not -- a managed resource was VIEW-ONLY in
// the portal. Its bytes could only be changed by picking a whole replacement
// file off disk, and the one control named Edit opened the metadata dialog.
//
// What these hold: a platform failure names where it stopped and still never
// carries the driver's own text, every failed registration -- a refusal
// included -- leaves an audit row naming the caller, the connection, the file
// and the error, and a text resource's source can be edited and saved, through
// the route the portal editor posts to, recording a version that says so.
//
// Wire forms. The 500 detail is a property of the REST route the portal's
// Register dialog posts, which takes one JSON object; `repair` and `follow`
// are booleans and `table_name` a string, and the forms they admit are sent by
// the #1774 and #1577 criteria. Nothing here is a tool parameter, so the calls
// below are the route's own shape.

const (
	// audit1775Attempts and audit1775Pause bound the wait for an audit row.
	// The audit store writes on its own drain, so "absent" and "not yet
	// written" read the same and the criterion waits before asserting.
	audit1775Attempts = 20
	audit1775Pause    = 500 * time.Millisecond
)

// TestIssue1775_APlatformFailureNamesWhereItStopped is criterion 1. The file's
// stored object is removed underneath the registration, which is the one
// platform failure a client can cause on demand and is the same shape the
// field failure had: the read of the object did not complete.
func TestIssue1775_APlatformFailureNamesWhereItStopped(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	_, id := file1774(t, c, "content", "1775failure"+stamp, insightsExport1774(insightsRows1774))

	// The object the resource's head points at, taken out from under it.
	key := resourceS3Key1775(t, c, id)
	c.call("s3_object", map[string]any{
		"action": "delete", "connection": "dev-resources",
		"bucket": "managed-resources", "key": key,
		"purpose": "Acceptance #1775: the registration must answer a read it cannot complete.",
	})

	status, body := c.rest(http.MethodPost, "/api/v1/resources/"+id+"/tables",
		jsonBody(t, map[string]any{
			"connection": scratchResourceConnection,
			"table_name": "acc1775fail_" + stamp,
		}))
	if status != http.StatusInternalServerError {
		t.Fatalf("a read the platform could not complete is a platform failure: status %d, %v", status, body)
	}

	detail, _ := body["detail"].(string)
	if detail != "the registration could not be completed while reading the file" {
		t.Fatalf("the failure does not name where it stopped: %q", detail)
	}
	// The stage is the whole of what leaves the server. The store's own text
	// names a bucket, a key and an endpoint, and none of that is the caller's.
	for _, leaked := range []string{"managed-resources", "s3", "NoSuchKey", key} {
		if strings.Contains(detail, leaked) {
			t.Fatalf("the detail carries the store's own text (%q): %q", leaked, detail)
		}
	}
}

// TestIssue1775_EveryFailedRegistrationIsAudited is criterion 2. The refusal
// below corrects nothing and settles nothing -- it is refused on the file
// before a target is resolved -- which is exactly the attempt that used to
// leave no trace.
func TestIssue1775_EveryFailedRegistrationIsAudited(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	table := "acc1775audit_" + stamp
	_, id := file1774(t, c, "content", "1775audit"+stamp,
		[]byte("post_id,he said \"hi\",title\np1,x,Launch\n"))

	status, body := c.rest(http.MethodPost, "/api/v1/resources/"+id+"/tables",
		jsonBody(t, map[string]any{"connection": scratchResourceConnection, "table_name": table}))
	if status < 400 {
		t.Fatalf("an unparseable header is a refusal: status %d, %v", status, body)
	}

	ev := awaitRegisterAudit1775(t, c, table)
	if success, _ := ev["success"].(bool); success {
		t.Fatalf("the failed attempt is recorded as a success: %v", ev)
	}
	if got, _ := ev["connection"].(string); got != scratchResourceConnection {
		t.Fatalf("the event does not name the connection: %v", ev)
	}
	params, _ := ev["parameters"].(map[string]any)
	if got, _ := params["source_id"].(string); got != id {
		t.Fatalf("the event does not name the file: %v", ev)
	}
	// The whole error, which is what the caller was NOT given, is here --
	// which is what makes this the trail support triages from.
	if msg, _ := ev["error_message"].(string); !strings.Contains(msg, "first line cannot be read as a CSV header") {
		t.Fatalf("the event does not carry the error the caller met: %v", ev)
	}
	if email, _ := ev["user_email"].(string); email == "" {
		t.Fatalf("the event names nobody: %v", ev)
	}
}

// --- helpers ---

// resourceS3Key1775 reads where a resource's content is stored now, which is
// the object the registration reads.
func resourceS3Key1775(t *testing.T, c *client, id string) string {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the resource: status %d, %v", status, body)
	}
	key, _ := body["s3_key"].(string)
	if key == "" {
		t.Fatalf("the resource record names no stored object: %v", body)
	}
	return key
}

// awaitRegisterAudit1775 finds the table_register event for one table name.
func awaitRegisterAudit1775(t *testing.T, c *client, table string) map[string]any {
	t.Helper()
	for attempt := range audit1775Attempts {
		if attempt > 0 {
			time.Sleep(audit1775Pause)
		}
		for _, row := range c.list("/api/v1/admin/audit/events?tool_name=table_register&per_page=50") {
			ev, _ := row.(map[string]any)
			params, _ := ev["parameters"].(map[string]any)
			// The recorded name carries the persona prefix the registration
			// takes, so the table asked for is contained rather than equal.
			if name, _ := params["table"].(string); strings.Contains(name, table) {
				return ev
			}
		}
	}
	t.Fatalf("no table_register audit event for %s after %s; a failed registration must leave one",
		table, time.Duration(audit1775Attempts)*audit1775Pause)
	return nil
}

// TestIssue1775_AResourcesSourceIsEditableInPlace is criterion 3, executed
// through the route the portal's Source editor posts to: the edited text goes
// back as the file's next version, the resource keeps its identity, and the
// version says why it exists.
func TestIssue1775_AResourcesSourceIsEditableInPlace(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	_, id := file1774(t, c, "content", "1775edit"+stamp, insightsExport1774(insightsRows1774))

	before := resourceRecord1775(t, c, id)
	edited := "post_id,page_id,title\np1,pg1,Edited in place\n"

	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources/"+id+"/content",
		func(w *multipart.Writer) error {
			// The summary precedes the file, which is the order the route
			// reads: its walk stops at the file part.
			if err := w.WriteField("change_summary", "edited in the portal"); err != nil {
				return err
			}
			return writeBytesUpload1774(w, before.filename, []byte(edited), nil)
		})
	if status != http.StatusOK {
		t.Fatalf("saving an edit: status %d, %v", status, body)
	}

	// The resource is the same resource: every mcp:resource:<id> citation and
	// prompt attachment pointing at it keeps resolving.
	after := resourceRecord1775(t, c, id)
	if after.uri != before.uri || after.filename != before.filename {
		t.Fatalf("the edit moved the resource's identity: %+v -> %+v", before, after)
	}
	if after.size != len(edited) {
		t.Fatalf("the stored file is %d bytes, not the %d saved", after.size, len(edited))
	}

	// And the bytes a reader gets are the edited ones.
	served := resourceContent1775(t, c, id)
	if served != edited {
		t.Fatalf("the resource serves %q, not the edited text", served)
	}

	// The version trail says this was an edit rather than a file picked off
	// disk, which is the whole difference between the two on that panel.
	status, versions := c.rest(http.MethodGet, "/api/v1/resources/"+id+"/versions", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the version history: status %d, %v", status, versions)
	}
	list, _ := versions["versions"].([]any)
	if len(list) < 2 {
		t.Fatalf("the edit recorded no version above the upload: %v", versions)
	}
	var summaries []string
	for _, entry := range list {
		v, _ := entry.(map[string]any)
		if summary, _ := v["change_summary"].(string); summary != "" {
			summaries = append(summaries, summary)
		}
	}
	if len(summaries) != 1 || summaries[0] != "edited in the portal" {
		t.Fatalf("the version history does not name the edit (and only the edit): %v", summaries)
	}
}

// resource1775 is what a criterion reads off a resource record.
type resource1775 struct {
	uri      string
	filename string
	size     int
}

// resourceRecord1775 reads the fields an edit must not move.
func resourceRecord1775(t *testing.T, c *client, id string) resource1775 {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the resource: status %d, %v", status, body)
	}
	uri, _ := body["uri"].(string)
	filename, _ := body["filename"].(string)
	size, _ := body["size_bytes"].(float64)
	return resource1775{uri: uri, filename: filename, size: int(size)}
}

// resourceContent1775 reads the bytes the resource serves now.
func resourceContent1775(t *testing.T, c *client, id string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet,
		baseURL()+"/api/v1/resources/"+id+"/content", http.NoBody)
	if err != nil {
		t.Fatalf("building the content read: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reading the content: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the content body: %v", err)
	}
	return string(raw)
}
