package toolhelp

import (
	"regexp"
	"slices"
	"strings"
)

// paramUse is what a parameter's description says about the commands of a
// multi-command tool (#2826): the commands it names, and the ones it says need
// it.
type paramUse struct {
	named       []string
	requiredFor []string
}

// commandPrefix matches a description that opens by naming the commands it is
// for: "publish: ...", "grant, revoke: ...", "For register: ...".
var commandPrefix = regexp.MustCompile(`^(?i:for\s+)?([a-z_]+(?:(?:,\s*|\s+and\s+|\s+or\s+|,\s*and\s+|,\s*or\s+)[a-z_]+)*)\s*:`)

// commandSplit splits that list into its names.
var commandSplit = regexp.MustCompile(`,\s*(?:and\s+|or\s+)?|\s+and\s+|\s+or\s+`)

// clauseEnd splits a description into the clauses a "required for" list
// cannot run past: a sentence end, a semicolon, a parenthesis.
var clauseEnd = regexp.MustCompile(`[;()]|\.(?:\s|$)`)

// forList finds "for" and "required for" inside a clause, the forms the
// schemas name commands with wherever in the description they do it:
// "(required for get, update, delete)", "Required for 'update' and 'forget'",
// "required for revert action", "for list it is the folder ...".
var forList = regexp.MustCompile(`(?i)\b(required\s+)?for\s+`)

// listToken is one token of a command list: a name, possibly quoted, or a comma.
var listToken = regexp.MustCompile(`^\s*(?:,|['"]?([A-Za-z_]+)['"]?)`)

// listFiller are the words a command list may hold between names.
var listFiller = map[string]bool{"and": true, "or": true, "for": true, "the": true, "action": true, "actions": true, "command": true, "commands": true}

// parenList matches a parenthesis that opens by naming the commands a
// parameter is for (#2832), the form most action schemas write: "(update
// action)", "(update and patch actions)", "(update action, only when ...)",
// "(locate and get_content)", and the list after a noun, "The content actions
// (get_content, outline, locate)". The list closes the parenthesis or is
// followed by a comma; a word after it with no comma ("(list of names)") is
// prose, not a list.
var parenList = regexp.MustCompile(`^\s*(` + listName + `(?:(?:` + listSep + `)` + listName + `)*)(?:\s+(?i:actions?|commands?))?\s*(?:,|$)`)

// listGroup is parenList's group holding the list.
const listGroup = 1

// listName and listSep are one name of a parenthesised list, possibly quoted,
// and what separates two.
const (
	listName = `['"]?[A-Za-z_]+['"]?`
	listSep  = `\s*,\s*(?:and\s+|or\s+)?|\s+and\s+|\s+or\s+`
)

// clause is one clause of a description, and whether it opens a parenthesis.
type clause struct {
	text  string
	paren bool
}

// clausesOf splits a description at clauseEnd, keeping which clauses follow
// an opening parenthesis.
func clausesOf(desc string) []clause {
	var out []clause
	start, paren := 0, false
	for _, m := range clauseEnd.FindAllStringIndex(desc, -1) {
		out = append(out, clause{text: desc[start:m[0]], paren: paren})
		paren = desc[m[0]] == '('
		start = m[1]
	}
	return append(out, clause{text: desc[start:], paren: paren})
}

// useOf reads a description's command list, in each place a schema writes
// it: opening the description ("publish: ..."), after "for" or "required
// for" anywhere in it, or opening a parenthesis ("(update and patch
// actions)"). A command is required where a "required for" clause names it,
// or, for a description that opens with its commands, where it says required
// with no list of its own. A clause naming no command ("Required for
// access_mode public") names nothing.
func useOf(desc string, commands []string) paramUse {
	var u paramUse
	prefixed := prefixNamed(desc, commands)
	u.named = append(u.named, prefixed...)
	listedRequired := false
	for _, c := range clausesOf(desc) {
		if c.paren {
			u.named = appendNew(u.named, parenNamed(c.text, commands)...)
		}
		listedRequired = u.readFor(c.text, commands) || listedRequired
	}
	if len(prefixed) > 0 && !listedRequired && strings.Contains(strings.ToLower(desc), "required") {
		u.requiredFor = appendNew(u.requiredFor, prefixed...)
	}
	return u
}

