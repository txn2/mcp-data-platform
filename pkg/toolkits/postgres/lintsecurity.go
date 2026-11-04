package postgres

import (
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/pglex"
)

// The migration review's security and version rules (#2449). The lint above
// finds statements that lock, rewrite or remove data; these find statements
// that leave an application database open to a role that should not reach
// it, statements that need privileges an application's role must not hold,
// and statements the server's major does not have. The version rules read
// the major from Target, which the review reads from the connection at add.

// The rules.
const (
	ruleDefinerSearchPath  = "security_definer_search_path"
	ruleDefinerPublic      = "security_definer_public_execute"
	ruleRLSNotForced       = "row_security_not_forced"
	ruleViewOverRLS        = "view_bypasses_row_security"
	ruleIndexVirtualColumn = "index_on_virtual_column"
	ruleNeedsNewerServer   = "feature_needs_newer_server"
	ruleTargetUnread       = "target_unread"
	ruleUntrustedCode      = "untrusted_code"
	ruleCopyProgram        = "copy_program"
	ruleEventTrigger       = "event_trigger"
	ruleAlterSystem        = "alter_system"
)

// SeverityRefused is a statement that needs a privilege an application's
// role must not hold or reaches outside the database; the migration does not
// save.
const SeverityRefused = "refused"

// Target is what the review knows of the server and schema a migration is
// for. A zero Target leaves the version rules silent.
type Target struct {
	// Major is the server's major, 0 when it could not be read.
	Major int
	// RowSecurity is the schema's tables with row-level security enabled.
	RowSecurity map[string]bool
	// Virtual is the schema's virtual generated columns, by table.
	Virtual map[string]map[string]bool
}

// untrustedLanguages are the languages a function runs outside the
// database's sandbox in: an untrusted procedural language, or native code.
var untrustedLanguages = map[string]bool{
	"plpython3u": true, "plpythonu": true, "plpython2u": true, "plperlu": true, "pltclu": true,
	"c": true, "internal": true, "plsh": true, "pljavau": true, "plv8u": true,
}

// securityState carries what the security rules have read of the migration
// so far, and is judged when its last statement has been read.
type securityState struct {
	target Target
	// definers are the SECURITY DEFINER functions created, by name, with the
	// statement that created each.
	definers map[string]int
	revoked  map[string]bool
	// revokedAll is a REVOKE ... ON ALL FUNCTIONS IN SCHEMA ... FROM PUBLIC.
	revokedAll bool
	// defaultRevokedAt is the statement of an ALTER DEFAULT PRIVILEGES that
	// revokes EXECUTE from PUBLIC; it covers the functions created after it.
	defaultRevokedAt int
	enabled          map[string]int
	forced           map[string]bool
	// views are the views created without security_invoker, with the tables
	// each reads and its statement.
	views   map[string]viewRead
	invoker map[string]bool
	virtual map[string]map[string]bool
}

type viewRead struct {
	n      int
	tables []string
}

func newSecurityState(target Target) *securityState {
	return &securityState{
		target: target, definers: map[string]int{}, revoked: map[string]bool{},
		enabled: map[string]int{}, forced: map[string]bool{}, views: map[string]viewRead{},
		invoker: map[string]bool{}, virtual: map[string]map[string]bool{},
	}
}

// Words the security rules read statements by.
const (
	kwFunction  = "function"
	kwProcedure = "procedure"
	kwAs        = "as"
	kwComma     = ","
	kwAlter     = "alter"
	kwView      = "view"
	kwOpen      = "("
	// generatedAsOpen is how far kwOpen stands from GENERATED in GENERATED
	// ALWAYS AS (.
	generatedAsOpen = 3
	// pg18 is the major the 18 rules are about.
	pg18 = 18
)

// securityRules reads a statement for the security and version rules by
// its first word.
var securityRules = map[string]func(l *linter, n int, ts toks){
	"create": (*linter).createObject,
	"revoke": (*linter).revoke,
	kwAlter:  (*linter).alterSecurity,
	"copy":   (*linter).copyProgram,
	"do":     (*linter).doBlock,
}

