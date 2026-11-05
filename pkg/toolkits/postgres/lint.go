package postgres

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/pglex"
)

// The migration lint (#2405): what a migration's up SQL does to a live table,
// read statement by statement before the migration is saved. A locking or
// rewriting statement is a warning that names its safe form; a statement that
// removes data needs a summary of what it removes, agreed by the person the
// application is built for, before the migration saves.
//
// The lint is lexical, over the statements checkMigrationSQL splits. It reads
// the statement kinds a migration is written with and leaves any other alone:
// a lint that cannot read a statement says nothing about it rather than guess.

// The severities a finding has.
const (
	// SeverityWarning is a statement that locks or rewrites a live table; it
	// is reported and recorded, and the migration saves.
	SeverityWarning = "warning"
	// SeverityDestructive is a statement that removes data; the migration
	// saves only with an agreed summary of what it removes.
	SeverityDestructive = "destructive"
)

// The lint's rules.
const (
	RuleIndexLocks          = "create_index_locks"
	RuleNotNullNoDefault    = "not_null_without_default"
	RuleVolatileDefault     = "volatile_default_rewrites"
	RuleTypeChangeRewrites  = "type_change_rewrites"
	RuleConstraintValidates = "constraint_validates"
	RuleDropTable           = "drop_table"
	RuleDropColumn          = "drop_column"
	RuleTruncate            = "truncate"
	RuleUnboundedDelete     = "unbounded_delete"
	RuleUnboundedUpdate     = "unbounded_update"
	RuleRenameReferenced    = "rename_referenced"
	RuleLockTimeoutRaised   = "lock_timeout_raised"
	RuleFromZeroUnchecked   = "from_zero_unchecked"
)

// Finding is one thing the lint found in a migration.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	// Statement is the statement's place in the up SQL, from 1.
	Statement int `json:"statement,omitempty"`
	// Object is the table, column or setting the statement acts on.
	Object string `json:"object,omitempty"`
	// Effect is what the statement does to a live table.
	Effect string `json:"effect"`
	// Safe is the form that does not.
	Safe string `json:"safe_form,omitempty"`
	// Scripts names the saved scripts that name a renamed object.
	Scripts []string `json:"scripts,omitempty"`
}

// Rename is a table or column a migration renames.
type Rename struct {
	Statement int
	// Table is the table renamed, or the table whose column is renamed.
	Table string
	// Column is the column renamed; empty for a table.
	Column string
}

// Name is the identifier a script would name: the column, or the table.
func (r Rename) Name() string {
	if r.Column != "" {
		return r.Column
	}
	return r.Table
}

// Object is the renamed object as a finding names it.
func (r Rename) Object() string {
	if r.Column != "" {
		return r.Table + "." + r.Column
	}
	return r.Table
}

// Lint reads a migration's up SQL for the server and schema target
// describes. It returns what it found and the objects the migration renames,
// which the caller looks up in the saved scripts.
func Lint(up string, target Target) ([]Finding, []Rename) {
	l := &linter{created: map[string]bool{}, sec: newSecurityState(target)}
	for i, s := range splitStatements(up) {
		ts := lex(s)
		l.statement(i+1, ts)
		l.securityStatement(i+1, s, ts)
	}
	l.finishSecurity()
	if l.findings == nil {
		l.findings = []Finding{}
	}
	return l.findings, l.renames
}

// Destructive is the findings that need an agreed summary.
func Destructive(fs []Finding) []Finding { return withSeverity(fs, SeverityDestructive) }

// Refused is the findings no migration may save with.
func Refused(fs []Finding) []Finding { return withSeverity(fs, SeverityRefused) }

func withSeverity(fs []Finding, severity string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Severity == severity {
			out = append(out, f)
		}
	}
	return out
}

// linter carries what earlier statements of the migration created.
type linter struct {
	created  map[string]bool
	findings []Finding
	renames  []Rename
	sec      *securityState
}

func (l *linter) add(f Finding) { l.findings = append(l.findings, f) }

// live is the tables of names this migration did not create: a table it
// created holds nothing a statement after it could remove.
func (l *linter) live(names []string) []string {
	var out []string
	for _, n := range names {
		if !l.created[n] {
			out = append(out, n)
		}
	}
	return out
}

// Words the lint reads statements by.
const (
	kwTable  = "table"
	kwColumn = "column"
	kwConstr = "constraint"
)

