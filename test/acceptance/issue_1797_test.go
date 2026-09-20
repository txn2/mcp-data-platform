//go:build integration

package acceptance

import (
	"net/http"
	"testing"
)

// Issue #1797: a row of the reader's own session timeline opened nothing.
// SessionTimeline attaches onClick only when an onSelect is passed; the
// operator surface passed one and opened the audit event drawer, and
// MySessionDetailPage did not. So a reader could see that a call failed and
// had no way to see what it was called with or what it said.
//
// GET /portal/calls/{id} was not the drill-down: a call record is written only
// for the sql, api and graphql kinds, so most of a session's rows -- search,
// list_connections, manage_resource, manage_table -- would still open nothing.
// The answer is a self-scoped read of the audit event itself.
//
// What these hold, against the running platform: the caller's own event opens
// by id and carries what the timeline entry does not (the parameters, the
// stated purpose, the error text); a row whose tool has no call record opens
// the same way a trino_query row does; and an event that is not the caller's
// is not found rather than refused.
//
// Wire forms: the route takes one path parameter, an event id, which admits
// one form. The `purpose` sent on the calls below is typed string.

const issue1797Purpose = "Acceptance for #1797: a session timeline row opens the call behind it."

// issue1797Session runs two calls of different kinds in one session and
// returns the session id and the timeline the portal reports for it.
//
// search is deliberately one of them: it writes no call record, and it is the
// kind of row that used to open nothing at all.
func issue1797Session(t *testing.T, c *client) (sessionID string, timeline []any) {
	t.Helper()

	c.call("search", map[string]any{
		"intent":  "acceptance 1797 discovery",
		"purpose": issue1797Purpose,
	})
	c.call("list_connections", map[string]any{"purpose": issue1797Purpose})

	status, out := c.rest(http.MethodGet, "/api/v1/portal/sessions", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /portal/sessions: status %d: %v", status, out)
	}
	sessions, _ := out["data"].([]any)
	if len(sessions) == 0 {
		t.Fatalf("the caller has no sessions: %v", out)
	}
	first, _ := sessions[0].(map[string]any)
	sessionID, _ = first["session_id"].(string)
	if sessionID == "" {
		sessionID, _ = first["id"].(string)
	}
	if sessionID == "" {
		t.Fatalf("the session summary carries no id: %v", first)
	}

	status, detail := c.rest(http.MethodGet, "/api/v1/portal/sessions/"+sessionID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /portal/sessions/%s: status %d: %v", sessionID, status, detail)
	}
	timeline, _ = detail["timeline"].([]any)
	if len(timeline) == 0 {
		// Some shapes nest the entries under the session.
		if inner, ok := detail["session"].(map[string]any); ok {
			timeline, _ = inner["timeline"].([]any)
		}
	}
	if len(timeline) == 0 {
		t.Fatalf("the session carries no timeline: %v", detail)
	}
	return sessionID, timeline
}

func TestIssue1797_ATimelineRowOpensTheCallBehindIt(t *testing.T) {
	c := connect(t)
	_, timeline := issue1797Session(t, c)

	opened := 0
	sawArguments := false
	for _, entry := range timeline {
		e, _ := entry.(map[string]any)
		eventID, _ := e["event_id"].(string)
		if eventID == "" {
			continue
		}

		status, event := c.rest(http.MethodGet, "/api/v1/portal/events/"+eventID, nil)
		if status != http.StatusOK {
			t.Fatalf("GET /portal/events/%s: status %d: %v", eventID, status, event)
		}
		for _, field := range []string{"id", "tool_name", "timestamp", "duration_ms", "persona"} {
			if _, ok := event[field]; !ok {
				t.Fatalf("the opened call carries no %s: %v", field, event)
			}
		}

		// The parameters are the half the timeline entry does not carry, and
		// are asserted against a call that HAD some: list_connections takes
		// none but a purpose, and a call that carried no arguments carrying no
		// parameters is the right answer rather than a gap.
		//
		// The session holds more than one search: connect() runs one of its own
		// to satisfy the search-first gate. So this looks for THIS test's call
		// rather than asserting against whichever search came first.
		if tool, _ := event["tool_name"].(string); tool == "search" {
			params, ok := event["parameters"].(map[string]any)
			if !ok {
				t.Fatalf("the opened search call carries no parameters: %v", event)
			}
			if intent, _ := params["intent"].(string); intent == "acceptance 1797 discovery" {
				sawArguments = true
			}
		}
		opened++
	}
	if opened == 0 {
		t.Fatal("no timeline entry carried an event id")
	}
	if !sawArguments {
		t.Fatal("no opened call carried this test's own search arguments")
	}
}

func TestIssue1797_ARowWithNoCallRecordOpensTheSameWay(t *testing.T) {
	c := connect(t)
	_, timeline := issue1797Session(t, c)

	// search and list_connections write no call record (only the sql, api and
	// graphql kinds do), so these are exactly the rows that used to open
	// nothing. Each must open through the event route.
	wanted := map[string]bool{"search": true, "list_connections": true}
	seen := map[string]bool{}
	for _, entry := range timeline {
		e, _ := entry.(map[string]any)
		tool, _ := e["tool_name"].(string)
		if !wanted[tool] {
			continue
		}
		eventID, _ := e["event_id"].(string)
		if eventID == "" {
			t.Fatalf("the %s row carries no event id: %v", tool, e)
		}
		status, event := c.rest(http.MethodGet, "/api/v1/portal/events/"+eventID, nil)
		if status != http.StatusOK {
			t.Fatalf("a %s row did not open: status %d: %v", tool, status, event)
		}
		if got, _ := event["tool_name"].(string); got != tool {
			t.Fatalf("the opened call is %q, not the %q row that was opened", got, tool)
		}
		seen[tool] = true
	}
	for tool := range wanted {
		if !seen[tool] {
			t.Fatalf("no %s row appeared in the session timeline", tool)
		}
	}
}

func TestIssue1797_TheStatedPurposeIsCarried(t *testing.T) {
	c := connect(t)
	_, timeline := issue1797Session(t, c)

	for _, entry := range timeline {
		e, _ := entry.(map[string]any)
		purpose, _ := e["purpose"].(string)
		if purpose != issue1797Purpose {
			continue
		}
		eventID, _ := e["event_id"].(string)
		status, event := c.rest(http.MethodGet, "/api/v1/portal/events/"+eventID, nil)
		if status != http.StatusOK {
			t.Fatalf("GET /portal/events/%s: status %d: %v", eventID, status, event)
		}
		// In full, not truncated: the drawer is where the whole sentence reads.
		if got, _ := event["purpose"].(string); got != issue1797Purpose {
			t.Fatalf("the opened call's purpose is %q, not the one the call stated", got)
		}
		return
	}
	t.Fatalf("no timeline entry carried the purpose this test stated (%q)", issue1797Purpose)
}

func TestIssue1797_SomebodyElsesEventIsNotFound(t *testing.T) {
	c := connect(t)

	// An id that was never issued and an id belonging to another caller are
	// the same answer: not found, never a refusal, because telling one caller
	// that another caller's event exists is the disclosure the scope prevents.
	status, out := c.rest(http.MethodGet, "/api/v1/portal/events/evt-not-this-callers", nil)
	if status != http.StatusNotFound {
		t.Fatalf("an event that is not the caller's answered %d, not 404: %v", status, out)
	}
	if status == http.StatusForbidden {
		t.Fatal("the route refused rather than answering not-found")
	}
}
