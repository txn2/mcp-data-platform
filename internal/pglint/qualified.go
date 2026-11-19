package pglint

// RuleSchemaQualified refuses a migration that names an object qualified by
// its own schema (#2709). An application's migrations are written once and
// applied to every environment's schema with the search path set to it, so a
// name left unqualified lands in the schema the migration is applied to; one
// qualified by the schema the migration was written for would create or
// change that environment's objects from another environment's release.
const RuleSchemaQualified = "schema_qualified"

// schemaQualified refuses each name qualified by the target's schema, in the
// statement and in a function's or DO block's body. Silent when the target
// names no schema.
func (l *linter) schemaQualified(n int, stmt string, ts toks) {
	schema := l.sec.target.Schema
	if schema == "" {
		return
	}
	seen := map[string]bool{}
	for _, name := range qualifiedBy(append(ts, lex(dollarBody(stmt))...), schema) {
		if seen[name] {
			continue
		}
		seen[name] = true
		l.add(Finding{
			Rule: RuleSchemaQualified, Severity: SeverityRefused, Statement: n, Object: schema + "." + name,
			Effect: "names " + name + " in schema " + schema + ": the migration applies to every environment's schema, and a " +
				"qualified name would reach " + schema + " from whichever environment applies it",
			Safe: "write " + name + " without the schema: the platform applies the migration with the search path set to " +
				"each environment's schema",
		})
	}
}

// qualifiedBy is each name ts qualifies with schema: the word after
// schema and a dot.
func qualifiedBy(ts toks, schema string) []string {
	var out []string
	for i := 0; i+2 < len(ts); i++ {
		if ts[i].word == schema && ts[i+1].text == "." && ts[i+2].word != "" {
			out = append(out, ts[i+2].word)
		}
	}
	return out
}
