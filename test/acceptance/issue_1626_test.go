//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1626: `api_discover` with a query, on a connection with an embedding
// index, ranked the WHOLE catalog and returned the first `limit` operations
// with nothing on the result separating a match from filler. A three-operation
// query returned all 34 operations of a fixture; a two-token query on the admin
// catalog returned 50, of which the last twenty contained neither token. The
// score was computed and discarded.
//
// A ranked result now stops where relevance does: the operations containing
// every token of the query, then at most five near neighbors by intent, each
// row carrying `score` and `lexical_match`, and the result carrying
// `matched_lexical` and `shown_semantic`.
//
// Wire forms: `api_discover` is registered with a typed input struct
// (DiscoverInput, pkg/toolkits/apigateway/discover.go), so each parameter
// admits exactly one JSON form -- connection, spec, operation_id, query and
// ranking are strings and limit is a number -- and every call below sends them
// as literal tools/call params in that form. `score`, `lexical_match`,
// `matched_lexical` and `shown_semantic` are result fields, read here off the
// real tool result.

const (
	// issue1626FixtureConn is the api-test fixture `make dev` registers with a
	// catalog, and issue1626AdminConn the built-in self-connection over the
	// platform's own admin API.
	issue1626FixtureConn = "api-test-fixture"
	issue1626AdminConn   = "platform-admin"
	// issue1626EmbedWait bounds how long the suite waits for the connection's
	// catalog to be embedded. Hybrid is the default only where vectors exist,
	// and the dev stack embeds them through Ollama shortly after start, so a
	// run that begins immediately after `make dev` can arrive first.
	issue1626EmbedWait = 3 * time.Minute
)

// TestIssue1626_HybridStopsWhereTheMatchesEnd is criterion 1. The fixture's
// pagination operations contain the token; everything else in its catalog does
// not, and used to come back anyway.
func TestIssue1626_HybridStopsWhereTheMatchesEnd(t *testing.T) {
	c := connect(t)
	total := catalogSize1626(t, c, issue1626FixtureConn)
	out := rankedDiscover1626(t, c, issue1626FixtureConn, "pagination")

	matched, shown := boundary1626(t, out)
	if matched == 0 {
		t.Fatalf("no operation of %s matched \"pagination\": %v",
			issue1626FixtureConn, operationIDs1626(out))
	}
	if shown > 5 {
		t.Errorf("shown_semantic = %d; at most five neighbors follow the matches", shown)
	}
	ops := operations1626(t, out)
	if len(ops) != matched+shown {
		t.Errorf("%d rows for matched=%d shown=%d", len(ops), matched, shown)
	}
	if total > 0 && len(ops) >= total {
		t.Errorf("the whole catalog (%d operations) came back for a query matching %d of them",
			total, matched)
	}

	// The matches lead, each says it matched, and every row carries a score.
	for i, op := range ops {
		score, hasScore := op["score"].(float64)
		if !hasScore {
			t.Errorf("row %d (%s) carries no score", i, idOf1626(op))
		} else if score < 0 || score > 1 {
			t.Errorf("row %d (%s): score %v is outside [0,1]", i, idOf1626(op), score)
		}
		lexical, _ := op["lexical_match"].(bool)
		if i < matched && !lexical {
			t.Errorf("row %d (%s) is inside the matched half but says lexical_match=false", i, idOf1626(op))
		}
		if i >= matched && lexical {
			t.Errorf("row %d (%s) is a neighbor but says lexical_match=true", i, idOf1626(op))
		}
		if i < matched && !strings.Contains(strings.ToLower(pathOf1626(op)+" "+summaryOf1626(op)+" "+idOf1626(op)), "pagination") {
			t.Errorf("row %d (%s %s) leads as a match but contains no \"pagination\"",
				i, idOf1626(op), pathOf1626(op))
		}
	}
}

// TestIssue1626_AQueryThatMatchesNothingSaysSo is criterion 2: the lexical path
// has always answered such a query by naming it, and hybrid answers the same
// way instead of returning the head of the catalog.
func TestIssue1626_AQueryThatMatchesNothingSaysSo(t *testing.T) {
	c := connect(t)
	const nonsense = "zzqx"

	for _, ranking := range []string{"", "lexical"} {
		args := map[string]any{"connection": issue1626FixtureConn, "query": nonsense}
		if ranking != "" {
			args["ranking"] = ranking
		}
		out := c.call("api_discover", args)
		mode := ranking
		if mode == "" {
			mode = "default (hybrid where embeddings exist)"
		}
		if ops := operationsOrNone1626(out); len(ops) != 0 {
			t.Errorf("%s ranking returned %d operations for %q: %v",
				mode, len(ops), nonsense, operationIDs1626(out))
		}
		if note, _ := out["note"].(string); !strings.Contains(note, `no operations match query "`+nonsense+`"`) {
			t.Errorf("%s ranking: note = %q; want the query named", mode, note)
		}
	}
}

