package toolkit

// SchemaFirst is the sentence every SQL tool's description carries (#2385):
// know a table's columns and types before writing SQL against it. A search hit
// names a table and describes it; it does not list its columns, so guidance
// that leads from a search hit straight to SQL leads to guessed column names.
// A guess that fails costs a retry. A guess that happens to name a real column
// returns rows that look right and are wrong, and nothing reports it.
//
// It lives here because two packages author SQL tool descriptions: the
// platform's description overrides (trino_query, trino_execute) and the Trino
// toolkit's own trino_export, which carries no override. The schema
// requirement is for the SQL tools alone; API and GraphQL tools do not carry
// it.
const SchemaFirst = "IMPORTANT: Know every table's columns and their types before you write SQL against it. " +
	"Read them with trino_describe_table, or fetch the table's mcp:dataset reference. " +
	"Never guess a column name. Guessing schema is dangerous: a guessed column that happens to exist " +
	"(filtering on id when the record id is p_id) returns rows that look right and are wrong, " +
	"and nothing reports the mistake."