// Where CREATE's object word may stand: CREATE [GLOBAL|LOCAL]
// [TEMP|UNLOGGED] TABLE, CREATE [UNIQUE] INDEX.
const (
	maxTableWord = 3
	maxIndexWord = 2
)

// statementRules reads a statement by its first word.
var statementRules = map[string]func(l *linter, n int, ts toks){
	"create":   (*linter).create,
	"alter":    (*linter).alter,
	"drop":     (*linter).drop,
	"truncate": (*linter).truncate,
	"delete":   (*linter).deleteFrom,
	"update":   (*linter).update,
	"set":      (*linter).setting,
}

// statement reads one statement by its leading words.
func (l *linter) statement(n int, ts toks) {
	if len(ts) == 0 {
		return
	}
	if rule, ok := statementRules[ts[0].word]; ok {
		rule(l, n, ts)
	}
}

// create notes a table the migration creates and reads an index it builds.
func (l *linter) create(n int, ts toks) {
	if t := ts.findWord(kwTable); t >= 0 && t <= maxTableWord {
		if name := ts.nameAfter(t + 1); name != "" {
			l.created[name] = true
		}
		return
	}
	if i := ts.findWord("index"); i >= 0 && i <= maxIndexWord {
		l.createIndex(n, ts)
	}
}

// alter reads an ALTER TABLE.
func (l *linter) alter(n int, ts toks) {
	if ts.at(1, kwTable) {
		l.alterTable(n, ts)
	}
}

// drop marks the live tables a DROP TABLE drops.
func (l *linter) drop(n int, ts toks) {
	if !ts.at(1, kwTable) {
		return
	}
	for _, name := range l.live(ts.namesAfter(2)) {
		l.add(Finding{
			Rule: RuleDropTable, Severity: SeverityDestructive, Statement: n, Object: name,
			Effect: "drops table " + name + " and every row in it",
		})
	}
}

// truncate marks the live tables a TRUNCATE empties.
func (l *linter) truncate(n int, ts toks) {
	for _, name := range l.live(ts.namesAfter(1)) {
		l.add(Finding{
			Rule: RuleTruncate, Severity: SeverityDestructive, Statement: n, Object: name,
			Effect: "removes every row of " + name,
		})
	}
}

// deleteFrom marks a DELETE with no WHERE.
func (l *linter) deleteFrom(n int, ts toks) {
	if ts.at(1, "from") {
		l.unbounded(n, ts, ts.nameAfter(2), RuleUnboundedDelete, "deletes every row of ")
	}
}

// update marks an UPDATE with no WHERE.
func (l *linter) update(n int, ts toks) {
	l.unbounded(n, ts, ts.nameAfter(1), RuleUnboundedUpdate, "rewrites every row of ")
}

// createIndex warns on an index built without CONCURRENTLY on a table this
// migration did not create: the build blocks every write to the table.
func (l *linter) createIndex(n int, ts toks) {
	if ts.findWord("concurrently") >= 0 {
		return
	}
	on := ts.findWord("on")
	if on < 0 {
		return
	}
	table := ts.nameAfter(on + 1)
	if table == "" || l.created[table] {
		return
	}
	l.add(Finding{
		Rule: RuleIndexLocks, Severity: SeverityWarning, Statement: n, Object: table,
		Effect: "builds the index while holding a lock that blocks every insert, update and delete on " + table,
		Safe:   "CREATE INDEX CONCURRENTLY, in a migration of its own added with transactional=false",
	})
}

// alterTable reads each action of an ALTER TABLE.
func (l *linter) alterTable(n int, ts toks) {
	table := ts.nameAfter(2)
	if table == "" {
		return
	}
	existing := !l.created[table]
	for _, action := range ts.actions(ts.afterName(2)) {
		l.alterAction(n, table, existing, action)
	}
}

// alterActions reads an ALTER TABLE action by its first word.
var alterActions = map[string]func(l *linter, n int, table string, existing bool, a toks){
	"add":    (*linter).alterAdd,
	"alter":  (*linter).alterColumn,
	"drop":   (*linter).alterDrop,
	"rename": (*linter).alterRename,
}

