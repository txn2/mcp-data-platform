package pglint

import (
	"strings"

	"github.com/txn2/mcp-data-platform/internal/pglex"
)

// RuleRoleNamed refuses a migration that creates, alters, drops or grants to
// a role (#2539, #2709). A role belongs to the whole server, and an
// application's migrations apply to every environment's schema, each with a
// run role of its own that the platform grants on that schema alone: a
// migration naming a role would grant one environment's role, another
// application's, or every role's (PUBLIC) on data it must not reach.
const RuleRoleNamed = "role_named"

// Words the role rule reads statements by.
const (
	kwCreate = "create"
	kwDrop   = "drop"
	kwRevoke = "revoke"
	kwGroup  = "group"
	kwWith   = "with"
	// roleClausesFrom is where a CREATE ROLE's clauses start: past CREATE,
	// ROLE and the name.
	roleClausesFrom = 3
)

// roleWords are the words a statement names a role kind by.
var roleWords = map[string]bool{"role": true, "user": true, kwGroup: true}

// ownRoleWords are the role names a grant may name that stand for the
// session's own role or every role's, not another application's.
var ownRoleWords = map[string]bool{"current_user": true, "session_user": true, "current_role": true}

// grantEnd ends a list of grantees.
var grantEnd = map[string]bool{kwWith: true, "granted": true, "cascade": true, "restrict": true}

// rolesNamed refuses each role a statement names. Silent when the target
// names no schema.
func (l *linter) rolesNamed(n int, ts toks) {
	schema := l.sec.target.Schema
	if schema == "" || len(ts) < 2 {
		return
	}
	for _, role := range namedRoles(ts) {
		l.add(Finding{
			Rule: RuleRoleNamed, Severity: SeverityRefused, Statement: n, Object: role,
			Effect: "names role " + role + ": the migration applies to every environment's schema, each with a run role the " +
				"platform grants on it alone, and a role belongs to the whole server, so a grant here would reach data in " +
				"another environment or another application",
			Safe: "leave the role out: the platform grants each environment's run role SELECT, INSERT, UPDATE and DELETE on " +
				"what the migration creates in that environment's schema",
		})
	}
}

// namedRoles is the roles a statement creates, alters, drops or grants to:
// CREATE, ALTER and DROP ROLE, USER and GROUP; a GRANT's grantees and the
// roles a membership GRANT hands out; a REVOKE's grantees other than PUBLIC;
// and the grantees of ALTER DEFAULT PRIVILEGES.
func namedRoles(ts toks) []string {
	switch ts[0].word {
	case kwCreate, kwAlter, kwDrop:
		if roleWords[ts[1].word] {
			out := roleList(ts, 2, ts[0].word == kwDrop)
			if ts[0].word == kwCreate {
				out = append(out, memberClauses(ts, roleClausesFrom)...)
			}
			return out
		}
		if ts[0].word == kwAlter && ts.at(1, "default") && ts.at(2, "privileges") {
			return grantees(ts, true)
		}
	case "grant":
		out := grantees(ts, false)
		if ts.findWord("on") < 0 {
			// GRANT role, ... TO ...: membership in the roles named.
			out = append(out, roleList(ts, 1, true)...)
		}
		return out
	case kwRevoke:
		return grantees(ts, true)
	}
	return nil
}

// memberClauses reads the roles a CREATE ROLE's IN ROLE, ROLE, ADMIN and
// USER clauses name, from i: each makes one role a member of another.
func memberClauses(ts toks, i int) []string {
	var out []string
	for ; i < len(ts); i++ {
		if roleWords[ts[i].word] || ts[i].word == "admin" {
			out = append(out, roleList(ts, i+1, true)...)
		}
	}
	return out
}

// roleList reads the role name at i, or a comma-separated list of them when
// many is set, past IF EXISTS.
func roleList(ts toks, i int, many bool) []string {
	i = skipIfExists(ts, i)
	var out []string
	for i < len(ts) && ts[i].word != "" {
		if ts[i].word == "to" || ts[i].word == "from" || ts[i].word == kwWith {
			break
		}
		out = append(out, ts[i].word)
		if !many || i+1 >= len(ts) || ts[i+1].text != kwComma {
			break
		}
		i += 2
	}
	return out
}

