package pglex

import "testing"

func TestFirstWord(t *testing.T) {
	for sql, want := range map[string]string{
		"SELECT 1":                      "select",
		"  (select 1)":                  "select",
		"-- note\nINSERT INTO a":        "insert",
		"/* x */ Drop index i":          "drop",
		"-- only a comment":             "",
		"/* never closed":               "",
		"":                              "",
		"WITH x AS (SELECT 1) SELECT 1": "with",
	} {
		if got := FirstWord(sql); got != want {
			t.Errorf("FirstWord(%q) = %q, want %q", sql, got, want)
		}
	}
}

// TestRunsConcurrently is #2490's add check: a CREATE, DROP or REINDEX that
// runs CONCURRENTLY, and nothing that only mentions the word.
func TestRunsConcurrently(t *testing.T) {
	for sql, want := range map[string]bool{
		"DROP INDEX CONCURRENTLY IF EXISTS app.i": true,
		"create index concurrently i on a (id)":   true,
		"REINDEX INDEX CONCURRENTLY i":            true,
		"DROP TABLE a; DROP INDEX CONCURRENTLY i": true,
		"DROP INDEX i": false,
		"COMMENT ON TABLE a IS 'built concurrently'": false,
		"SELECT 'concurrently'":                      false,
		"DROP INDEX \"concurrently\"":                false,
		"DROP INDEX i -- concurrently":               false,
		"CREATE TABLE concurrently_built (id int)":   false,
	} {
		if got := RunsConcurrently(sql); got != want {
			t.Errorf("RunsConcurrently(%q) = %v, want %v", sql, got, want)
		}
	}
}
