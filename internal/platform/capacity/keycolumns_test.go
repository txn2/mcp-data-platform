package capacity

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notObjectKeys are the text columns named *_key that hold no object key,
// each with what it holds instead. A column named *_key that is neither here
// nor read by a reference query fails TestEveryObjectKeyColumnIsAReference.
//
//nolint:gochecknoglobals // a read-only table.
var notObjectKeys = map[string]string{
	"idempotency_key": "a caller's retry token",
	"item_key":        "an integration item's identity within its operation",
	"state_key":       "an alert condition's identity",
	"user_key":        "a person's identifier in a watermark",
	"site_key":        "a bot-check provider's public site key",
}

// migrationsDir is where the platform's migrations are kept.
const migrationsDir = "../../../pkg/database/migrate/migrations"

var (
	createTableRe = regexp.MustCompile(`(?i)CREATE TABLE\s+(?:IF NOT EXISTS\s+)?(\w+)\s*\(`)
	dropTableRe   = regexp.MustCompile(`(?i)DROP TABLE\s+(?:IF EXISTS\s+)?(\w+)`)
	addColumnRe   = regexp.MustCompile(`(?i)ALTER TABLE\s+(?:IF EXISTS\s+)?(\w+)\s+ADD COLUMN\s+(?:IF NOT EXISTS\s+)?(\w+_key)\s+(?:TEXT|VARCHAR)`)
	dropColumnRe  = regexp.MustCompile(`(?i)ALTER TABLE\s+(?:IF EXISTS\s+)?(\w+)\s+DROP COLUMN\s+(?:IF EXISTS\s+)?(\w+)`)
	keyColumnRe   = regexp.MustCompile(`(?im)^\s*(\w+_key)\s+(?:TEXT|VARCHAR)`)
)

// objectKeyColumns is every live table.column the migrations declare as a text
// column named *_key, in migration order, minus the notObjectKeys.
func objectKeyColumns(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.up.sql"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	sort.Strings(files)
	live := map[string]bool{}
	for _, f := range files {
		body, err := os.ReadFile(f) // #nosec G304 -- a migration of this repository
		require.NoError(t, err)
		recordKeyColumns(string(body), live)
	}
	var out []string
	for col, ok := range live {
		if _, exempt := notObjectKeys[col[strings.Index(col, ".")+1:]]; ok && !exempt {
			out = append(out, col)
		}
	}
	sort.Strings(out)
	return out
}

// recordKeyColumns applies one migration's creates, adds and drops to live.
func recordKeyColumns(sql string, live map[string]bool) {
	for _, loc := range createTableRe.FindAllStringSubmatchIndex(sql, -1) {
		table := sql[loc[2]:loc[3]]
		for _, m := range keyColumnRe.FindAllStringSubmatch(tableBody(sql[loc[1]:]), -1) {
			live[table+"."+strings.ToLower(m[1])] = true
		}
	}
	for _, m := range addColumnRe.FindAllStringSubmatch(sql, -1) {
		live[m[1]+"."+strings.ToLower(m[2])] = true
	}
	for _, m := range dropColumnRe.FindAllStringSubmatch(sql, -1) {
		delete(live, m[1]+"."+strings.ToLower(m[2]))
	}
	for _, m := range dropTableRe.FindAllStringSubmatch(sql, -1) {
		for col := range live {
			if strings.HasPrefix(col, m[1]+".") {
				delete(live, col)
			}
		}
	}
}

// tableBody is a CREATE TABLE's column list: the text up to the parenthesis
// that closes the one its statement opened.
func tableBody(rest string) string {
	depth := 1
	for i, r := range rest {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return rest[:i]
			}
		}
	}
	return rest
}

// TestEveryObjectKeyColumnIsAReference: every column a migration declares to
// hold an object key is read by a reference query, so the capacity pass never
// takes an object a row names for an orphan and deletes it. Site tiles were
// left out and every one was deleted a day after it was drawn (#2701). A new
// *_key column either joins portalRefsQuery or resourceRefsQuery, or is listed
// in notObjectKeys with what it holds.
func TestEveryObjectKeyColumnIsAReference(t *testing.T) {
	cols := objectKeyColumns(t)
	require.Contains(t, cols, "site_tiles.tile_key")
	require.Contains(t, cols, "portal_assets.s3_key")
	refs := portalRefsQuery + "\n" + resourceRefsQuery
	for _, col := range cols {
		table, column, _ := strings.Cut(col, ".")
		read := regexp.MustCompile(`SELECT\s+` + column + `\s+FROM\s+` + table + `\b`)
		assert.Regexp(t, read, refs,
			"%s holds an object key and no capacity reference query reads it: the capacity pass would delete its objects as orphans", col)
	}
}

// TestRecordKeyColumns: the migration reader follows creates, added columns,
// dropped columns and dropped tables.
func TestRecordKeyColumns(t *testing.T) {
	live := map[string]bool{}
	recordKeyColumns(`CREATE TABLE IF NOT EXISTS a (
    id TEXT PRIMARY KEY,
    s3_key TEXT NOT NULL DEFAULT '',
    meta JSONB NOT NULL DEFAULT '{}'::jsonb,
    other_key VARCHAR(64)
);
CREATE TABLE b (k TEXT);
ALTER TABLE b ADD COLUMN IF NOT EXISTS blob_key TEXT;`, live)
	assert.Equal(t, map[string]bool{"a.s3_key": true, "a.other_key": true, "b.blob_key": true}, live)
	recordKeyColumns(`ALTER TABLE a DROP COLUMN IF EXISTS other_key; DROP TABLE IF EXISTS b;`, live)
	assert.Equal(t, map[string]bool{"a.s3_key": true}, live)
	assert.Equal(t, "x (y)", tableBody("x (y)) z"))
	assert.Equal(t, "open", tableBody("open"))
}
