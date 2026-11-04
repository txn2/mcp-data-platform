package postgres

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bulkConfig() Config {
	cfg := baseConfig()
	cfg.IterMaxRows, cfg.CopyMaxRows, cfg.CopyMaxBytes = 5, 3, 64
	return cfg
}

func cursorStatements() map[string]fakeStatement {
	page := func(from int32) [][]any { return [][]any{{from}, {from + 1}} }
	return map[string]fakeStatement{
		"SELECT n FROM t WHERE n > $1":      {params: []uint32{oidInt4}, fields: []pgconn.FieldDescription{field("n", oidInt4)}},
		"FETCH FORWARD 2 FROM mcp_cursor":   {fields: []pgconn.FieldDescription{field("n", oidInt4)}, rows: page(1), tag: "FETCH 2"},
		"FETCH FORWARD 2 FROM mcp_cursor_1": {fields: []pgconn.FieldDescription{field("n", oidInt4)}, rows: page(1)[:1], tag: "FETCH 1"},
		"SELECT bad":                        {prepErr: &pgconn.PgError{Code: "42601", Message: "syntax error"}},
	}
}

func TestCursorReadsPagesUpToTheLoopBound(t *testing.T) {
	tk, opened := txToolkit(bulkConfig(), cursorStatements())
	ctx := context.Background()
	k, err := tk.OpenCursor(ctx, Statement{Connection: "db", SQL: "SELECT n FROM t WHERE n > $1", Params: []any{"0"}}, 2)
	require.NoError(t, err)
	fake := (*opened)[0]
	assert.Equal(t, pgx.ReadOnly, fake.opts.AccessMode, "a cursor of its own reads in a READ ONLY transaction")
	assert.Equal(t, []string{"DECLARE mcp_cursor NO SCROLL CURSOR FOR SELECT n FROM t WHERE n > $1"}, fake.execs)
	assert.Equal(t, []Column{{Name: "n", Type: "integer"}}, k.Columns())

	for range 2 {
		page, done, err := k.Next(ctx)
		require.NoError(t, err)
		assert.False(t, done, "a full page may have more after it")
		assert.Equal(t, [][]any{{int64(1)}, {int64(2)}}, page.Rows)
	}
	_, _, err = k.Next(ctx)
	var limit *IterLimitError
	require.ErrorAs(t, err, &limit, "six rows pass iter_max_rows of five")
	assert.Contains(t, limit.Error(), "iter_max_rows (5)")

	k.Close()
	k.Close()
	assert.True(t, fake.rolledBack, "closing ends the cursor's own transaction")
	_, _, err = k.Next(ctx)
	require.ErrorContains(t, err, "closed")
}

func TestCursorInTheTransaction(t *testing.T) {
	tk, opened := txToolkit(bulkConfig(), cursorStatements())
	ctx := context.Background()
	x, err := tk.Begin(ctx, TxOptions{Connection: "db"})
	require.NoError(t, err)
	k, err := x.OpenCursor(ctx, Statement{SQL: "SELECT n FROM t WHERE n > $1", Params: []any{"0"}}, 2)
	require.NoError(t, err)
	page, done, err := k.Next(ctx)
	require.NoError(t, err)
	assert.True(t, done, "a short page is the last")
	assert.Len(t, page.Rows, 1)
	k.Close()
	assert.False(t, (*opened)[0].rolledBack, "a cursor in the script's transaction leaves it open")

	_, err = x.OpenCursor(ctx, Statement{SQL: "SELECT bad"}, 2)
	require.Error(t, err)
	_, err = x.OpenCursor(ctx, Statement{SQL: "SELECT n FROM t WHERE n > $1"}, 2)
	require.Error(t, err, "the transaction ended with the failed statement")
	_, err = x.OpenCursor(ctx, Statement{}, 2)
	require.Error(t, err)
	_, err = x.OpenCursor(ctx, Statement{SQL: "SELECT $1", Params: 5}, 2)
	require.Error(t, err)
}

func TestCursorRefusals(t *testing.T) {
	tk, _ := txToolkit(bulkConfig(), cursorStatements())
	ctx := context.Background()
	_, err := tk.OpenCursor(ctx, Statement{Connection: "db", SQL: "SELECT n FROM t WHERE n > $1"}, 2)
	require.ErrorContains(t, err, "takes 1 parameters")
	_, err = tk.OpenCursor(ctx, Statement{Connection: "db", SQL: "COMMIT"}, 2)
	require.Error(t, err)
	_, err = tk.OpenCursor(ctx, Statement{Connection: "nope", SQL: "SELECT 1"}, 2)
	require.Error(t, err)
	assert.Equal(t, 1000, pageSize(baseConfig(), 0))
	assert.Equal(t, MaxMaxRows, pageSize(baseConfig(), MaxMaxRows+1))
	assert.Equal(t, 7, pageSize(baseConfig(), 7))
}

