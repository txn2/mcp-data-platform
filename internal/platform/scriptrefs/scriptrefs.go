// Package scriptrefs finds the saved scripts that name an application
// database's table or column (#2405): the lookup a migration's rename is
// checked against before it saves. It reads each candidate's source for the
// connection's name and the object's name in its strings, so a script that
// runs SQL naming the column, or reads a row by its key, is found.
package scriptrefs

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// scriptsNamingQuery is the saved scripts whose source mentions a name, the
// candidates the source is then read for.
const scriptsNamingQuery = `SELECT name, source_code FROM scripts WHERE source_code ILIKE $1 ESCAPE '\' ORDER BY name`

// Lookup is the lookup a migration's rename is checked against: for each
// name, the saved scripts whose source names the
// connection in a string and the name as a word in a string -- the SQL the
// script runs, or the key it reads a row's column by. Endpoints are scripts,
// so a bound endpoint is found as its script.
func Lookup(db *sql.DB) postgreskit.ScriptReferences {
	return func(ctx context.Context, connection string, names []string) (map[string][]string, error) {
		out := make(map[string][]string, len(names))
		for _, name := range names {
			scripts, err := scriptsNaming(ctx, db, connection, name)
			if err != nil {
				return nil, err
			}
			if len(scripts) > 0 {
				out[name] = scripts
			}
		}
		return out, nil
	}
}

// scriptsNaming reads the candidates for one name.
func scriptsNaming(ctx context.Context, db *sql.DB, connection, name string) ([]string, error) {
	rows, err := db.QueryContext(ctx, scriptsNamingQuery, "%"+likeEscape(name)+"%")
	if err != nil {
		return nil, fmt.Errorf("reading the scripts that name %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var script, source string
		if err := rows.Scan(&script, &source); err != nil {
			return nil, fmt.Errorf("reading a script: %w", err)
		}
		if namesObject(source, connection, name) {
			out = append(out, script)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the scripts that name %s: %w", name, err)
	}
	return out, nil
}

// namesObject reports whether a script's source has a string that is the
// connection and a string holding name as a word. A source that does not
// parse names nothing.
func namesObject(source, connection, name string) bool {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	if err != nil {
		return false
	}
	var strs []string
	syntax.Walk(file, func(n syntax.Node) bool {
		if lit, ok := n.(*syntax.Literal); ok && lit.Token == syntax.STRING {
			if s, ok := lit.Value.(string); ok {
				strs = append(strs, s)
			}
		}
		return true
	})
	return slices.Contains(strs, connection) && slices.ContainsFunc(strs, func(s string) bool { return hasWord(s, name) })
}

// hasWord reports whether s holds w, ignoring case, between characters that
// cannot be part of an identifier.
func hasWord(s, w string) bool {
	s, w = strings.ToLower(s), strings.ToLower(w)
	for i := 0; ; {
		j := strings.Index(s[i:], w)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(w)
		if (start == 0 || !identByte(s[start-1])) && (end == len(s) || !identByte(s[end])) {
			return true
		}
		i = start + 1
	}
}

func identByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

// likeEscape escapes the characters LIKE reads as patterns.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}
