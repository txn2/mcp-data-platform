package catalogimport

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resolveOrders(urn string) string {
	if urn == ordersURN {
		return "mcp:dataset:(trino,w,hive.sales.orders)"
	}
	return ""
}

func TestRewriteText(t *testing.T) {
	ds := resolveOrders(ordersURN)
	for in, want := range map[string]string{
		"See " + ordersURN + ".":                           "See " + ds + ".",
		"(" + ordersURN + ")":                              "(" + ds + ")",
		"Two: " + ordersURN + ", and urn:li:corpuser:ana!": "Two: " + ds + ", and urn:li:corpuser:ana!",
		"`" + ordersURN + "`":                              "`" + ds + "`",
		"no references":                                    "no references",
		"urn:li:schemaField:(" + ordersURN + ",amount) stays unmapped": "urn:li:schemaField:(" + ordersURN + ",amount) stays unmapped",
	} {
		assert.Equal(t, want, rewriteText(in, resolveOrders), in)
	}
}

func TestRewriteValueShapes(t *testing.T) {
	ds := resolveOrders(ordersURN)
	got, err := rewriteValue(valueShape, ordersURN, resolveOrders)
	require.NoError(t, err)
	assert.Equal(t, ds, got)
	got, err = rewriteValue(valueShape, "urn:li:query:q", resolveOrders)
	require.NoError(t, err)
	assert.Equal(t, "urn:li:query:q", got)

	got, err = rewriteValue(arrayShape, `["`+ordersURN+`","mcp:asset:a"]`, resolveOrders)
	require.NoError(t, err)
	assert.JSONEq(t, `["`+ds+`","mcp:asset:a"]`, got)
	unchanged := `["urn:li:query:q"]`
	got, err = rewriteValue(arrayShape, unchanged, resolveOrders)
	require.NoError(t, err)
	assert.Equal(t, unchanged, got, "an array with nothing to rewrite is left byte for byte")
	_, err = rewriteValue(arrayShape, `{"not":"an array"}`, resolveOrders)
	assert.Error(t, err)

	got, err = rewriteValue(objectArrayShape, `[{"urn":"`+ordersURN+`","column":"amount"},{"urn":"mcp:x"}]`, resolveOrders)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"urn":"`+ds+`","column":"amount"},{"urn":"mcp:x"}]`, got)
	got, err = rewriteValue(objectArrayShape, `[{"urn":"urn:li:x:y"}]`, resolveOrders)
	require.NoError(t, err)
	assert.Equal(t, `[{"urn":"urn:li:x:y"}]`, got)
	_, err = rewriteValue(objectArrayShape, `"text"`, resolveOrders)
	assert.Error(t, err)

	got, err = rewriteValue(textShape, "x "+ordersURN, resolveOrders)
	require.NoError(t, err)
	assert.Equal(t, "x "+ds, got)
}

// expectEmptyColumns answers every column's listing with no rows, from the
// one at index from on.
func expectEmptyColumns(mock sqlmock.Sqlmock, from int) {
	for _, c := range columns[from:] {
		mock.ExpectQuery(regexp.QuoteMeta(c.list)).WillReturnRows(sqlmock.NewRows([]string{"id", "value"}))
	}
}

// The rewrite updates a row whose value changed, leaves and reports one it
// cannot map, moves a page citation, removes a citation the page already
// has, and drops the index's DataHub rows.
func TestRewriterWithMock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	ds := resolveOrders(ordersURN)

	mock.ExpectQuery(regexp.QuoteMeta(columns[0].list)).WillReturnRows(sqlmock.NewRows([]string{"id", "value"}).
		AddRow("m1", `["`+ordersURN+`"]`).
		AddRow("m2", `["urn:li:query:q"]`).
		AddRow("m3", `not json`))
	mock.ExpectExec(regexp.QuoteMeta(columns[0].update)).WithArgs("m1", `["`+ds+`"]`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectEmptyColumns(mock, 1)
	mock.ExpectQuery(regexp.QuoteMeta(listPageReferences)).WillReturnRows(sqlmock.NewRows([]string{"id", "urn"}).
		AddRow("r1", ordersURN).AddRow("r2", ordersURN).AddRow("r3", "urn:li:chart:c"))
	mock.ExpectExec(regexp.QuoteMeta(updatePageReference)).WithArgs("r1", ds).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(updatePageReference)).WithArgs("r2", ds).WillReturnError(&pq.Error{Code: "23505"})
	mock.ExpectExec(regexp.QuoteMeta(deletePageReference)).WithArgs("r2").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM catalog_datasets").WillReturnResult(sqlmock.NewResult(0, 4))

	report := newReport()
	require.NoError(t, NewRewriter(db).Rewrite(context.Background(), resolveOrders, report))
	assert.Equal(t, 1, report.Rewrite.Rewritten["memory_records.entity_urns"])
	assert.Equal(t, 2, report.Rewrite.Rewritten["knowledge_page_entity_refs.entity_urn"])
	assert.Equal(t, 2, report.Rewrite.Unmapped)
	assert.Equal(t, 4, report.Rewrite.IndexRowsDropped)
	assert.Len(t, report.Skipped, 3, "two unmapped references and one unreadable value")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRewriterFailures(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]func(sqlmock.Sqlmock){
		"a listing fails": func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(regexp.QuoteMeta(columns[0].list)).WillReturnError(boom)
		},
		"an update fails": func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(regexp.QuoteMeta(columns[0].list)).WillReturnRows(sqlmock.NewRows([]string{"id", "value"}).
				AddRow("m1", `["`+ordersURN+`"]`))
			mock.ExpectExec(regexp.QuoteMeta(columns[0].update)).WillReturnError(boom)
		},
		"the page citations cannot be read": func(mock sqlmock.Sqlmock) {
			expectEmptyColumns(mock, 0)
			mock.ExpectQuery(regexp.QuoteMeta(listPageReferences)).WillReturnError(boom)
		},
		"a page citation cannot be moved": func(mock sqlmock.Sqlmock) {
			expectEmptyColumns(mock, 0)
			mock.ExpectQuery(regexp.QuoteMeta(listPageReferences)).WillReturnRows(sqlmock.NewRows([]string{"id", "urn"}).AddRow("r1", ordersURN))
			mock.ExpectExec(regexp.QuoteMeta(updatePageReference)).WillReturnError(boom)
		},
		"the index rows cannot be dropped": func(mock sqlmock.Sqlmock) {
			expectEmptyColumns(mock, 0)
			mock.ExpectQuery(regexp.QuoteMeta(listPageReferences)).WillReturnRows(sqlmock.NewRows([]string{"id", "urn"}))
			mock.ExpectExec("DELETE FROM catalog_datasets").WillReturnError(boom)
		},
		"a row cannot be scanned": func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(regexp.QuoteMeta(columns[0].list)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("m1"))
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			setup(mock)
			assert.Error(t, NewRewriter(db).Rewrite(context.Background(), resolveOrders, newReport()))
		})
	}
}
