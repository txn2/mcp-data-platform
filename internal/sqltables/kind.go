package sqltables

import (
	"strings"
)

// KindOther is the statement kind of anything StatementKind does not
// recognize, including an empty statement.
const KindOther = "other"

// statementKinds are the leading keywords StatementKind reports, lowercased.
// Bounded on purpose: the kind is a metric label (trino_queries_total's
// query_kind, db_client_operation_duration_seconds' operation), and a
// statement's first word is the caller's to choose.
//
//nolint:gochecknoglobals // a read-only lookup set.
var statementKinds = map[string]bool{
	"select": true, "with": true, "values": true, "table": true,
	"insert": true, "update": true, "delete": true, "merge": true, "upsert": true,
	"show": true, "describe": true, "desc": true, "explain": true, "analyze": true,
	"create": true, "drop": true, "alter": true, "truncate": true, "comment": true,
	"call": true, "grant": true, "revoke": true, "set": true, "reset": true,
	"begin": true, "start": true, "commit": true, "rollback": true, "savepoint": true, "release": true,
	"copy": true, "lock": true, "listen": true, "notify": true, "unlisten": true,
	"vacuum": true, "refresh": true, "prepare": true, "execute": true, "deallocate": true,
	"use": true,
}

// StatementKind is the lowercased leading keyword of a SQL statement, after
// any leading comments and opening parentheses, when it is one of the
// keywords this package knows; KindOther otherwise. It reads no further than
// that word, so it never carries a literal, an identifier or the text.
func StatementKind(sql string) string {
	word := leadingWord(sql)
	if statementKinds[word] {
		return word
	}
	return KindOther
}

// Summary is a low-cardinality description of a statement without its
// literals, in the shape the OpenTelemetry database conventions give
// db.query.summary: the statement's keyword in upper case followed by the
// tables it reads, at most three of them ("SELECT hive.sales.orders"). A
// statement this package does not recognize summarizes as its kind alone.
func Summary(sql string) string {
	kind := StatementKind(sql)
	parts := []string{strings.ToUpper(kind)}
	if kind == KindOther {
		return parts[0]
	}
	const maxTables = 3
	for _, ref := range Extract(sql) {
		if len(parts) > maxTables {
			break
		}
		parts = append(parts, ref.FullPath)
	}
	return strings.Join(parts, " ")
}

// leadingWord is the first word of sql, lowercased, past leading whitespace,
// "--" line comments, "/* */" block comments and opening parentheses.
func leadingWord(sql string) string {
	s := sql
	for {
		s = strings.TrimLeft(s, " \t\r\n(")
		rest, skipped, ok := skipComment(s)
		if !ok {
			return ""
		}
		if !skipped {
			break
		}
		s = rest
	}
	end := strings.IndexFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '_'
	})
	if end < 0 {
		end = len(s)
	}
	return strings.ToLower(s[:end])
}

// skipComment removes one comment from the front of s. skipped reports whether
// there was one; ok is false for a comment that never ends, which leaves no
// statement after it.
func skipComment(s string) (rest string, skipped, ok bool) {
	switch {
	case strings.HasPrefix(s, "--"):
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			return "", true, false
		}
		return s[nl+1:], true, true
	case strings.HasPrefix(s, "/*"):
		_, after, found := strings.Cut(s, "*/")
		if !found {
			return "", true, false
		}
		return after, true, true
	default:
		return s, false, true
	}
}