// alterAction reads one ALTER TABLE action.
func (l *linter) alterAction(n int, table string, existing bool, a toks) {
	if len(a) == 0 {
		return
	}
	if rule, ok := alterActions[a[0].word]; ok {
		rule(l, n, table, existing, a)
	}
}

// alterAdd reads an ADD: a constraint added without NOT VALID checks every
// row under a lock; a column, addColumn.
func (l *linter) alterAdd(n int, table string, existing bool, a toks) {
	if !existing {
		return
	}
	if !isConstraintAction(a) {
		l.addColumn(n, table, a)
		return
	}
	if (a.findWord("foreign") >= 0 || a.findWord("check") >= 0) && a.findWord("valid") < 0 {
		l.add(Finding{
			Rule: RuleConstraintValidates, Severity: SeverityWarning, Statement: n, Object: table,
			Effect: "checks every existing row of " + table + " while holding a lock that blocks writes to it",
			Safe:   "ADD CONSTRAINT ... NOT VALID, then VALIDATE CONSTRAINT in a later migration",
		})
	}
}

// alterColumn warns on a type change, which rewrites the table.
func (l *linter) alterColumn(n int, table string, existing bool, a toks) {
	if !existing || a.findWord("type") < 0 {
		return
	}
	l.add(Finding{
		Rule: RuleTypeChangeRewrites, Severity: SeverityWarning, Statement: n,
		Object: table + "." + a.nameAfter(skipWord(a, 1, kwColumn)),
		Effect: "rewrites " + table + " and rebuilds its indexes while holding a lock that blocks reads and writes, " +
			"unless the new type is binary coercible from the old (varchar to text, a wider varchar)",
		Safe: "add a column of the new type, backfill it in batches, and switch reads to it in a later migration",
	})
}

// alterDrop marks a dropped column.
func (l *linter) alterDrop(n int, table string, existing bool, a toks) {
	if !existing || a.at(1, kwConstr) {
		return
	}
	col := a.nameAfter(skipIfExists(a, skipWord(a, 1, kwColumn)))
	l.add(Finding{
		Rule: RuleDropColumn, Severity: SeverityDestructive, Statement: n, Object: table + "." + col,
		Effect: "drops column " + col + " of " + table + " and the value every row holds in it",
	})
}

// alterRename notes a renamed table or column.
func (l *linter) alterRename(n int, table string, _ bool, a toks) {
	switch {
	case a.at(1, "to"):
		l.renames = append(l.renames, Rename{Statement: n, Table: table})
	case a.at(1, kwConstr):
	default:
		if col := a.nameAfter(skipWord(a, 1, kwColumn)); col != "" {
			l.renames = append(l.renames, Rename{Statement: n, Table: table, Column: col})
		}
	}
}

// addColumn warns on a column added NOT NULL with no default (refused by a
// table with rows) and on a default PostgreSQL computes per row, which
// rewrites the table.
func (l *linter) addColumn(n int, table string, a toks) {
	col := a.nameAfter(skipIfNotExists(a, skipWord(a, 1, kwColumn)))
	def := a.findWord("default")
	if def < 0 && a.findPair("not", "null") >= 0 {
		l.add(Finding{
			Rule: RuleNotNullNoDefault, Severity: SeverityWarning, Statement: n, Object: table + "." + col,
			Effect: "fails on a table that holds rows, since every existing row would be null in a NOT NULL column",
			Safe:   "add the column with a constant DEFAULT, or add it nullable, backfill it, and set NOT NULL later",
		})
	}
	if def >= 0 && def+1 < len(a) && volatileDefaults[a[def+1].word] {
		l.add(Finding{
			Rule: RuleVolatileDefault, Severity: SeverityWarning, Statement: n, Object: table + "." + col,
			Effect: "rewrites every row of " + table + " to store a value computed per row by " + a[def+1].word +
				"(), holding a lock that blocks reads and writes",
			Safe: "add the column with no default or a constant one, then backfill it in batches",
		})
	}
}

// volatileDefaults are the functions whose default is computed per row, so
// adding a column with one rewrites the table. now() and current_timestamp
// are stable within a statement and are stored once, without a rewrite.
var volatileDefaults = map[string]bool{
	"random": true, "clock_timestamp": true, "gen_random_uuid": true, "uuid_generate_v4": true,
	"uuid_generate_v1": true, "timeofday": true, "nextval": true,
}

