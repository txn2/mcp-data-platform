//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2586: the Trino cost prompt asked a person to approve how many rows
// a table scan READ (an aggregate returning 60 rows prompted as a 1.4M-row
// result), it was on by default, the PII prompt counted PII columns a
// statement never read, an unanswered prompt was reported as the person
// declining, and the call that issued a prompt was audited as a successful
// query. Now: the cost prompt is off by default and, when a connection turns
// it on, counts the rows the query returns, capped by the row limit the call
// runs with; the PII prompt counts only the columns a statement reads; a
// prompt with no answer says the person was not reached (prompt_unanswered);
// and the prompting call is audited as input_required, never a success.
//
// Against the dev stack's Trino (make dev with Trino) and the catalog
// dev/seed-catalog.sql seeds. The cost criteria query acme-costed, whose own
// elicitation block in dev/platform.yaml turns the cost prompt on at a
// threshold of 500; the other dev connections set none, so they show the
// default.
// The client speaks 2026-07-28, so prompts arrive as input requests and the
// client answers by retrying the call.
//
// Wire forms: trino_query's sql and connection are typed string and admit one
// JSON form each; limit is typed integer and is sent as a JSON number, and
// omitted (the connection's default limit). The answers travel as
// inputResponses on the retried call, which the SDK client builds.

const issue2586Purpose = "Acceptance for #2586: the cost prompt counts the rows a query returns."

// issue2586Costed is the dev Trino connection whose own elicitation block
// turns the cost prompt on at 500 returned rows (dev/platform.yaml). A
// connection's elicitation block is file configuration: the admin API drops
// it from a stored connection as a key the platform owns.
const issue2586Costed = "acme-costed"

func (ec *issue2052Client) queryOn(t *testing.T, connection, sql string, extra map[string]any) (bool, string) {
	t.Helper()
	args := map[string]any{"connection": connection, "sql": sql, "purpose": issue2586Purpose}
	for k, v := range extra {
		args[k] = v
	}
	res, text, err := ec.callRaw("trino_query", args)
	if err != nil {
		t.Fatalf("trino_query: transport error: %v", err)
	}
	return res.IsError, text
}

// TestIssue2586_TheCostPromptIsOffByDefault: a dev connection sets no
// elicitation block, and a 1,100,000-row scan runs with no prompt.
func TestIssue2586_TheCostPromptIsOffByDefault(t *testing.T) {
	ec := connect2052(t, "")
	table := issue2052Large(t, ec.client)
	failed, text := ec.queryOn(t, issue2052Reader, "SELECT n FROM "+table, nil)
	if failed {
		t.Fatalf("the query did not run: %s", text)
	}
	if asked := ec.prompts(); len(asked) != 0 {
		t.Errorf("the default settings asked %q", asked)
	}
}

// TestIssue2586_TheCostPromptCountsTheRowsReturned: on a connection with the
// prompt at 500 rows, a scan of the 1,100,000-row table is asked about as the
// 1,000 rows its default limit returns; the same scan with limit 100 returns
// at most 100 and is not asked about; an aggregate returning ten rows is not
// asked about though it reads every row; declining stops the query and
// accepting runs it; each estimate's EXPLAIN is counted.
func TestIssue2586_TheCostPromptCountsTheRowsReturned(t *testing.T) {
	ec := connect2052(t, "")
	table := issue2052Large(t, ec.client)
	conn := issue2586Costed
	before := issue2052ExplainCount(t)

	failed, text := ec.queryOn(t, conn, "SELECT n FROM "+table, nil)
	asked := ec.prompts()
	if len(asked) != 1 || !strings.Contains(asked[0], "return about 1,000 rows (threshold: 500)") {
		t.Fatalf("the client was asked %q; want one prompt naming the 1,000 rows the default limit returns", asked)
	}
	if !failed || !strings.Contains(text, "did not approve the estimated row count") {
		t.Fatalf("a declined prompt did not stop the query: %s", text)
	}

	if failed, text := ec.queryOn(t, conn, "SELECT n FROM "+table, map[string]any{"limit": 100}); failed {
		t.Fatalf("a query its limit holds to 100 rows did not run: %s", text)
	}
	if failed, text := ec.queryOn(t, conn, "SELECT n % 10 AS d, count(*) AS c FROM "+table+" GROUP BY 1", nil); failed {
		t.Fatalf("an aggregate returning ten rows did not run: %s", text)
	}
	if asked := ec.prompts(); len(asked) != 1 {
		t.Errorf("prompts = %q; neither the limited scan nor the aggregate should ask", asked)
	}

	ec.setAnswer("accept")
	if failed, text := ec.queryOn(t, conn, "SELECT n FROM "+table, nil); failed {
		t.Fatalf("an accepted prompt did not run the query: %s", text)
	}
	if after := issue2052ExplainCount(t); after < before+4 {
		t.Errorf(`trino_queries_total{query_kind="explain",status="ok"} went from %g to %g; want four more`, before, after)
	}
}

