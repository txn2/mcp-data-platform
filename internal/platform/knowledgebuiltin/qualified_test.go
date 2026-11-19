package knowledgebuiltin

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applicationSQLPages are the built-in pages that teach an application's SQL,
// each with the schemas its example application is held in. Each of an
// application's environments runs in a schema of its own, with the search
// path set to it, so the SQL an application writes names its tables without a
// schema, and a save that adds a qualified name is refused (#2709). A page
// that taught shop.orders taught the refusal.
var applicationSQLPages = map[string][]string{
	"applications.md":             {"shop"},
	"public-applications.md":      {"store"},
	"application-environments.md": {"shop", "orders", "orders_dev"},
	"application-databases.md":    {"shop", "app"},
}

// qualifiedSQL matches a table, view or index named with one of schemas
// where SQL names one: after FROM, JOIN, INTO, UPDATE, TABLE, ON, VIEW,
// REFERENCES or TRUNCATE. A hostname or a file (store.example, app.js) is not
// SQL and is left alone.
func qualifiedSQL(schemas []string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b(from|join|into|update|table|on|view|references|truncate)\s+"?(` +
		strings.Join(schemas, "|") + `)"?\.[a-z_"]`)
}

// codeLines is each line of body inside a fenced block or an indented block,
// with its line number.
func codeLines(body string) map[int]string {
	out := map[int]string{}
	fenced := false
	for i, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			out[i+1] = line
		}
	}
	return out
}

// TestPages_ApplicationSQLNamesNoSchema refuses a schema-qualified table in
// an example of a page that teaches application SQL (#2709).
func TestPages_ApplicationSQLNamesNoSchema(t *testing.T) {
	for file, schemas := range applicationSQLPages {
		raw, err := pagesFS.ReadFile("pages/" + file)
		require.NoError(t, err, file)
		re := qualifiedSQL(schemas)
		for n, line := range codeLines(string(raw)) {
			assert.Falsef(t, re.MatchString(line), "%s line %d names a table with its schema; an application writes it unqualified: %s",
				file, n, strings.TrimSpace(line))
		}
	}
}

func TestQualifiedSQL(t *testing.T) {
	re := qualifiedSQL([]string{"shop"})
	for _, bad := range []string{`SELECT * FROM shop.orders`, `INSERT INTO "shop".lines`, `update shop.inventory`, `GRANT SELECT ON shop.t TO x`} {
		assert.True(t, re.MatchString(bad), bad)
	}
	for _, ok := range []string{`SELECT * FROM orders`, `https://shop.example`, `src="/shop.js"`, `FROM shopping.orders`, `x = shop.get(1)`} {
		assert.False(t, re.MatchString(ok), ok)
	}
	assert.Equal(t, map[int]string{3: "SELECT 1", 6: "    indented"}, codeLines("a\n```sql\nSELECT 1\n```\nprose\n    indented\n"))
}
