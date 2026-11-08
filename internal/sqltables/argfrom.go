package sqltables

import "strings"

// wordSelect opens a statement of its own inside parentheses.
const wordSelect = "select"

// fromScan walks a statement's words by parenthesis, marking each FROM that
// does not open a table clause.
type fromScan struct {
	sql string
	// groups says, for each open parenthesis and the statement around them,
	// whether a SELECT or DELETE has appeared in it at its own level.
	groups []bool
	prev   string
	marked map[int]bool
}

// argumentFroms is the byte offset of every FROM that names no table: an
// argument keyword of a function (extract(epoch FROM ts), substring(x FROM 1),
// trim(BOTH FROM x), overlay(a PLACING b FROM 1)) or the end of IS DISTINCT
// FROM (#2547). Inside parentheses a FROM opens a table clause only when the
// parentheses hold a statement of their own, which a SELECT or DELETE at that
// level says, so no list of functions has to be kept. String literals, quoted
// identifiers and comments are skipped.
func argumentFroms(sql string) map[int]bool {
	s := &fromScan{sql: sql, groups: []bool{true}, marked: map[int]bool{}}
	for i := 0; i < len(sql); {
		i = s.step(i)
	}
	return s.marked
}

// step reads what starts at i and returns where the next thing starts.
func (s *fromScan) step(i int) int {
	if skip := skipQuotedOrComment(s.sql, i); skip > i {
		return skip
	}
	if isWordByte(s.sql[i]) {
		return s.word(i)
	}
	s.bracket(s.sql[i])
	return i + 1
}

func (s *fromScan) bracket(c byte) {
	switch {
	case c == '(':
		s.groups = append(s.groups, false)
	case c == ')' && len(s.groups) > 1:
		s.groups = s.groups[:len(s.groups)-1]
	}
}

// word records the word starting at i and returns the index just past it.
func (s *fromScan) word(i int) int {
	end := i
	for end < len(s.sql) && isWordByte(s.sql[end]) {
		end++
	}
	word := strings.ToLower(s.sql[i:end])
	top := len(s.groups) - 1
	switch {
	case word == wordSelect || word == "delete":
		s.groups[top] = true
	case word == "from" && (!s.groups[top] || s.prev == "distinct"):
		s.marked[i] = true
	}
	s.prev = word
	return end
}