// isConstraintAction reports whether an ADD action adds a constraint rather
// than a column.
func isConstraintAction(a toks) bool {
	switch {
	case a.at(1, kwConstr), a.at(1, "primary"), a.at(1, "unique"), a.at(1, "foreign"), a.at(1, "check"), a.at(1, "exclude"):
		return true
	}
	return false
}

// unbounded marks a DELETE or UPDATE with no WHERE at its top level.
func (l *linter) unbounded(n int, ts toks, table, rule, effect string) {
	if table == "" || l.created[table] || ts.topLevelWord("where") {
		return
	}
	l.add(Finding{Rule: rule, Severity: SeverityDestructive, Statement: n, Object: table, Effect: effect + table})
}

// setting warns on a lock_timeout set longer than the one the platform runs
// a migration with, or turned off: a statement then queues behind a long
// read while every later query queues behind it.
func (l *linter) setting(n int, ts toks) {
	i := skipWord(ts, skipWord(ts, 1, "local"), "session")
	if !ts.at(i, "lock_timeout") || i+2 >= len(ts) {
		return
	}
	d, ok := pgDuration(ts[i+2].text)
	if !ok || (d != 0 && d <= migrationLockTimeout) {
		return
	}
	effect := fmt.Sprintf("waits up to %s for a lock a running query holds, while every later query on the table waits behind it", d)
	if d == 0 {
		effect = "waits without limit for a lock a running query holds, while every later query on the table waits behind it"
	}
	l.add(Finding{
		Rule: RuleLockTimeoutRaised, Severity: SeverityWarning, Statement: n, Object: "lock_timeout",
		Effect: effect, Safe: fmt.Sprintf("leave lock_timeout at the %s the platform sets, and retry the apply if it times out", migrationLockTimeout),
	})
}

// pgDuration reads a lock_timeout value as PostgreSQL does: a number of
// milliseconds, or a number with a unit.
func pgDuration(v string) (time.Duration, bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	end := 0
	for end < len(v) && (v[end] >= '0' && v[end] <= '9' || v[end] == '.') {
		end++
	}
	num, err := strconv.ParseFloat(v[:end], float64Bits)
	if err != nil {
		return 0, false
	}
	unit, ok := durationUnits[strings.TrimSpace(v[end:])]
	if !ok {
		return 0, false
	}
	return time.Duration(num * float64(unit)), true
}

// float64Bits is the precision a duration's number is read at.
const float64Bits = 64

var durationUnits = map[string]time.Duration{
	"": time.Millisecond, "us": time.Microsecond, "ms": time.Millisecond, "s": time.Second,
	"min": time.Minute, "h": time.Hour, "d": 24 * time.Hour,
}

// tok is one token of a statement: a word or identifier (lower-cased unless
// it was quoted), a literal's text, or one byte of punctuation.
type tok struct {
	word string // the word or identifier, "" for anything else
	text string // what the token reads as: the identifier, a literal's content, the punctuation
}

// toks is a statement's tokens.
type toks []tok

// lex tokenizes a statement, dropping comments and dollar-quoted bodies.
func lex(s string) toks {
	var out toks
	for i := 0; i < len(s); {
		t, next, keep := lexAt(s, i)
		if keep {
			out = append(out, t)
		}
		i = next
	}
	return out
}

// lexAt reads the token at i: what it is, where the next starts, and whether
// it is kept.
func lexAt(s string, i int) (t tok, next int, keep bool) {
	c := s[i]
	switch {
	case strings.IndexByte(" \t\n\r", c) >= 0:
		return tok{}, i + 1, false
	case pglex.SkipComment(s, i) > i:
		return tok{}, pglex.SkipComment(s, i), false
	case c == '\'', c == '"':
		return lexQuoted(s, i)
	case dollarEnd(s, i) > i:
		return tok{text: "$$"}, dollarEnd(s, i), true
	case pglex.IsIdentByte(c):
		return lexWord(s, i)
	}
	return tok{text: string(c)}, i + 1, true
}

// dollarEnd is past a dollar-quoted body starting at i, or i when none does.
func dollarEnd(s string, i int) int {
	if s[i] != '$' {
		return i
	}
	return pglex.SkipDollarQuoted(s, i)
}

