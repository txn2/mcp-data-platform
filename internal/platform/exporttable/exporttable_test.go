package exporttable

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	spec, err := Parse(nil)
	require.NoError(t, err)
	assert.Nil(t, spec, "no argument asks for no table")

	spec, err = Parse(map[string]any{"connection": " warehouse ", "table_name": " orders ", "follow": false})
	require.NoError(t, err)
	assert.Equal(t, &Spec{Connection: "warehouse", TableName: "orders", Follow: false}, spec)

	spec, err = Parse(map[string]any{"connection": "warehouse"})
	require.NoError(t, err)
	assert.True(t, spec.Follow, "a table follows its file unless pinned, as manage_table's does")

	for _, tt := range []struct {
		name string
		v    any
		want string
	}{
		{"not a dict", "warehouse", "register must be a dict"},
		{"no connection", map[string]any{}, "register needs a connection"},
		{"a blank connection", map[string]any{"connection": "  "}, "register needs a connection"},
		{"a non-string connection", map[string]any{"connection": 1}, "register connection must be a string"},
		{"a non-bool follow", map[string]any{"connection": "w", "follow": "yes"}, "register follow must be True or False"},
		{"an unknown key", map[string]any{"connection": "w", "repair": true}, `register has no key "repair"; it takes connection, follow, table_name`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.v)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestSpec_Check(t *testing.T) {
	spec := &Spec{Connection: "w"}
	require.NoError(t, spec.Check("jsonl", true))
	require.NoError(t, spec.Check("csv", true))
	assert.ErrorContains(t, spec.Check("json", true), `register needs format="jsonl" or format="csv"`)
	assert.ErrorContains(t, spec.Check("markdown", true), `got "markdown"`)
	assert.ErrorContains(t, spec.Check("jsonl", false), "a bucket destination delivers the file out of the platform")
}

func TestSpec_Args(t *testing.T) {
	assert.Equal(t, map[string]any{
		"action": "register", "reference": "mcp:resource:r1", "connection": "w", "follow": true, "table_name": "t",
	}, (&Spec{Connection: "w", TableName: "t", Follow: true}).Args("mcp:resource:r1"))

	args := (&Spec{Connection: "w"}).Args("mcp:asset:a1")
	_, named := args["table_name"]
	assert.False(t, named, "an unnamed table takes manage_table's default name")
	assert.Equal(t, false, args["follow"])
}

func TestReference(t *testing.T) {
	assert.Equal(t, "mcp:resource:r1", Reference("mcp:resource:r1", ""))
	assert.Equal(t, "mcp:asset:a1", Reference("", "a1"))
}

func TestFromResultAndMap(t *testing.T) {
	table := FromResult(map[string]any{
		"connection": "w", "query_table": "scratch.s.t", "registration_id": "reg_1",
		"columns": []any{"id", 7, "note"}, "format": "jsonl", "follow": true,
	})
	assert.Equal(t, &Table{
		Connection: "w", QueryTable: "scratch.s.t", RegistrationID: "reg_1",
		Columns: []string{"id", "note"}, Format: "jsonl", Follow: true,
	}, table)
	assert.Equal(t, map[string]any{
		"connection": "w", "follow": true, "preview": false, "columns": []any{"id", "note"},
		"query_table": "scratch.s.t", "registration_id": "reg_1", "format": "jsonl",
	}, table.Map())

	preview := (&Spec{Connection: "w", Follow: true}).Preview()
	assert.Equal(t, map[string]any{
		"connection": "w", "follow": true, "preview": true, "columns": []any{},
	}, preview.Map(), "a preview names no table, because none was made")
}