// securityStatement reads one statement for the security and version rules.
func (l *linter) securityStatement(n int, ts toks) {
	if len(ts) == 0 {
		return
	}
	l.versionFeatures(n, ts)
	if rule, ok := securityRules[ts[0].word]; ok {
		rule(l, n, ts)
	}
}

// copyProgram refuses COPY ... FROM PROGRAM and TO PROGRAM.
func (l *linter) copyProgram(n int, ts toks) {
	if c := ts.findWord("program"); c > 0 && (ts.at(c-1, "from") || ts.at(c-1, "to")) {
		l.refuse(n, ruleCopyProgram, "COPY ... PROGRAM", "runs a shell command on the database server as its operating-system user")
	}
}

// doBlock refuses a DO block in an untrusted language.
func (l *linter) doBlock(n int, ts toks) {
	if lang := ts.languageOf(); untrustedLanguages[lang] {
		l.refuse(n, ruleUntrustedCode, "DO LANGUAGE "+lang, "runs code in "+lang+", which runs outside the database's sandbox")
	}
}

// refuse adds a refused finding.
func (l *linter) refuse(n int, rule, object, effect string) {
	l.add(Finding{
		Rule: rule, Severity: SeverityRefused, Statement: n, Object: object, Effect: effect,
		Safe: "leave it to the database's administrator: an application's migration may not need a superuser",
	})
}

// maxCreateKindWord is how far into a CREATE its object word may stand
// (CREATE OR REPLACE TEMP RECURSIVE VIEW).
const maxCreateKindWord = 5

// createRules reads a CREATE by its object word, at index k.
var createRules = map[string]func(l *linter, n int, ts toks, k int){
	kwFunction:  (*linter).createFunction,
	kwProcedure: (*linter).createFunction,
	kwView:      (*linter).createView,
	"language": func(l *linter, n int, _ toks, _ int) {
		l.refuse(n, ruleUntrustedCode, "CREATE LANGUAGE", "installs a procedural language, which needs a superuser")
	},
	"event": func(l *linter, n int, _ toks, _ int) {
		l.refuse(n, ruleEventTrigger, "CREATE EVENT TRIGGER", "runs a function on every DDL statement in the database, which needs a superuser")
	},
	"extension": (*linter).createExtension,
	kwTable:     (*linter).createTableColumns,
	"index":     func(l *linter, n int, ts toks, _ int) { l.indexOnVirtual(n, ts) },
}

// createObject reads a CREATE for the security rules.
func (l *linter) createObject(n int, ts toks) {
	for k := 1; k < len(ts) && k <= maxCreateKindWord; k++ {
		if rule, ok := createRules[ts[k].word]; ok {
			rule(l, n, ts, k)
			return
		}
	}
}

// createExtension refuses an extension that installs an untrusted language.
func (l *linter) createExtension(n int, ts toks, k int) {
	if name := ts.nameAfter(k + 1); untrustedLanguages[name] {
		l.refuse(n, ruleUntrustedCode, "CREATE EXTENSION "+name, "installs "+name+", a language whose code runs outside the database's sandbox")
	}
}

// createFunction reads a function's language, SECURITY DEFINER and the
// search_path it pins.
func (l *linter) createFunction(n int, ts toks, k int) {
	name := ts.nameAfter(k + 1)
	if lang := ts.languageOf(); untrustedLanguages[lang] {
		l.refuse(n, ruleUntrustedCode, name, "creates "+name+" in "+lang+", which runs outside the database's sandbox")
		return
	}
	if ts.findPair("security", "definer") < 0 {
		return
	}
	l.sec.definers[name] = n
	if !ts.searchPathEndsInTemp() {
		l.add(Finding{
			Rule: ruleDefinerSearchPath, Severity: SeverityWarning, Statement: n, Object: name,
			Effect: name + " runs with its owner's privileges and resolves names through the caller's search_path, " +
				"so a caller who can create an object in a schema on that path runs their own code as the owner",
			Safe: "SET search_path = <the schemas it uses>, pg_temp on the function, with pg_temp last",
		})
	}
}