// TestIssue1626_TheAdminCatalogAnswersATwoTokenQuery is criterion 3, on the
// catalog the report measured: a query whose two tokens both appear on one
// operation must not bring back fifty.
func TestIssue1626_TheAdminCatalogAnswersATwoTokenQuery(t *testing.T) {
	c := connect(t)
	out := rankedDiscover1626(t, c, issue1626AdminConn, "scripts transfer")

	matched, shown := boundary1626(t, out)
	ops := operations1626(t, out)
	if shown > 5 {
		t.Errorf("shown_semantic = %d; at most five neighbors follow the matches", shown)
	}
	if len(ops) > matched+5 {
		t.Errorf("%d operations returned for a two-token query with %d matches", len(ops), matched)
	}

	// The owner transfer is the operation whose path carries "scripts" and
	// whose summary carries "transfer", so it is one of the matches rather
	// than a neighbor behind them.
	position := -1
	for i, op := range ops {
		if strings.Contains(pathOf1626(op), "/scripts/") && strings.Contains(pathOf1626(op), "/owner") {
			position = i
			break
		}
	}
	if position < 0 {
		t.Fatalf("the script owner transfer is not in the result: %v", operationIDs1626(out))
	}
	if position >= matched {
		t.Errorf("the owner transfer is at row %d, behind the %d matches; it contains both tokens",
			position, matched)
	}
	if lexical, _ := ops[position]["lexical_match"].(bool); !lexical {
		t.Errorf("the owner transfer says lexical_match=false for a query whose tokens it carries")
	}
	// Nothing behind the matches is unbounded: every neighbor is scored.
	for i := matched; i < len(ops); i++ {
		if _, ok := ops[i]["score"].(float64); !ok {
			t.Errorf("neighbor row %d (%s) carries no score", i, idOf1626(ops[i]))
		}
	}
}

// TestIssue1626_LexicalRankingIsUnchanged is criterion 4: the explicit opt-out
// returns the AND filter's own membership and order, and each row now says it
// matched and where it placed.
func TestIssue1626_LexicalRankingIsUnchanged(t *testing.T) {
	c := connect(t)
	out := c.call("api_discover", map[string]any{
		"connection": issue1626FixtureConn, "query": "pagination", "ranking": "lexical",
	})

	ops := operations1626(t, out)
	if len(ops) == 0 {
		t.Fatalf("lexical ranking returned nothing for \"pagination\"")
	}
	matched, shown := boundary1626(t, out)
	if matched != len(ops) || shown != 0 {
		t.Errorf("matched=%d shown=%d for %d rows; the AND filter adds no neighbors",
			matched, shown, len(ops))
	}
	var previous float64 = 2
	for i, op := range ops {
		if lexical, _ := op["lexical_match"].(bool); !lexical {
			t.Errorf("row %d (%s): lexical_match=false in an AND filter's own result", i, idOf1626(op))
		}
		score, ok := op["score"].(float64)
		if !ok {
			t.Fatalf("row %d (%s) carries no score", i, idOf1626(op))
		}
		if score > previous {
			t.Errorf("row %d (%s) scores %v above the row before it (%v); the positional score descends",
				i, idOf1626(op), score, previous)
		}
		previous = score
	}
}

// TestIssue1626_AnUnrankedLevelCarriesNoBoundary: a call with no query matched
// nothing against anything, and says so by carrying none of the four fields.
func TestIssue1626_AnUnrankedLevelCarriesNoBoundary(t *testing.T) {
	c := connect(t)
	out := c.call("api_discover", map[string]any{"connection": issue1626FixtureConn})

	if _, present := out["matched_lexical"]; present {
		t.Errorf("an unranked level reports matched_lexical: %v", out["matched_lexical"])
	}
	if _, present := out["shown_semantic"]; present {
		t.Errorf("an unranked level reports shown_semantic: %v", out["shown_semantic"])
	}
	for i, op := range operations1626(t, out) {
		if _, present := op["score"]; present {
			t.Errorf("row %d (%s) carries a score on an unranked level", i, idOf1626(op))
		}
		if _, present := op["lexical_match"]; present {
			t.Errorf("row %d (%s) carries lexical_match on an unranked level", i, idOf1626(op))
		}
	}
}

// --- helpers ---

