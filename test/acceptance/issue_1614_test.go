//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1614: a deployment's automated machinery calls the same tools its
// people do, and every one of those calls was cataloged and embedded. A
// deployment now names the personas that are machinery, and their calls are
// audited and not cataloged.
//
// The dev stack carries one: `acme-ingest-key`, whose persona `ingest-service`
// is named in `calls.exclude_personas` (dev/platform.yaml). The control is any
// ordinary caller making the same call against the same connection.
const (
	devIngestAPIKey  = "acme-ingest-key"
	devIngestPersona = "ingest-service"
	ingestPath       = "/v1/pagination/link"
)

// fetchOnce makes one cataloged API call and reports whether the platform
// stamped the call's own mcp:call:<id> reference on the result. What each
// criterion reads afterwards is the session's audit rows and call records.
func fetchOnce(c *client, purpose string) (citable bool) {
	c.t.Helper()
	// callRaw rather than call: this criterion needs the whole result and not
	// its first block. The rate-limit wait is callRaw's own.
	res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": apiTestConnection,
		"method":     "GET",
		"path":       ingestPath,
		"purpose":    purpose,
	})
	if err != nil {
		c.t.Fatalf("api_invoke_endpoint: transport error: %v", err)
	}
	if res.IsError {
		c.t.Fatalf("api_invoke_endpoint: tool error: %s", text)
	}
	for _, content := range res.Content {
		text, ok := content.(*mcp.TextContent)
		if ok && strings.Contains(text.Text, "call_reference") {
			return true
		}
	}
	return false
}

// list reads a paged admin or portal collection as this client's identity and
// returns its rows.
func (c *client) list(path string) []any {
	c.t.Helper()
	status, body := c.rest("GET", path, nil)
	if status != 200 {
		c.t.Fatalf("GET %s: status %d, body %v", path, status, body)
	}
	rows, _ := body["data"].([]any)
	return rows
}

const (
	recordAttempts   = 20
	recordRetryPause = 250 * time.Millisecond
)

// awaitRecord waits for the call record of one session's call and returns it.
//
// A record is written on the audit writer's single drain goroutine after the
// call has answered, so a read taken the moment the tool returns can outrun it.
// Every criterion here is about a record being absent, and "absent" and "not
// yet written" are the same read: the suite waits for a record it expects
// before asserting about one it does not.
func awaitRecord(admin *client, sessionID string) map[string]any {
	admin.t.Helper()
	for attempt := range recordAttempts {
		if attempt > 0 {
			time.Sleep(recordRetryPause)
		}
		if rows := admin.list("/api/v1/admin/calls?session_id=" + sessionID); len(rows) > 0 {
			rec, _ := rows[0].(map[string]any)
			return rec
		}
	}
	admin.t.Fatalf("no call record for session %s after %s", sessionID,
		time.Duration(recordAttempts)*recordRetryPause)
	return nil
}

// drainPast makes an ordinary caller's identical call and waits for its record.
//
// It is the barrier the absence criteria stand on. The platform drains audit
// through one goroutine in call order, so a record for a call made after the
// excluded one proves the excluded call's turn has already passed: the catalog
// is then empty of it because it was declined, not because it is behind.
func drainPast(t *testing.T, admin *client, purpose string) *barrierCaller {
	t.Helper()
	person := connectAs(t, devOwnerAPIKey)
	citable := fetchOnce(person, purpose)
	awaitRecord(admin, person.sessionID)
	return &barrierCaller{client: person, citedLastCall: citable}
}

// barrierCaller is the ordinary caller a criterion reads as its control: its
// session, and whether its own call came back citable.
type barrierCaller struct {
	*client
	citedLastCall bool
}

// TestIssue1614_AnExcludedPrincipalsCallIsAuditedAndNotCataloged is criteria 1
// and 5: the audit row is written and complete, and no call record exists.
func TestIssue1614_AnExcludedPrincipalsCallIsAuditedAndNotCataloged(t *testing.T) {
	admin := connect(t)
	machine := connectAs(t, devIngestAPIKey)
	citable := fetchOnce(machine, "Acceptance #1614: an automated principal fetches one upstream page.")
	person := drainPast(t, admin, "Acceptance #1614: the barrier call an ordinary caller makes.")

	// No citation token: the reference would name a record the catalog
	// declined to write, so an agent told to cite it would store a citation
	// that can never be satisfied.
	if citable {
		t.Error("an excluded principal's result carries a call reference that resolves to nothing")
	}
	if !person.citedLastCall {
		t.Error("an ordinary caller's result must still carry its call reference")
	}

	// The audit row is written, and written whole: an operator can still see
	// exactly what the automated system did, which is what makes declining to
	// catalog it safe.
	events := admin.list("/api/v1/admin/audit/events?session_id=" + machine.sessionID +
		"&tool_name=api_invoke_endpoint&per_page=50")
	if len(events) == 0 {
		t.Fatal("an excluded principal's call must still be audited; no audit event for the session")
	}
	ev, _ := events[0].(map[string]any)
	for field, want := range map[string]string{
		"tool_name": "api_invoke_endpoint",
		"persona":   devIngestPersona,
	} {
		if got, _ := ev[field].(string); got != want {
			t.Errorf("audit event %s = %v, want %q (event: %v)", field, ev[field], want, ev)
		}
	}
	for _, field := range []string{"id", "user_id", "timestamp", "duration_ms", "connection"} {
		if ev[field] == nil || ev[field] == "" {
			t.Errorf("audit event field %s is empty; the audit row must be complete: %v", field, ev)
		}
	}

	if records := admin.list("/api/v1/admin/calls?session_id=" + machine.sessionID); len(records) != 0 {
		t.Errorf("an excluded principal's call must not be cataloged, got %d record(s): %v",
			len(records), records)
	}
}

