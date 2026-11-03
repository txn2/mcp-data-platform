package postgres

import (
	"fmt"
	"strings"
)

// splitStatements cuts SQL into its top-level statements at the semicolons
// that end them, skipping a semicolon inside a string literal (with an
// E-string's backslash escapes), a quoted identifier, a dollar-quoted body
// (a function's, a DO block's) or a comment. Empty statements are dropped.
func splitStatements(sql string) []string {
	var out []string
	start := 0
	for i := 0; i < len(sql); {
		next := skipLexeme(sql, i)
		if next > i {
			i = next
			continue
		}
		if sql[i] == ';' {
			out = appendStatement(out, sql[start:i])
			start = i + 1
		}
		i++
	}
	return appendStatement(out, sql[start:])
}

func appendStatement(out []string, s string) []string {
	if strings.TrimSpace(s) == "" || firstWord(s) == "" {
		return out
	}
	return append(out, strings.TrimSpace(s))
}

// skipLexeme returns the index just past the literal, quoted identifier,
// dollar-quoted body or comment starting at i, or i when none starts there.
// One that never ends runs to the end of sql.
func skipLexeme(sql string, i int) int {
	switch sql[i] {
	case '\'':
		return skipString(sql, i, isEString(sql, i))
	case '"':
		return skipString(sql, i, false)
	case '$':
		return skipDollarQuoted(sql, i)
	}
	return skipComment(sql, i)
}

// isEString reports whether the quote at i opens an E'...' string, whose
// backslashes escape.
func isEString(sql string, i int) bool {
	return i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e') && (i < 2 || !isIdentByte(sql[i-2]))
}

// skipComment returns the index just past a comment starting at i, or i.
func skipComment(sql string, i int) int {
	switch {
	case strings.HasPrefix(sql[i:], "--"):
		if j := strings.IndexByte(sql[i:], '\n'); j >= 0 {
			return i + j + 1
		}
		return len(sql)
	case strings.HasPrefix(sql[i:], "/*"):
		return skipBlockComment(sql, i)
	}
	return i
}

// skipString skips a quoted run where a doubled quote is an escaped one and,
// in an E-string, a backslash escapes the next byte.
func skipString(sql string, i int, backslash bool) int {
	quote := sql[i]
	for j := i + 1; j < len(sql); j++ {
		switch {
		case backslash && sql[j] == '\\':
			j++
		case sql[j] != quote:
		case j+1 < len(sql) && sql[j+1] == quote:
			j++
		default:
			return j + 1
		}
	}
	return len(sql)
}

// skipBlockComment skips a /* */ comment, which nests in PostgreSQL.
func skipBlockComment(sql string, i int) int {
	depth := 0
	for j := i; j+1 < len(sql); j++ {
		switch {
		case sql[j] == '/' && sql[j+1] == '*':
			depth++
			j++
		case sql[j] == '*' && sql[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(sql)
}

// skipDollarQuoted skips $tag$ ... $tag$, or returns i when $ starts no
// dollar quote (a positional parameter, $1).
func skipDollarQuoted(sql string, i int) int {
	end := strings.IndexByte(sql[i+1:], '$')
	if end < 0 {
		return i
	}
	tag := sql[i : i+end+2]
	for k := 1; k < len(tag)-1; k++ {
		if !isIdentByte(tag[k]) || (k == 1 && tag[k] >= '0' && tag[k] <= '9') {
			return i
		}
	}
	if closing := strings.Index(sql[i+len(tag):], tag); closing >= 0 {
		return i + len(tag) + closing + len(tag)
	}
	return len(sql)
}

func isIdentByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// checkMigrationSQL refuses migration SQL that would end the transaction a
// migration runs in -- a top-level COMMIT would commit the statements before
// it, run the ones after it outside any transaction, and let a plan, which
// rolls back, keep them -- and a migration that runs outside a transaction
// holding more than its one statement.
func checkMigrationSQL(sql string, transactional bool) error {
	stmts := splitStatements(sql)
	for i, s := range stmts {
		if word := firstWord(s); transactionControl[word] {
			return fmt.Errorf("statement %d is %s, and a migration does not end or split the transaction it runs in; "+
				"the platform commits each migration itself", i+1, strings.ToUpper(word))
		}
	}
	if !transactional && len(stmts) != 1 {
		return fmt.Errorf("a migration that runs outside a transaction holds exactly one statement (it holds %d); "+
			"give each such statement a migration of its own", len(stmts))
	}
	return nil
}