// revoke notes the functions a REVOKE ... FROM PUBLIC takes EXECUTE from.
func (l *linter) revoke(_ int, ts toks) {
	on, from, ok := ts.revokeFromPublic()
	if !ok {
		return
	}
	if ts.at(on+1, "all") {
		l.sec.revokedAll = true
		return
	}
	if ts.at(on+1, kwFunction) || ts.at(on+1, kwProcedure) || ts.at(on+1, "routine") {
		for _, name := range ts.routineNames(on+2, from) {
			l.sec.revoked[name] = true
		}
	}
}

// revokeFromPublic finds a REVOKE's ON and FROM, when it revokes from PUBLIC.
func (ts toks) revokeFromPublic() (on, from int, ok bool) {
	from = ts.findWord("from")
	if from < 0 || !slices.ContainsFunc(ts[from:], func(t tok) bool { return t.word == "public" }) {
		return 0, 0, false
	}
	on = ts.findWord("on")
	return on, from, on >= 0 && on+1 < len(ts)
}

// routineNames is the comma-separated routines from i to end, each with an
// optional argument list.
func (ts toks) routineNames(i, end int) []string {
	var out []string
	for i < end {
		if name := ts.nameAfter(i); name != "" {
			out = append(out, name)
		}
		i = ts.skipArgs(ts.afterName(i))
		if i >= end || ts[i].text != kwComma {
			break
		}
		i++
	}
	return out
}

// alterRules reads an ALTER for the security rules by its second word.
var alterRules = map[string]func(l *linter, n int, ts toks){
	"system": func(l *linter, n int, _ toks) {
		l.refuse(n, ruleAlterSystem, "ALTER SYSTEM", "changes the server's configuration for every database on it, which needs a superuser")
	},
	"default": (*linter).alterDefaultPrivileges,
	kwView: func(l *linter, _ int, ts toks) {
		if name := ts.nameAfter(2); ts.optionOn("security_invoker") {
			l.sec.invoker[name] = true
		}
	},
	kwTable: func(l *linter, n int, ts toks) {
		table := ts.nameAfter(2)
		for _, a := range ts.actions(ts.afterName(2)) {
			l.rowSecurityAction(n, table, a)
			l.addVirtualColumn(n, table, a)
		}
	},
}

// alterSecurity reads ALTER TABLE's row-level security, ALTER VIEW's
// security_invoker, ALTER DEFAULT PRIVILEGES and ALTER SYSTEM.
func (l *linter) alterSecurity(n int, ts toks) {
	if len(ts) < 2 {
		return
	}
	if rule, ok := alterRules[ts[1].word]; ok {
		rule(l, n, ts)
	}
}

// alterDefaultPrivileges notes an ALTER DEFAULT PRIVILEGES revoking EXECUTE
// on functions from PUBLIC.
func (l *linter) alterDefaultPrivileges(n int, ts toks) {
	if ts.at(2, "privileges") && ts.findWord("revoke") >= 0 && ts.findWord("public") >= 0 &&
		(ts.findWord("functions") >= 0 || ts.findWord("routines") >= 0) {
		l.sec.defaultRevokedAt = n
	}
}

// rowSecurityAction notes ENABLE and FORCE ROW LEVEL SECURITY.
func (l *linter) rowSecurityAction(n int, table string, a toks) {
	if a.findPair("level", "security") < 0 {
		return
	}
	switch {
	case a.at(0, "enable"):
		l.sec.enabled[table] = n
		l.sec.target.RowSecurity = withName(l.sec.target.RowSecurity, table)
	case a.at(0, "force"):
		l.sec.forced[table] = true
	}
}

func withName(m map[string]bool, name string) map[string]bool {
	if m == nil {
		m = map[string]bool{}
	}
	m[name] = true
	return m
}

// createView notes a view created without security_invoker and the tables
// it reads; a materialized view has no security_invoker and is left alone.
func (l *linter) createView(n int, ts toks, k int) {
	if slices.ContainsFunc(ts[1:k], func(t tok) bool { return t.word == "materialized" }) {
		return
	}
	name := ts.nameAfter(k + 1)
	as := ts.findWord(kwAs)
	if name == "" || as < 0 {
		return
	}
	if ts[:as].optionOn("security_invoker") {
		l.sec.invoker[name] = true
		return
	}
	l.sec.views[name] = viewRead{n: n, tables: ts[as+1:].tablesRead()}
}