// lexWord reads a word or unquoted identifier, lower-cased.
func lexWord(s string, i int) (t tok, next int, keep bool) {
	j := i
	for j < len(s) && (pglex.IsIdentByte(s[j]) || s[j] == '$') {
		j++
	}
	w := strings.ToLower(s[i:j])
	return tok{word: w, text: w}, j, true
}

// lexQuoted reads a string literal (its content) or a quoted identifier (its
// name, case kept).
func lexQuoted(s string, i int) (t tok, next int, keep bool) {
	c := s[i]
	end := pglex.SkipString(s, i, c == '\'' && pglex.IsEString(s, i))
	inner := strings.ReplaceAll(s[i+1:max(i+1, end-1)], string(c)+string(c), string(c))
	t = tok{text: inner}
	if c == '"' {
		t.word = inner
	}
	return t, end, true
}

// at reports whether token i is the word w.
func (ts toks) at(i int, w string) bool { return i >= 0 && i < len(ts) && ts[i].word == w }

// findWord is the index of the first top-level w, or -1.
func (ts toks) findWord(w string) int {
	depth := 0
	for i, t := range ts {
		switch t.text {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 && t.word == w {
			return i
		}
	}
	return -1
}

// findPair is the index of w1 followed by w2, or -1.
func (ts toks) findPair(w1, w2 string) int {
	for i := 0; i+1 < len(ts); i++ {
		if ts[i].word == w1 && ts[i+1].word == w2 {
			return i
		}
	}
	return -1
}

// topLevelWord reports whether w appears outside parentheses.
func (ts toks) topLevelWord(w string) bool { return ts.findWord(w) >= 0 }

// nameAfter is the object name starting at i, past IF [NOT] EXISTS and ONLY:
// its last part when it is schema-qualified.
func (ts toks) nameAfter(i int) string {
	i = skipWord(ts, skipIfNotExists(ts, skipIfExists(ts, i)), "only")
	if i >= len(ts) || ts[i].word == "" {
		return ""
	}
	name := ts[i].word
	for i+2 < len(ts) && ts[i+1].text == "." && ts[i+2].word != "" {
		name, i = ts[i+2].word, i+2
	}
	return name
}

// afterName is the index just past the object name starting at i.
func (ts toks) afterName(i int) int {
	i = skipWord(ts, skipIfNotExists(ts, skipIfExists(ts, i)), "only")
	if i >= len(ts) {
		return i
	}
	i++
	for i+1 < len(ts) && ts[i].text == "." {
		i += 2
	}
	return i
}

// namesAfter is every comma-separated object name starting at i, up to a
// word that ends the list (CASCADE, RESTRICT, RESTART, CONTINUE).
func (ts toks) namesAfter(i int) []string {
	var out []string
	i = skipWord(ts, i, kwTable)
	for i < len(ts) {
		name := ts.nameAfter(i)
		if name == "" || listEnd[name] {
			break
		}
		out = append(out, name)
		i = ts.afterName(i)
		if i >= len(ts) || ts[i].text != "," {
			break
		}
		i++
	}
	return out
}

var listEnd = map[string]bool{"cascade": true, "restrict": true, "restart": true, "continue": true}

// actions splits the tokens from i into the comma-separated actions of an
// ALTER TABLE, at the commas outside parentheses.
func (ts toks) actions(i int) []toks {
	var out []toks
	depth, start := 0, i
	for j := i; j < len(ts); j++ {
		switch ts[j].text {
		case "(":
			depth++
		case ")":
			depth--
		case ",":
			if depth == 0 {
				out, start = append(out, ts[start:j]), j+1
			}
		}
	}
	if start < len(ts) {
		out = append(out, ts[start:])
	}
	return out
}

// skipWord is i+1 when token i is w, else i.
func skipWord(ts toks, i int, w string) int {
	if ts.at(i, w) {
		return i + 1
	}
	return i
}

// skipIfExists is past IF EXISTS at i.
func skipIfExists(ts toks, i int) int {
	if ts.at(i, "if") && ts.at(i+1, "exists") {
		return i + 2
	}
	return i
}

// ifNotExistsWords is the length of IF NOT EXISTS in tokens.
const ifNotExistsWords = 3

// skipIfNotExists is past IF NOT EXISTS at i.
func skipIfNotExists(ts toks, i int) int {
	if ts.at(i, "if") && ts.at(i+1, "not") && ts.at(i+2, "exists") {
		return i + ifNotExistsWords
	}
	return i
}
