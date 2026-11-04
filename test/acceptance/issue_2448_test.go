//go:build integration

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Acceptance for #2448: the PostgreSQL design pages are found by the search
// an agent runs and read with fetch, and the two claims the "does not work"
// page makes are what the platform does when a script tries them: a
// procedure that commits is refused when platform.execute calls it, and
// session state a statement leaves (a temporary table, a session SET) does
// not reach the next statement.
//
// Wire forms: search takes intent and purpose as strings and sources as a
// list; fetch takes reference as a string; manage_schema takes command,
// connection, schema, name and up as strings and version as an integer;
// fetch takes purpose as a string; manage_script run_draft takes command, name and source as strings and
// allow_writes as a boolean. No parameter admits a second form.

// pageSearches2448 are the searches, the page each must find, and the title
// that page carries (a search hit names a page by its row id, which differs
// per deployment; fetch takes the slug).
var pageSearches2448 = []struct{ intent, slug, title string }{
	{"upsert", "platform-postgres-write-patterns", "PostgreSQL write patterns"},
	{"full text search", "platform-postgres-search", "PostgreSQL search"},
	{"row level security", "platform-postgres-security", "PostgreSQL security"},
	{"exclusion constraint", "platform-postgres-integrity", "PostgreSQL integrity"},
	{"job queue", "platform-postgres-queues-locks-events", "PostgreSQL job queues"},
}

// topTitles2448 is the first lines of the first n hits a search answers.
func topTitles2448(out map[string]any, n int) []string {
	titles := []string{}
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		hits, _ := g.(map[string]any)["hits"].([]any)
		for _, h := range hits {
			text, _ := h.(map[string]any)["text"].(string)
			titles = append(titles, strings.SplitN(text, "\n", 2)[0])
			if len(titles) == n {
				return titles
			}
		}
	}
	return titles
}

func TestIssue2448_EachTopicFindsItsPageAndFetchReadsIt(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	for _, q := range pageSearches2448 {
		t.Run(q.slug, func(t *testing.T) {
			var top []string
			waitFor(t, "the page among the top results", func() (bool, string) {
				out := c.call("search", map[string]any{
					"intent": q.intent, "sources": []any{"knowledge_pages"}, "purpose": "Acceptance #2448: an agent looks for PostgreSQL guidance.",
				})
				top = topTitles2448(out, 3)
				for _, title := range top {
					if strings.HasPrefix(title, q.title) {
						return true, ""
					}
				}
				return false, strings.Join(top, " | ")
			})
			doc := c.call("fetch", map[string]any{"reference": "mcp:knowledge_page:" + q.slug, "purpose": "Acceptance #2448: read the guidance the search found."})
			raw := asJSON(doc)
			if !strings.Contains(raw, q.title) || !strings.Contains(raw, "```sql") {
				t.Errorf("fetch %s did not return the page: %.300s", q.slug, raw)
			}
		})
	}
}

func TestIssue2448_AProcedureThatCommitsIsRefusedThroughPlatformExecute(t *testing.T) {
	admin := connectFor(t, 3*time.Minute)
	conn := appdbConn(t, admin, "acc-2448-proc", "orders_app", "orders_secret")
	schema := "acc2448_proc_" + unique1579()
	migrate(t, admin, conn, schema, `CREATE TABLE batches (id int PRIMARY KEY);
CREATE PROCEDURE load_in_chunks() LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO `+schema+`.batches VALUES (1);
  COMMIT;
  INSERT INTO `+schema+`.batches VALUES (2);
END $$`)
	out := draft2428(admin, "acc-2448-proc", `def main():
    """Calls a procedure that commits."""
    got = platform.execute("CALL `+schema+`.load_in_chunks()", connection = "`+conn+`", on_error = "return")
    print(json.encode(got))
`, true)
	if !strings.Contains(asJSON(out), "2D000") {
		t.Fatalf("the procedure's COMMIT was not refused with SQLSTATE 2D000: %v", out)
	}
	if n := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM "+schema+".batches")); n != "0" {
		t.Errorf("the refused call left %s rows", n)
	}
}

func TestIssue2448_SessionStateDoesNotReachTheNextStatement(t *testing.T) {
	admin := connect(t)
	conn := appdbConn(t, admin, "acc-2448-session", "orders_app", "orders_secret")
	out := draft2428(admin, "acc-2448-session", `def main():
    """Leaves session state behind and reads it from the next statement."""
    platform.execute("CREATE TEMP TABLE scratch_2448 (id int)", connection = "`+conn+`")
    gone = platform.query("SELECT count(*) AS n FROM scratch_2448", connection = "`+conn+`", on_error = "return")
    print("temp: " + json.encode(gone))
    platform.execute("SET application_name = 'acc-2448-set'", connection = "`+conn+`")
    shown = platform.query("SELECT current_setting('application_name') AS name", connection = "`+conn+`")
    print("set: " + shown["rows"][0]["name"])
`, true)
	log := asJSON(out)
	if !strings.Contains(log, "42P01") {
		t.Errorf("the temporary table reached the next statement: %s", log)
	}
	if strings.Contains(log, "set: acc-2448-set") || !strings.Contains(log, "set: mcp-data-platform") {
		t.Errorf("the session SET reached the next statement: %s", log)
	}
}
