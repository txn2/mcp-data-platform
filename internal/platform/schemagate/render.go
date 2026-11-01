package schemagate

import (
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Bounds on what one refusal or error carries. A column comment and a catalog
// description are text anyone who can write the table's metadata chose, so
// they are cut rather than repeated whole.
const (
	maxColumnsPerTable = 300
	maxTextRunes       = 240
)

// codeSchemaRequired is the refusal's stable error code, and its category.
const codeSchemaRequired = "schema_required"

// Column is one column of a table, as the engine and the catalog describe it.
type Column struct {
	Name          string
	Type          string
	Comment       string
	Description   string
	GlossaryTerms []string
}

// refusalResult is the refusal of a statement naming tables the session has
// not read, with their schemas.
func refusalResult(tool string, tables []describedTable) mcp.Result {
	var b strings.Builder
	fmt.Fprintf(&b, "SCHEMA_REQUIRED: %s names a table whose columns this session has not read, "+
		"so its SQL was not run. A guessed column that happens to exist returns rows that look right and are wrong. "+
		"The schema is below: check every column and type the statement uses against it, correct the statement, then send it again.\n",
		tool)
	writeTables(&b, tables)
	return middleware.BuildErrorResult(middleware.NewToolError(
		codeSchemaRequired, codeSchemaRequired, b.String(),
		"Correct the statement against the columns listed, then send it again. "+
			"This is a workflow requirement, not a platform outage.",
	))
}

// failureColumnsText is the text added to a statement that failed on a column
// or a type.
func failureColumnsText(tables []describedTable) string {
	var b strings.Builder
	_, _ = b.WriteString("The statement failed on a column or a type. These are the real columns of the tables it names; " +
		"correct the statement against them rather than guessing again.\n")
	writeTables(&b, tables)
	return b.String()
}

func writeTables(b *strings.Builder, tables []describedTable) {
	for _, t := range tables {
		fmt.Fprintf(b, "\nTable %s (connection %s):\n", t.ref.catalog+"."+t.ref.schema+"."+t.ref.table, t.ref.connection)
		for i, c := range t.columns {
			if i == maxColumnsPerTable {
				fmt.Fprintf(b, "- ... %d more columns; read them all with trino_describe_table\n", len(t.columns)-maxColumnsPerTable)
				break
			}
			_, _ = b.WriteString(columnLine(c))
		}
	}
}

func columnLine(c Column) string {
	line := "- " + c.Name + " " + c.Type
	notes := make([]string, 0, 3)
	if c.Description != "" {
		notes = append(notes, clip(c.Description))
	}
	if c.Comment != "" && c.Comment != c.Description {
		notes = append(notes, "comment: "+clip(c.Comment))
	}
	if len(c.GlossaryTerms) > 0 {
		notes = append(notes, "glossary: "+clip(strings.Join(c.GlossaryTerms, ", ")))
	}
	if len(notes) > 0 {
		line += ": " + strings.Join(notes, "; ")
	}
	return line + "\n"
}

// clip shortens text to maxTextRunes runes on one line.
func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= maxTextRunes {
		return s
	}
	return string(r[:maxTextRunes]) + "..."
}