// grantees reads the roles after a statement's TO, or its FROM when from is
// set and TO is absent, leaving out PUBLIC on a REVOKE and the session's own
// role.
func grantees(ts toks, from bool) []string {
	at := ts.findWord("to")
	revoking := false
	if at < 0 && from {
		at, revoking = ts.findWord("from"), true
	}
	if at < 0 {
		return nil
	}
	var out []string
	for i := at + 1; i < len(ts); i++ {
		w := ts[i].word
		switch {
		case ts[i].text == kwComma, w == kwGroup:
			continue
		case w == "" || grantEnd[w]:
			return out
		case ownRoleWords[w], revoking && w == "public":
			continue
		}
		out = append(out, w)
	}
	return out
}

// rolesInDo reads the statements written in a DO block's body for the roles
// they name, so a role created idempotently (IF NOT EXISTS ... THEN CREATE
// ROLE) is held to the rule as a plain CREATE ROLE is. SQL the body passes as
// a string (EXECUTE '...', dblink_exec) is read once unquoted, and a body
// that builds SQL at run time naming a role statement (EXECUTE format('GRANT
// ... TO %I', ...)), which cannot be read, is refused (#2750).
func (l *linter) rolesInDo(n int, stmt string) {
	if l.sec.target.Schema == "" {
		return
	}
	body := dollarBody(stmt)
	if pglex.BuildsSQL(body) && namesRoleStatement(pglex.Words(body)) {
		l.add(Finding{
			Rule: RuleRoleNamed, Severity: SeverityRefused, Statement: n, Object: pglex.BuiltAtRunTime,
			Effect: "builds a statement at run time that names a role, which cannot be read before it runs; a role belongs " +
				"to the whole server, so a grant it makes would reach data in another environment or another application",
			Safe: "leave the role out: the platform grants each environment's run role SELECT, INSERT, UPDATE and DELETE on " +
				"what the migration creates in that environment's schema",
		})
		return
	}
	l.rolesInBody(n, body, 0)
}

// rolesInBody reads each statement of a body, and the SQL each string literal
// in it stands for, for the roles they name, to maxLiteralDepth.
func (l *linter) rolesInBody(n int, body string, depth int) {
	for _, inner := range splitStatements(body) {
		ts := lex(inner)
		for i := range ts {
			if namesRoles(ts[i:]) {
				l.rolesNamed(n, ts[i:])
				break
			}
		}
		if depth < maxLiteralDepth {
			for _, text := range pglex.StringLiterals(inner) {
				l.rolesInBody(n, text, depth+1)
			}
		}
	}
}

// maxLiteralDepth bounds how deep a string literal inside a literal is read.
const maxLiteralDepth = 4

// namesRoleStatement reports whether words, a unit's words with its literals'
// read in place, name a statement namedRoles reads: CREATE, ALTER or DROP of
// a role, user or group, GRANT, REVOKE, or ALTER DEFAULT PRIVILEGES.
func namesRoleStatement(words []string) bool {
	for i, w := range words {
		switch w {
		case "grant", kwRevoke:
			return true
		case kwCreate, kwAlter, kwDrop:
			if i+1 < len(words) && (roleWords[words[i+1]] || (w == kwAlter && words[i+1] == "default")) {
				return true
			}
		}
	}
	return false
}

// namesRoles reports whether ts opens a statement namedRoles reads.
func namesRoles(ts toks) bool {
	if len(ts) < 2 {
		return false
	}
	switch ts[0].word {
	case kwCreate, kwAlter, kwDrop:
		return roleWords[ts[1].word] || (ts[0].word == kwAlter && ts.at(1, "default"))
	case "grant", kwRevoke:
		return true
	}
	return false
}

// dollarBody is the text of a statement's first dollar-quoted body, empty
// when it has none.
func dollarBody(stmt string) string {
	for i := strings.IndexByte(stmt, '$'); i >= 0 && i < len(stmt); {
		end := pglex.SkipDollarQuoted(stmt, i)
		if end > i {
			tag := stmt[i : i+strings.IndexByte(stmt[i+1:], '$')+2]
			if end-len(tag) >= i+len(tag) {
				return stmt[i+len(tag) : end-len(tag)]
			}
			return ""
		}
		next := strings.IndexByte(stmt[i+1:], '$')
		if next < 0 {
			return ""
		}
		i += next + 1
	}
	return ""
}