// readFor adds the commands each "for" or "required for" list in a clause
// names, reporting whether a "required for" list named any.
func (u *paramUse) readFor(clause string, commands []string) bool {
	required, listedRequired := false, false
	for _, m := range forList.FindAllStringSubmatchIndex(clause, -1) {
		required = required || m[2] >= 0
		names := listAt(clause[m[1]:], commands)
		u.named = appendNew(u.named, names...)
		if required && len(names) > 0 {
			u.requiredFor = appendNew(u.requiredFor, names...)
			listedRequired = true
		}
	}
	return listedRequired
}

// parenNamed is the commands a parenthesised clause opens by listing. A list
// holding a plain word that is no command ("(true)", "(update and more)") is
// prose and names nothing, as is one whose comma is followed by another
// command ("(publish, promote and rollback leave it alone)": a sentence whose
// subject is a list, not a list of the commands it is for); a snake_case word
// that is no command (a field, or a command this tool does not have) is
// passed over.
func parenNamed(text string, commands []string) []string {
	m := parenList.FindStringSubmatchIndex(text)
	if len(m) <= 2*listGroup+1 || continuesList(text[m[1]:], commands) {
		return nil
	}
	var out []string
	for _, n := range commandSplit.Split(strings.TrimSpace(text[m[2*listGroup]:m[2*listGroup+1]]), -1) {
		n = strings.Trim(strings.TrimSpace(n), `'"`)
		switch {
		case slices.Contains(commands, n):
			out = appendNew(out, n)
		case !strings.Contains(n, "_"):
			return nil
		}
	}
	return out
}

// prefixNamed is the commands a description opens by naming, when every name
// in its leading list is one of them.
func prefixNamed(desc string, commands []string) []string {
	m := commandPrefix.FindStringSubmatch(desc)
	if m == nil {
		return nil
	}
	var out []string
	for _, n := range commandSplit.Split(m[1], -1) {
		n = strings.TrimSpace(n)
		if !slices.Contains(commands, n) {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// listAt reads the commands a list starting at s names, up to the first word
// that is neither a command, a filler word, nor shaped like a command name
// (snake_case: a command this tool does not have, such as a renamed one).
func listAt(s string, commands []string) []string {
	var out []string
	for {
		m := listToken.FindStringSubmatch(s)
		if m == nil {
			return out
		}
		if w := m[1]; w != "" {
			switch {
			case slices.Contains(commands, w):
				out = appendNew(out, w)
			case !listFiller[strings.ToLower(w)] && !strings.Contains(w, "_"):
				return out
			}
		}
		s = s[len(m[0]):]
	}
}

// continuesList reports whether the text after a list's closing comma opens
// with another command, so the comma was inside the list.
func continuesList(rest string, commands []string) bool {
	m := listToken.FindStringSubmatch(strings.TrimPrefix(strings.TrimSpace(rest), "and "))
	return len(m) > 1 && slices.Contains(commands, m[1])
}

// appendNew appends each name not already in list.
func appendNew(list []string, names ...string) []string {
	for _, n := range names {
		if !slices.Contains(list, n) {
			list = append(list, n)
		}
	}
	return list
}

// commandsNamed is the commands a description names, wherever it names them.
func commandsNamed(desc string, commands []string) []string {
	return useOf(desc, commands).named
}

// requiredFor reports whether a parameter is required for cmd: the schema
// requires it and its description names no command other than cmd's, or its
// description says cmd needs it.
func requiredFor(u paramUse, schemaRequired bool, cmd string) bool {
	if slices.Contains(u.requiredFor, cmd) {
		return true
	}
	return schemaRequired && (len(u.named) == 0 || slices.Contains(u.named, cmd))
}
