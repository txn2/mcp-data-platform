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
// A CSV's columns are all VARCHAR, because Hive CSV admits nothing else, and
// skipping the header line is what keeps its column names out of its rows. A
// JSON-lines or Parquet table declares the type each column carries (#1833).
func BuildDDL(r Registration, replacing bool) []string {
	stmts := make([]string, 0, 3)
	stmts = append(stmts,
		"CREATE SCHEMA IF NOT EXISTS "+QuoteIdentifier(r.Catalog)+"."+QuoteIdentifier(r.Schema))
	if replacing {
		stmts = append(stmts, dropTableStatement(r))
	}
	stmts = append(stmts, createTableStatement(r))
	return stmts
}

// dropTableStatement renders the drop of a registration's table. It is one
// function because three callers have to agree on the exact text: BuildDDL
// issues it as part of a replacement, Unregister issues it on its own, and
// Register compares against it to learn whether a replacement's drop is among
// the statements that ran before the DDL failed.
func dropTableStatement(r Registration) string {
	return "DROP TABLE IF EXISTS " + qualified(r)
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
	_, _ = b.WriteString(", " + formatProperties(r.FormatOrDefault()) + ")")
	return b.String()
}

// formatProperties renders the table properties that choose a registration's
// reader.
//
// A JSON-lines table needs only the format: the JSON reader finds each value
// by its key, so there is no header to skip and no escape to declare, and
// every string arrives exactly as written (#1820). A Parquet table needs only
// the format too: the reader finds each column in the file by name.
func formatProperties(format string) string {
	switch format {
	case FormatJSONLines:
		return "format = 'JSON'"
	case FormatParquet:
		return "format = 'PARQUET'"
	}
	return "format = 'CSV', skip_header_line_count = 1, csv_escape = " + noCSVEscape
}

// noCSVEscape is the csv_escape a registration declares: NUL, which the Hive
// CSV reader takes as "no escape character" (#1819).
//
// Left unset, the reader escapes with a backslash, a dialect no writer of these
// files uses: every CSV the platform writes, and RFC 4180, double a quote and
// treat a backslash as an ordinary character. Under the default, "back\slash"
// read as "backslash", and a quoted field holding a backslash beside a quote
// read as an empty string, with the row and column counts intact. Setting the
// escape to the quote character does not help; NUL is the value that turns
// escaping off, and it cannot collide with a value: internal/platform/tablecsv
// refuses a file with a NUL byte in it before a table is registered over it.
const noCSVEscape = `U&'\0000'`

// SampleJoinSQL renders a statement showing how the registered table is used:
// a SELECT over it, and a join to a warehouse table.
//
// A CSV's columns are all VARCHAR, so the join casts, and says why: a reader
// who writes the obvious join against a CSV table gets a type error and no
// explanation of it. A JSON-lines or Parquet table declares its columns'
// types, and its join is the plain one (#1833).
func SampleJoinSQL(r Registration) string {
	if len(r.Columns) == 0 {
		return ""
	}
	first := QuoteIdentifier(r.Columns[0].Name)
	if r.FormatOrDefault() != FormatCSV && !r.AllVarchar {
		return "SELECT * FROM " + r.QualifiedName() +
			"\n-- JOIN " + r.QualifiedName() + " t ON w.id = t." + first
	}
	return "SELECT * FROM " + r.QualifiedName() +
		"\n-- every column is VARCHAR, so a join to a typed column casts:" +
		"\n-- JOIN " + r.QualifiedName() + " t ON w.id = CAST(t." + first + " AS BIGINT)"
}
