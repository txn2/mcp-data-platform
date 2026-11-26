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

// useOf reads a description's command list, in either place a schema writes
// it: opening the description ("publish: ..."), or after "for" or "required
// for" anywhere in it. A command is required where a "required for" clause
// names it, or, for a description that opens with its commands, where it says
// required with no list of its own. A clause naming no command ("Required for
// access_mode public") names nothing.
func useOf(desc string, commands []string) paramUse {
	var u paramUse
	prefixed := prefixNamed(desc, commands)
	u.named = append(u.named, prefixed...)
	listedRequired := false
	for _, clause := range clauseEnd.Split(desc, -1) {
		required := false
		for _, m := range forList.FindAllStringSubmatchIndex(clause, -1) {
			required = required || m[2] >= 0
			names := listAt(clause[m[1]:], commands)
			u.named = appendNew(u.named, names...)
			if required && len(names) > 0 {
				u.requiredFor = appendNew(u.requiredFor, names...)
				listedRequired = true
			}
		}
	}
	if len(prefixed) > 0 && !listedRequired && strings.Contains(strings.ToLower(desc), "required") {
		u.requiredFor = appendNew(u.requiredFor, prefixed...)
	}
	return u
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