// rankedDiscover1626 issues a default-ranking discovery and returns a result
// that was actually ranked semantically.
//
// Proving that matters more than it looks. An omitted ranking resolves to
// hybrid only where the connection's catalog carries vectors; without them it
// stays on the lexical floor, silently, and every criterion about the hybrid
// boundary would then be asserted against a lexical result and prove nothing.
// So this waits on the catalog's own embedding state, read from the admin API,
// and then checks that the call it makes reports no fallback.
func rankedDiscover1626(t *testing.T, c *client, connection, query string) map[string]any {
	t.Helper()
	requireEmbeddedCatalog1626(t, c, connection)
	out := c.call("api_discover", map[string]any{"connection": connection, "query": query})
	note, _ := out["note"].(string)
	if strings.Contains(note, "unavailable") || strings.Contains(note, "fell back") {
		t.Fatalf("%s was not ranked semantically although its catalog is embedded: %q", connection, note)
	}
	return out
}

// requireEmbeddedCatalog1626 waits until every operation of the connection's
// catalog carries a vector. The dev stack embeds through Ollama shortly after
// start, so a run that begins immediately after `make dev` can arrive first;
// after the deadline this fails rather than skips, because a criterion that
// quietly did not run is not one that held. Both connections these criteria
// use carry a catalog whose id is the connection's own name.
func requireEmbeddedCatalog1626(t *testing.T, c *client, catalogID string) {
	t.Helper()
	deadline := time.Now().Add(issue1626EmbedWait)
	for {
		status, body := c.rest(http.MethodGet, "/api/v1/admin/api-catalogs/"+catalogID+"/specs", http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("reading the specs of catalog %s: status %d: %v", catalogID, status, body)
		}
		if embeddedSpecs1626(body) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("catalog %s is still not embedded after %s: %v. "+
				"Hybrid ranking is the default only where vectors exist, so these criteria "+
				"cannot run against this deployment until its embedding provider has",
				catalogID, issue1626EmbedWait, body)
		}
		time.Sleep(5 * time.Second)
	}
}

// embeddedSpecs1626 reports whether every spec of the catalog has a vector for
// every operation it parses to.
func embeddedSpecs1626(body map[string]any) bool {
	specs, _ := body["specs"].([]any)
	if len(specs) == 0 {
		return false
	}
	for _, entry := range specs {
		spec, ok := entry.(map[string]any)
		if !ok {
			return false
		}
		operations, _ := spec["operation_count"].(float64)
		embeddings, _ := spec["embedding_count"].(float64)
		if operations == 0 || embeddings < operations {
			return false
		}
	}
	return true
}

// catalogSize1626 is how many operations the connection exposes to this
// caller, read from the unranked level, so "the whole catalog came back" is
// measured against the deployment rather than a number written here.
func catalogSize1626(t *testing.T, c *client, connection string) int {
	t.Helper()
	out := c.call("api_discover", map[string]any{"connection": connection, "limit": 500})
	return len(operationsOrNone1626(out))
}

// boundary1626 reads the two counts, failing when a ranked result omits them.
func boundary1626(t *testing.T, out map[string]any) (matched, shown int) {
	t.Helper()
	m, ok := out["matched_lexical"].(float64)
	if !ok {
		t.Fatalf("a ranked result carries no matched_lexical: %v", out)
	}
	s, ok := out["shown_semantic"].(float64)
	if !ok {
		t.Fatalf("a ranked result carries no shown_semantic: %v", out)
	}
	return int(m), int(s)
}

func operations1626(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, ok := out["operations"].([]any)
	if !ok {
		t.Fatalf("the operations level carries no operations: %v", out)
	}
	ops := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		op, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("an operation is not an object: %v", entry)
		}
		ops = append(ops, op)
	}
	return ops
}

// operationsOrNone1626 is operations1626 for a result that may legitimately
// have none.
func operationsOrNone1626(out map[string]any) []map[string]any {
	raw, _ := out["operations"].([]any)
	ops := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if op, ok := entry.(map[string]any); ok {
			ops = append(ops, op)
		}
	}
	return ops
}

func operationIDs1626(out map[string]any) []string {
	ops := operationsOrNone1626(out)
	ids := make([]string, 0, len(ops))
	for _, op := range ops {
		ids = append(ids, idOf1626(op)+" "+pathOf1626(op))
	}
	return ids
}

func idOf1626(op map[string]any) string {
	id, _ := op["operation_id"].(string)
	return id
}

func pathOf1626(op map[string]any) string {
	path, _ := op["path"].(string)
	return path
}

func summaryOf1626(op map[string]any) string {
	summary, _ := op["summary"].(string)
	return summary
}