// TestIssue2586_AnUnansweredPromptIsNotADecline: a prompt the client comes
// back from with no answer ends the call saying the person was not reached,
// and the audit log records the prompting call as input_required (not a
// success) and the retry as prompt_unanswered.
func TestIssue2586_AnUnansweredPromptIsNotADecline(t *testing.T) {
	ec := connect2052(t, "")
	table := issue2052Large(t, ec.client)
	conn := issue2586Costed
	ec.setAnswer("cancel")

	failed, text := ec.queryOn(t, conn, "SELECT n FROM "+table, nil)
	if !failed || !strings.Contains(text, "the person was not reached") ||
		!strings.Contains(text, "sending the same call again will only ask again") {
		t.Fatalf("an unanswered prompt: %s", text)
	}
	if strings.Contains(text, "declined") {
		t.Errorf("an unanswered prompt is reported as declined: %s", text)
	}

	admin := connect(t)
	want := map[string]bool{"input_required": false, "prompt_unanswered": false}
	deadline := time.Now().Add(30 * time.Second)
	for {
		for _, entry := range admin.list(fmt.Sprintf(
			"/api/v1/admin/audit/events?session_id=%s&tool_name=trino_query&per_page=20", ec.sessionID)) {
			event, _ := entry.(map[string]any)
			category, _ := event["error_category"].(string)
			if _, tracked := want[category]; !tracked {
				continue
			}
			if success, _ := event["success"].(bool); success {
				t.Errorf("the %s row is recorded as a success: %v", category, event)
			}
			want[category] = true
		}
		if want["input_required"] && want["prompt_unanswered"] {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit rows found within 30s: %v; want both categories", want)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestIssue2586_ThePIIPromptCountsOnlyTheColumnsRead: memory.default.contacts
// tags email PII and the table nothing. An aggregate over region is not asked
// about; a read of email is, naming one PII column.
func TestIssue2586_ThePIIPromptCountsOnlyTheColumnsRead(t *testing.T) {
	c := connect(t)
	res, text, err := c.callRaw("trino_execute", map[string]any{
		"connection": issue2052Writer, "purpose": issue2586Purpose,
		"sql": "CREATE TABLE IF NOT EXISTS memory.default.contacts AS SELECT 'pat@example.com' AS email, 'west' AS region",
	})
	if err != nil || res.IsError {
		t.Fatalf("creating memory.default.contacts: %v %s", err, text)
	}

	ec := connect2052(t, "")
	ec.setAnswer("accept")
	if failed, text := ec.queryOn(t, issue2052Reader,
		"SELECT region, count(*) AS people FROM memory.default.contacts GROUP BY region", nil); failed {
		t.Fatalf("the aggregate did not run: %s", text)
	}
	if asked := ec.prompts(); len(asked) != 0 {
		t.Fatalf("an aggregate reading no PII column was asked %q", asked)
	}
	if failed, text := ec.queryOn(t, issue2052Reader, "SELECT email FROM memory.default.contacts", nil); failed ||
		!strings.Contains(text, "pat@example.com") {
		t.Fatalf("an accepted read of email: %s", text)
	}
	if asked := ec.prompts(); len(asked) != 1 || !strings.Contains(asked[0], "1 PII column(s)") {
		t.Errorf("a read of the PII column was asked %q; want one prompt naming one PII column", asked)
	}
}