// TestIssue1614_AnOrdinaryCallersSameCallIsStillCataloged is criterion 2: the
// exclusion is about who called, not which tool.
func TestIssue1614_AnOrdinaryCallersSameCallIsStillCataloged(t *testing.T) {
	admin := connect(t)
	person := connectAs(t, devOwnerAPIKey)
	fetchOnce(person, "Acceptance #1614: an ordinary caller fetches the same upstream page.")

	rec := awaitRecord(admin, person.sessionID)
	if got, _ := rec["kind"].(string); got != "api" {
		t.Errorf("record kind = %v, want api (record: %v)", rec["kind"], rec)
	}
	if got, _ := rec["persona"].(string); got == devIngestPersona {
		t.Errorf("the control record must not be the excluded persona's: %v", rec)
	}
}

// TestIssue1614_NoSurfaceReturnsAnExcludedCall is criterion 6: the rule is
// stated once, at the recorder, so nothing that reads the catalog can return a
// record it declined to write. Both catalog readers are asked: the operator's
// admin listing and the caller's own portal listing.
func TestIssue1614_NoSurfaceReturnsAnExcludedCall(t *testing.T) {
	admin := connect(t)
	machine := connectAs(t, devIngestAPIKey)
	fetchOnce(machine, "Acceptance #1614: an automated principal's call is on no catalog surface.")
	person := drainPast(t, admin, "Acceptance #1614: the barrier call an ordinary caller makes.")

	if records := admin.list("/api/v1/admin/calls?session_id=" + machine.sessionID); len(records) != 0 {
		t.Errorf("the admin call catalog returned %d excluded record(s): %v", len(records), records)
	}

	// The caller's own view of its calls, read as the automated principal
	// itself: the surface scopes to the caller, so this is the one listing
	// that would show the record if it existed anywhere. The ordinary caller
	// reads the same route for its own call, so the route is shown to answer
	// at all rather than to answer nothing for everyone.
	if own := machine.list("/api/v1/portal/calls?session_id=" + machine.sessionID); len(own) != 0 {
		t.Errorf("the caller's own call catalog returned %d excluded record(s): %v", len(own), own)
	}
	if own := person.list("/api/v1/portal/calls?session_id=" + person.sessionID); len(own) == 0 {
		t.Error("the portal call catalog returned nothing for an ordinary caller's own call")
	}
}

// TestIssue1614_AnExcludedCallIsAbsentFromSearch is criterion 4: absent from
// the calls search source, because there is no row to find rather than a row
// hidden at read time.
func TestIssue1614_AnExcludedCallIsAbsentFromSearch(t *testing.T) {
	// A nonce, not a sentence: the calls source ranks by meaning as well as by
	// words, and a dev stack carries hundreds of records whose purposes read
	// like this one. A token nothing else contains is what makes the lexical
	// arm rank the record first and makes "not found" mean not recorded.
	marker := fmt.Sprintf("acceptance1614nonce%d", time.Now().UnixNano())
	machine := connectAs(t, devIngestAPIKey)
	fetchOnce(machine, marker)

	// The control runs first and is the barrier: it is made after the excluded
	// call, so its record being findable proves the excluded call's turn on the
	// audit writer has passed. It is made by the peer rather than the owner
	// because the calls source scopes to the caller and ranks by meaning as
	// well as by words: against an identity carrying a backlog of similar
	// purposes, the semantic arm fills the page before the exact lexical match
	// is reached.
	person := connectAs(t, devPeerAPIKey)
	fetchOnce(person, marker)
	if !searchFindsCall(person, marker) {
		t.Fatal("an ordinary caller's call must be searchable in the calls source; the control found nothing")
	}

	if searchFindsCall(machine, marker) {
		t.Error("an excluded principal's call is searchable in the calls source")
	}
}

// searchFindsCall reports whether the caller's own calls search returns a
// record carrying the marker, retrying while the record may still be in flight.
func searchFindsCall(c *client, marker string) bool {
	c.t.Helper()
	for attempt := range recordAttempts {
		if attempt > 0 {
			time.Sleep(recordRetryPause)
		}
		for _, hit := range callHits(c.call("search", map[string]any{
			"intent":  marker,
			"sources": []any{"calls"},
			"limit":   25,
			"purpose": "Acceptance #1614: read the calls source for one recorded call.",
		})) {
			if text, _ := hit["text"].(string); strings.Contains(text, marker) {
				return true
			}
		}
	}
	return false
}

// callHits returns the hits a search returned from the calls source.
func callHits(out map[string]any) []map[string]any {
	var hits []map[string]any
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		if source, _ := group["source"].(string); source != "calls" {
			continue
		}
		raw, _ := group["hits"].([]any)
		for _, h := range raw {
			if hit, ok := h.(map[string]any); ok {
				hits = append(hits, hit)
			}
		}
	}
	return hits
}