func TestCheckCopyIn(t *testing.T) {
	c := &conn{cfg: bulkConfig()}
	ok := CopyIn{Table: "shop.items", Columns: []string{"sku", "qty"}, Rows: [][]any{{"a", 1}}}
	require.NoError(t, c.checkCopyIn(ok))
	for name, in := range map[string]CopyIn{
		"no table":    {Columns: []string{"a"}},
		"three parts": {Table: "a.b.c", Columns: []string{"a"}},
		"no columns":  {Table: "t"},
		"empty name":  {Table: "t", Columns: []string{" "}},
		"too many":    {Table: "t", Columns: []string{"a"}, Rows: [][]any{{1}, {2}, {3}, {4}}},
		"wide row":    {Table: "t", Columns: []string{"a"}, Rows: [][]any{{1, 2}}},
	} {
		var refused *RefusedError
		require.ErrorAs(t, c.checkCopyIn(in), &refused, name)
	}
	id, err := tableIdentifier("shop.items")
	require.NoError(t, err)
	assert.Equal(t, `"shop"."items"`, id.Sanitize())
}

func TestEncodeCopy(t *testing.T) {
	c := &conn{cfg: bulkConfig()}
	types := []typeInfo{fakeTypes()[oidText], fakeTypes()[oidInt4]}
	buf, err := c.encodeCopy([][]any{{"a\tb\\c\nd", 1}, {nil, 2}}, types)
	require.NoError(t, err)
	assert.Equal(t, "a\\tb\\\\c\\nd\t1\n\\N\t2\n", buf.String())
	_, err = c.encodeCopy([][]any{{"x", []any{"not", "an", "int"}}, {"a long enough value to pass sixty four bytes of copy", 1}}, types)
	require.Error(t, err)
	_, err = c.encodeCopy([][]any{{"a long enough value to pass the sixty four byte copy_max_bytes", 1}, {"more", 2}}, types)
	require.ErrorContains(t, err, "copy_max_bytes")
}

func TestCopyRefusalsBeforeAnythingIsSent(t *testing.T) {
	cfg := bulkConfig()
	tk, opened := txToolkit(cfg, nil)
	ctx := context.Background()
	_, err := tk.CopyIn(ctx, CopyIn{Connection: "nope"})
	require.Error(t, err)
	_, err = tk.CopyIn(ctx, CopyIn{Connection: "db", Table: "t", Columns: []string{"a"}, Rows: [][]any{{1}, {2}, {3}, {4}}})
	require.ErrorContains(t, err, "copy_max_rows")
	assert.Empty(t, *opened, "a refused copy opens no transaction")
	_, err = tk.CopyOut(ctx, Statement{Connection: "db", SQL: "SELECT 1"}, "xml", &bytes.Buffer{}, 0)
	require.ErrorContains(t, err, "format")

	cfg.ReadOnly = true
	ro, _ := txToolkit(cfg, nil)
	var refused *RefusedError
	_, err = ro.CopyIn(ctx, CopyIn{Connection: "db", Table: "t", Columns: []string{"a"}})
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, CodeReadOnly, refused.Code)

	x, err := tk.Begin(ctx, TxOptions{Connection: "db", ReadOnly: true})
	require.NoError(t, err)
	_, err = x.CopyIn(ctx, CopyIn{Table: "t", Columns: []string{"a"}})
	require.ErrorAs(t, err, &refused)
	y, err := tk.Begin(ctx, TxOptions{Connection: "db"})
	require.NoError(t, err)
	_, err = y.CopyIn(ctx, CopyIn{Columns: []string{"a"}})
	require.Error(t, err)
}

// rowsOf serves fixed rows as a result reader does.
type rowsOf struct {
	rows   [][][]byte
	i      int
	closed bool
	err    error
}

func (r *rowsOf) NextRow() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}
func (r *rowsOf) Values() [][]byte { return r.rows[r.i-1] }
func (r *rowsOf) Close() (pgconn.CommandTag, error) {
	r.closed = true
	return pgconn.NewCommandTag("SELECT"), r.err
}

