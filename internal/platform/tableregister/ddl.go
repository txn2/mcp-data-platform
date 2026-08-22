package tableregister

import "strings"

// qualified renders a registration's table as a statement names it, each part
// quoted.
func qualified(r Registration) string {
	return QuoteIdentifier(r.Catalog) + "." + QuoteIdentifier(r.Schema) + "." + QuoteIdentifier(r.Table)
}

// BuildDDL returns the statements that make a registration, in the order they
// must run.
//
// CREATE SCHEMA comes first and is IF NOT EXISTS: the scratch schema is the
// target of every registration on a connection and the first one to arrive has
// to make it. DROP TABLE is issued only when replacing a registration the
// caller is entitled to replace -- an unconditional drop would let a name
// collision quietly take out somebody else's table, which is why the decision
// is made before this is called rather than here.
//
// Every column is VARCHAR because Hive CSV admits nothing else; skipping the
// header line is what keeps the column names out of the rows.
func BuildDDL(r Registration, replacing bool) []string {
	stmts := make([]string, 0, 3)
	stmts = append(stmts,
		"CREATE SCHEMA IF NOT EXISTS "+QuoteIdentifier(r.Catalog)+"."+QuoteIdentifier(r.Schema))
	if replacing {
		stmts = append(stmts, "DROP TABLE IF EXISTS "+qualified(r))
	}
	stmts = append(stmts, createTableStatement(r))
	return stmts
}

// createTableStatement renders the CREATE TABLE for a registration.
func createTableStatement(r Registration) string {
	cols := make([]string, 0, len(r.Columns))
	for _, c := range r.Columns {
		cols = append(cols, QuoteIdentifier(c.Name)+" "+c.Type)
	}

	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(qualified(r))
	b.WriteString(" (")
	b.WriteString(strings.Join(cols, ", "))
	b.WriteString(") WITH (external_location = ")
	b.WriteString(QuoteLiteral(r.Location))
	b.WriteString(", format = 'CSV', skip_header_line_count = 1)")
	return b.String()
}

// SampleJoinSQL renders a statement showing how the registered table is used:
// a SELECT over it, with the CAST that joining it to a typed warehouse column
// requires. Every column is VARCHAR, so a reader who writes the obvious join
// gets a type error and no explanation of why; this is the explanation.
func SampleJoinSQL(r Registration) string {
	if len(r.Columns) == 0 {
		return ""
	}
	first := QuoteIdentifier(r.Columns[0].Name)
	return "SELECT * FROM " + r.QualifiedName() +
		"\n-- every column is VARCHAR, so a join to a typed column casts:" +
		"\n-- JOIN " + r.QualifiedName() + " t ON w.id = CAST(t." + first + " AS BIGINT)"
}
