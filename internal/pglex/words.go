package pglex

import "strings"

// FirstWord is the lowercased first word of sql after whitespace, comments
// and opening parentheses: the keyword a statement is read by.
func FirstWord(sql string) string {
	s := strings.TrimLeft(sql, " \t\r\n(")
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = strings.TrimLeft(s[i+1:], " \t\r\n(")
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = strings.TrimLeft(s[i+2:], " \t\r\n(")
				continue
			}
			return ""
		}
		end := strings.IndexFunc(s, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z')
		})
		if end < 0 {
			end = len(s)
		}
		return strings.ToLower(s[:end])
	}
}

// RunsConcurrently reports whether sql holds a CREATE, DROP or REINDEX
// statement with the word CONCURRENTLY outside its literals and comments,
// which PostgreSQL runs only outside a transaction block (#2490).
func RunsConcurrently(sql string) bool {
	for _, s := range Split(sql) {
		switch FirstWord(s) {
		case "create", "drop", "reindex":
			if hasWord(s, "concurrently") {
				return true
			}
		}
	}
	return false
}

// hasWord reports whether the unquoted word appears in stmt as a whole word,
// ignoring case, outside literals, quoted identifiers and comments.
func hasWord(stmt, word string) bool {
	for i := 0; i < len(stmt); {
		if j := SkipLexeme(stmt, i); j > i {
			i = j
			continue
		}
		if !IsIdentByte(stmt[i]) {
			i++
			continue
		}
		j := i
		for j < len(stmt) && IsIdentByte(stmt[j]) {
			j++
		}
		if strings.EqualFold(stmt[i:j], word) {
			return true
		}
		i = j
	}
	return false
}
