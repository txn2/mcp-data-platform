// Package flowdoc reads what a script's author wrote about a function: the
// first sentence of the comment above it, at the top of its body, or of its
// docstring, which a flow diagram's boxes are captioned with.
package flowdoc

import (
	"strings"

	"go.starlark.net/syntax"
)

// Caption is the first sentence of the comment the author wrote above a def,
// or at the top of its body, or else of its docstring. A script created since
// #1938 documents every function, so its every box carries a sentence a reader
// who does not read code can follow.
func Caption(d *syntax.DefStmt, maxRunes int) string {
	if cm := d.Comments(); cm != nil {
		if s := firstSentence(cm.Before, maxRunes); s != "" {
			return s
		}
	}
	if len(d.Body) == 0 {
		return ""
	}
	if cm := d.Body[0].Comments(); cm != nil {
		if s := firstSentence(cm.Before, maxRunes); s != "" {
			return s
		}
	}
	return DocSentence(d.Body[0], maxRunes)
}

// DocSentence is the first sentence of a docstring statement, or "" when the
// statement is not one.
func DocSentence(s syntax.Stmt, maxRunes int) string {
	es, ok := s.(*syntax.ExprStmt)
	if !ok {
		return ""
	}
	lit, ok := es.X.(*syntax.Literal)
	if !ok {
		return ""
	}
	text, ok := lit.Value.(string)
	if !ok {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	comments := make([]syntax.Comment, 0, len(lines))
	for _, l := range lines {
		comments = append(comments, syntax.Comment{Text: "# " + l})
	}
	return firstSentence(comments, maxRunes)
}

// firstSentence joins the comment lines of one paragraph up to its first full
// stop. A rule line (# ----) or a blank comment ends the paragraph.
func firstSentence(comments []syntax.Comment, maxRunes int) string {
	var parts []string
	for _, c := range comments {
		t := strings.TrimSpace(strings.TrimPrefix(c.Text, "#"))
		if t == "" || strings.HasPrefix(t, "---") || strings.HasPrefix(t, "===") {
			if len(parts) > 0 {
				break
			}
			continue
		}
		parts = append(parts, t)
		if strings.HasSuffix(t, ".") {
			break
		}
	}
	s := strings.Join(parts, " ")
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	return clip(s, maxRunes)
}

// clip shortens s to n runes.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