// finishSecurity judges what needed the whole migration.
func (l *linter) finishSecurity() {
	l.definersLeftToPublic()
	l.rowSecurityNotForced()
	l.viewsOverRowSecurity()
}

// definersLeftToPublic warns on a definer no REVOKE ... FROM PUBLIC covers.
func (l *linter) definersLeftToPublic() {
	for _, name := range sortedKeys(l.sec.definers) {
		n := l.sec.definers[name]
		if l.sec.revoked[name] || l.sec.revokedAll || (l.sec.defaultRevokedAt > 0 && l.sec.defaultRevokedAt < n) {
			continue
		}
		l.add(Finding{
			Rule: ruleDefinerPublic, Severity: SeverityWarning, Statement: n, Object: name,
			Effect: name + " runs with its owner's privileges, and every role may execute it until EXECUTE is revoked from PUBLIC",
			Safe:   "REVOKE EXECUTE ON FUNCTION " + name + " FROM PUBLIC in the same migration, then GRANT it to the roles that call it",
		})
	}
}

// rowSecurityNotForced warns on row security enabled and not forced.
func (l *linter) rowSecurityNotForced() {
	for _, table := range sortedKeys(l.sec.enabled) {
		if l.sec.forced[table] {
			continue
		}
		l.add(Finding{
			Rule: ruleRLSNotForced, Severity: SeverityWarning, Statement: l.sec.enabled[table], Object: table,
			Effect: "enables row-level security on " + table + " without forcing it, so the table's owner, the role migrations and " +
				"connections without a persona role run as, reads and writes every row past its policies",
			Safe: "ALTER TABLE " + table + " FORCE ROW LEVEL SECURITY in the same migration",
		})
	}
}