func TestStreamRowsWritesCOPYsFormats(t *testing.T) {
	src := func() *rowsOf {
		return &rowsOf{rows: [][][]byte{{[]byte("1"), []byte(`a,"b"`)}, {[]byte("2"), nil}, {[]byte("3"), []byte("")}}}
	}
	cols := []Column{{Name: "id"}, {Name: "note"}}

	var csv bytes.Buffer
	res := &CopyOutResult{Columns: cols}
	require.NoError(t, streamRows(src(), res, copySink{w: &csv, format: CopyFormatCSV, limit: 1 << 20, rows: 10}))
	assert.Equal(t, "id,note\n1,\"a,\"\"b\"\"\"\n2,\n3,\"\"\n", csv.String(), "null is empty, an empty string is quoted")
	assert.EqualValues(t, 3, res.Rows)
	assert.EqualValues(t, csv.Len(), res.Bytes)

	var text bytes.Buffer
	require.NoError(t, streamRows(src(), &CopyOutResult{Columns: cols}, copySink{w: &text, format: CopyFormatText, limit: 1 << 20, rows: 10}))
	assert.Equal(t, "id\tnote\n1\ta,\"b\"\n2\t\\N\n3\t\n", text.String())

	r := src()
	err := streamRows(r, &CopyOutResult{Columns: cols}, copySink{w: &bytes.Buffer{}, format: CopyFormatCSV, limit: 1 << 20, rows: 2})
	require.ErrorContains(t, err, "copy_max_rows (2)")
	assert.True(t, r.closed)
	err = streamRows(src(), &CopyOutResult{Columns: cols}, copySink{w: &bytes.Buffer{}, format: CopyFormatCSV, limit: 12, rows: 10})
	require.ErrorContains(t, err, "more than 12 bytes")
	err = streamRows(src(), &CopyOutResult{Columns: cols}, copySink{w: &bytes.Buffer{}, format: CopyFormatCSV, limit: 3, rows: 10})
	require.Error(t, err, "even the header is bounded")
	failing := src()
	failing.err = errors.New("server went away")
	require.ErrorContains(t, streamRows(failing, &CopyOutResult{Columns: cols}, copySink{w: &bytes.Buffer{}, format: CopyFormatCSV, limit: 1 << 20, rows: 10}), "went away")
}

// A copy the server refuses to describe fails as its statement does: on a
// connection, with nothing committed, and in a transaction, which it ends.
func TestCopyFailuresBeforeTheData(t *testing.T) {
	refused := &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}
	stmts := map[string]fakeStatement{
		`SELECT "sku" FROM "missing" LIMIT 0`: {prepErr: refused},
		"SELECT bad":                          {prepErr: refused},
		"SELECT n FROM t WHERE n > $1":        {params: []uint32{oidInt4}, fields: []pgconn.FieldDescription{field("n", oidInt4)}},
	}
	tk, opened := txToolkit(bulkConfig(), stmts)
	ctx := context.Background()
	in := CopyIn{Connection: "db", Table: "missing", Columns: []string{"sku"}, Rows: [][]any{{"a"}}}
	_, err := tk.CopyIn(ctx, in)
	var failed *FailedError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, "42P01", failed.Statement.SQLState)
	assert.False(t, (*opened)[0].committed)

	x, err := tk.Begin(ctx, TxOptions{Connection: "db"})
	require.NoError(t, err)
	_, err = x.CopyIn(ctx, CopyIn{Table: "missing", Columns: []string{"sku"}})
	require.ErrorAs(t, err, &failed)
	_, err = x.CopyIn(ctx, CopyIn{Table: "missing", Columns: []string{"sku"}})
	require.ErrorContains(t, err, "is over", "the failed copy ended the transaction")

	_, err = tk.CopyOut(ctx, Statement{Connection: "db", SQL: "SELECT bad"}, CopyFormatCSV, &bytes.Buffer{}, 0)
	require.ErrorAs(t, err, &failed)
	_, err = tk.CopyOut(ctx, Statement{Connection: "db", SQL: "SELECT n FROM t WHERE n > $1"}, CopyFormatText, &bytes.Buffer{}, 0)
	require.ErrorContains(t, err, "takes 1 parameters")
	_, err = tk.CopyOut(ctx, Statement{Connection: "db", SQL: "COPY t TO STDOUT"}, CopyFormatCSV, &bytes.Buffer{}, 0)
	require.ErrorContains(t, err, "a COPY statement is not run")
	_, err = tk.CopyOut(ctx, Statement{Connection: "nope", SQL: "SELECT 1"}, CopyFormatCSV, &bytes.Buffer{}, 0)
	require.Error(t, err)

	_, err = tk.OpenCursor(ctx, Statement{Connection: "db", SQL: "SELECT bad"}, 2)
	require.ErrorAs(t, err, &failed)
}

// A row or parameter that does not encode by its column's type is refused
// before anything is sent.
func TestCopyRefusesAValueItsTypeDoesNotTake(t *testing.T) {
	stmts := map[string]fakeStatement{
		`SELECT "blob" FROM "files" LIMIT 0`: {fields: []pgconn.FieldDescription{field("blob", oidBytea)}},
		"SELECT n FROM f WHERE blob = $1":    {params: []uint32{oidBytea}, fields: []pgconn.FieldDescription{field("n", oidInt4)}},
	}
	tk, _ := txToolkit(bulkConfig(), stmts)
	ctx := context.Background()
	_, err := tk.CopyIn(ctx, CopyIn{Connection: "db", Table: "files", Columns: []string{"blob"}, Rows: [][]any{{"not base64!"}}})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Error(), "row 1")
	_, err = tk.CopyOut(ctx, Statement{Connection: "db", SQL: "SELECT n FROM f WHERE blob = $1", Params: []any{"not base64!"}}, CopyFormatCSV, &bytes.Buffer{}, 0)
	require.ErrorAs(t, err, &refused)
}
