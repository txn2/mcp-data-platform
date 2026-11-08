package pglint

import (
	"strings"

	"github.com/txn2/mcp-data-platform/internal/pglex"
)

// ruleRoleOutsideSchema refuses a migration that creates, alters, drops or
// grants to a role whose name does not start with its schema's (#2539). A
// role belongs to the whole server, and an application's statements run as
// its run role, a member of the roles its own migrations define: a
// migration naming another application's role could grant itself that
// application's data.
const ruleRoleOutsideSchema = "role_outside_schema"

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

// rolesOutsideSchema refuses each role a statement names that is not the
// schema's own. Silent when the target names no schema.
func (l *linter) rolesOutsideSchema(n int, ts toks) {
	schema := l.sec.target.Schema
	if schema == "" || len(ts) < 2 {
		return
	}
	for _, role := range namedRoles(ts) {
		if !strings.HasPrefix(role, schema+"_") {
			l.add(Finding{
				Rule: ruleRoleOutsideSchema, Severity: SeverityRefused, Statement: n, Object: role,
				Effect: "names a role outside schema " + schema + ": a role belongs to the whole server, and one another " +
					"application uses, or every role (PUBLIC), would reach this schema's data or give this application another's",
				Safe: "name the schema's own roles, " + schema + "_read, " + schema + "_write and narrower ones, each starting " +
					"with " + schema + "_; the platform grants the application's run role " + schema + "_write",
			})
		}
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
// ROLE) is held to the rule as a plain CREATE ROLE is. A statement the body
// assembles at run time (EXECUTE format(...)) cannot be read; applying a
// migration is for administrators and the personas a connection names.
func (l *linter) rolesInDo(n int, stmt string) {
	if l.sec.target.Schema == "" {
		return
	}
	for _, inner := range splitStatements(dollarBody(stmt)) {
		ts := lex(inner)
		for i := range ts {
			if namesRoles(ts[i:]) {
				l.rolesOutsideSchema(n, ts[i:])
				break
			}
		}
	}
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