// viewsOverRowSecurity warns on a view reading a table with row security as
// its owner.
func (l *linter) viewsOverRowSecurity() {
	for _, view := range sortedKeys(l.sec.views) {
		v := l.sec.views[view]
		if l.sec.invoker[view] {
			continue
		}
		i := slices.IndexFunc(v.tables, func(table string) bool { return l.sec.target.RowSecurity[table] })
		if i < 0 {
			continue
		}
		table := v.tables[i]
		l.add(Finding{
			Rule: ruleViewOverRLS, Severity: SeverityWarning, Statement: v.n, Object: view,
			Effect: view + " reads " + table + " as the view's owner, so a role that may select from the view sees every row " +
				table + "'s policies would hide from it",
			Safe: "CREATE VIEW " + view + " WITH (security_invoker = true) AS ...",
		})
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// languageOf is the language a function or DO block is written in: the word
// or literal after its top-level LANGUAGE, lower-cased.
func (ts toks) languageOf() string {
	i := ts.findWord("language")
	if i < 0 || i+1 >= len(ts) {
		return ""
	}
	return strings.ToLower(ts[i+1].text)
}

// searchPathEndsInTemp reports whether a function pins search_path with SET
// and puts pg_temp last. FROM CURRENT pins whatever the migration's path is,
// which the review cannot read, and is not taken as pinned.
func (ts toks) searchPathEndsInTemp() bool {
	i := ts.findPair("set", "search_path")
	if i < 0 || i+2 >= len(ts) {
		return false
	}
	i += 2
	if ts[i].text == "=" || ts.at(i, "to") {
		i++
	}
	values := ts.settingValues(i)
	return len(values) > 0 && strings.EqualFold(values[len(values)-1], "pg_temp")
}

// settingValues is a SET's comma-separated values from i: words, quoted
// names, and literals holding a list of their own.
func (ts toks) settingValues(i int) []string {
	var values []string
	for i < len(ts) && (ts[i].word != "" || ts[i].text != "" && !isPunct(ts[i].text)) {
		for v := range strings.SplitSeq(ts[i].text, kwComma) {
			values = append(values, strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"`)))
		}
		if i+1 >= len(ts) || ts[i+1].text != kwComma {
			break
		}
		i += 2
	}
	return values
}

// isPunct reports whether text is one byte of punctuation.
func isPunct(text string) bool {
	return len(text) == 1 && !pglex.IsIdentByte(text[0])
}

// skipArgs is past a parenthesized argument list at i, or i.
func (ts toks) skipArgs(i int) int {
	if i >= len(ts) || ts[i].text != kwOpen {
		return i
	}
	depth := 0
	for ; i < len(ts); i++ {
		switch ts[i].text {
		case kwOpen:
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return i
}

// optionTrue are the values that turn a boolean option on.
var optionTrue = map[string]bool{"true": true, "on": true, "1": true, "yes": true}

// optionOn reports whether a WITH (...) or SET (...) list turns option on:
// named with no value, or with true, on, 1 or yes.
func (ts toks) optionOn(option string) bool {
	for i, t := range ts {
		if t.word != option {
			continue
		}
		if i+2 < len(ts) && ts[i+1].text == "=" {
			return optionTrue[strings.ToLower(ts[i+2].text)]
		}
		return true
	}
	return false
}

// fromEnd are the words that end a FROM list's alias.
var fromEnd = map[string]bool{
	"where": true, "join": true, "on": true, "group": true, "order": true, "limit": true, "union": true,
	"left": true, "right": true, "inner": true, "full": true, "cross": true, "natural": true, "lateral": true,
	"using": true, "having": true, "window": true, "offset": true, "fetch": true, "for": true, "except": true,
	"intersect": true, "outer": true, "returning": true, "with": true,
}

// tablesRead is every table a query names after FROM or JOIN, at any depth.
func (ts toks) tablesRead() []string {
	var out []string
	for i, t := range ts {
		if t.word == "from" || t.word == "join" {
			out = append(out, ts.fromList(i+1)...)
		}
	}
	return out
}

// fromList is the comma-separated tables from j, each with an optional
// alias.
func (ts toks) fromList(j int) []string {
	var out []string
	for j < len(ts) {
		name := ts.nameAfter(j)
		if name == "" || fromEnd[name] {
			break
		}
		out = append(out, name)
		j = skipWord(ts, ts.afterName(j), kwAs)
		if j < len(ts) && ts[j].word != "" && !fromEnd[ts[j].word] {
			j++
		}
		if j >= len(ts) || ts[j].text != kwComma {
			break
		}
		j++
	}
	return out
}

// createTableColumns notes the virtual generated columns a CREATE TABLE
// declares.
func (l *linter) createTableColumns(n int, ts toks, k int) {
	table := ts.nameAfter(k + 1)
	open := ts.afterName(k + 1)
	if table == "" || open >= len(ts) || ts[open].text != kwOpen {
		return
	}
	end := ts.skipArgs(open)
	for _, col := range ts[open+1 : max(open+1, end-1)].actions(0) {
		l.addVirtualColumn(n, table, append(toks{{word: schemaAdd, text: schemaAdd}}, col...))
	}
}

// addVirtualColumn notes a column an ADD [COLUMN] declares GENERATED ALWAYS
// AS (...) without STORED.
func (l *linter) addVirtualColumn(_ int, table string, a toks) {
	if !a.at(0, schemaAdd) || isConstraintAction(a) {
		return
	}
	g := a.findPair("generated", "always")
	if g < 0 || !a.at(g+2, kwAs) || g+generatedAsOpen >= len(a) || a[g+generatedAsOpen].text != kwOpen {
		return
	}
	if after := a.skipArgs(g + generatedAsOpen); a.at(after, "stored") {
		return
	}
	col := a.nameAfter(skipIfNotExists(a, skipWord(a, 1, kwColumn)))
	if l.sec.virtual[table] == nil {
		l.sec.virtual[table] = map[string]bool{}
	}
	l.sec.virtual[table][col] = true
}

// indexOnVirtual warns on an index naming a virtual generated column, which
// PostgreSQL 18 cannot index.
func (l *linter) indexOnVirtual(n int, ts toks) {
	if l.sec.target.Major < pg18 {
		return
	}
	on := ts.findWord("on")
	if on < 0 {
		return
	}
	table := ts.nameAfter(on + 1)
	open := ts.afterName(on + 1)
	if ts.at(open, "using") {
		open += 2
	}
	if open >= len(ts) || ts[open].text != kwOpen {
		return
	}
	for _, t := range ts[open:ts.skipArgs(open)] {
		if l.sec.virtual[table][t.word] || l.sec.target.Virtual[table][t.word] {
			l.add(Finding{
				Rule: ruleIndexVirtualColumn, Severity: SeverityWarning, Statement: n, Object: table + "." + t.word,
				Effect: fmt.Sprintf("indexes %s.%s, a generated column declared without STORED, which PostgreSQL %d makes virtual "+
					"and cannot index", table, t.word, l.sec.target.Major),
				Safe: "declare the column GENERATED ALWAYS AS (...) STORED",
			})
			return
		}
	}
}

// feature18 is a feature PostgreSQL 18 introduced, how a statement uses it,
// and what to write for a 17 server instead.
type feature18 struct {
	name  string
	found func(ts toks) bool
	safe  string
}

var features18 = []feature18{
	{
		"uuidv7()", func(ts toks) bool { return ts.callsAny("uuidv7", "uuidv4") },
		"gen_random_uuid(), or a uuidv7 function written in SQL",
	},
	{
		"RETURNING old/new", func(ts toks) bool { return ts.returningOldNew() },
		"read the old values in a CTE before the change, or in a trigger",
	},
	{
		"virtual generated columns", func(ts toks) bool { return ts.declaresVirtual() },
		"GENERATED ALWAYS AS (...) STORED",
	},
	{
		"WITHOUT OVERLAPS", func(ts toks) bool { return ts.findPair("without", "overlaps") >= 0 },
		"an exclusion constraint: EXCLUDE USING gist (key WITH =, period WITH &&), with btree_gist",
	},
	{
		"NOT ENFORCED", func(ts toks) bool { return ts.findPair("not", "enforced") >= 0 },
		"leave the constraint out, or add it NOT VALID",
	},
}

// versionFeatures warns on a feature newer than the server's major.
func (l *linter) versionFeatures(n int, ts toks) {
	major := l.sec.target.Major
	if major == 0 || major >= pg18 {
		return
	}
	for _, f := range features18 {
		if f.found(ts) {
			l.add(Finding{
				Rule: ruleNeedsNewerServer, Severity: SeverityWarning, Statement: n, Object: f.name,
				Effect: fmt.Sprintf("uses %s, which PostgreSQL 18 introduced; the connection's server is PostgreSQL %d, which refuses it",
					f.name, major),
				Safe: f.safe,
			})
		}
	}
}

// callsAny reports whether the statement calls one of fns.
func (ts toks) callsAny(fns ...string) bool {
	for i := 0; i+1 < len(ts); i++ {
		if slices.Contains(fns, ts[i].word) && ts[i+1].text == kwOpen {
			return true
		}
	}
	return false
}

// returningOldNew reports a RETURNING that reads OLD or NEW (old.col, new.*,
// RETURNING WITH (OLD AS o)).
func (ts toks) returningOldNew() bool {
	r := slices.IndexFunc(ts, func(t tok) bool { return t.word == "returning" })
	if r < 0 {
		return false
	}
	if ts.at(r+1, "with") {
		return true
	}
	for i := r + 1; i+1 < len(ts); i++ {
		if (ts[i].word == "old" || ts[i].word == "new") && ts[i+1].text == "." {
			return true
		}
	}
	return false
}

// declaresVirtual reports a GENERATED ALWAYS AS (...) column without STORED.
func (ts toks) declaresVirtual() bool {
	for i := 0; i+generatedAsOpen < len(ts); i++ {
		if ts[i].word == "generated" && ts.at(i+1, "always") && ts.at(i+2, kwAs) && ts[i+generatedAsOpen].text == kwOpen {
			if !ts.at(ts.skipArgs(i+generatedAsOpen), "stored") {
				return true
			}
		}
	}
	return false
}
