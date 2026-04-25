package enrichment

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRule() Rule {
	return Rule{
		ConnectionName: "crm",
		ToolName:       "crm__get_contact",
		WhenPredicate:  Predicate{Kind: PredicateResponseContains, Paths: []string{"$.email"}},
		EnrichAction: Action{
			Source:    SourceTrino,
			Operation: "query",
			Parameters: map[string]any{
				"sql_template": "SELECT * FROM warehouse.mart.customers WHERE email = :email",
				"bindings":     map[string]any{"email": "$.response.email"},
			},
		},
		MergeStrategy: Merge{Kind: MergePath, Path: "warehouse_signals"},
		Description:   "attach customer 360 signals",
		Enabled:       true,
		CreatedBy:     "admin@example.com",
	}
}

func rowColumns() []string {
	return []string{
		"id", "connection_name", "tool_name", "when_predicate",
		"enrich_action", "merge_strategy", "description", "enabled",
		"created_by", "created_at", "updated_at",
	}
}

func ruleToRow(r Rule) (whenJSON, actionJSON, mergeJSON []byte) {
	whenJSON, _ = json.Marshal(r.WhenPredicate)
	actionJSON, _ = json.Marshal(r.EnrichAction)
	mergeJSON, _ = json.Marshal(r.MergeStrategy)
	return whenJSON, actionJSON, mergeJSON
}

func TestCreate_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	r := newTestRule()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gateway_enrichment_rules")).
		WithArgs(
			sqlmock.AnyArg(), // generated id
			r.ConnectionName, r.ToolName,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			r.Description, r.Enabled, r.CreatedBy,
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	got, err := store.Create(context.Background(), r)
	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)
	assert.False(t, got.UpdatedAt.IsZero())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_PreservesProvidedID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	r := newTestRule()
	r.ID = "fixed-id"

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gateway_enrichment_rules")).
		WithArgs("fixed-id",
			r.ConnectionName, r.ToolName,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			r.Description, r.Enabled, r.CreatedBy,
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	got, err := NewPostgresStore(db).Create(context.Background(), r)
	require.NoError(t, err)
	assert.Equal(t, "fixed-id", got.ID)
}

func TestCreate_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gateway_enrichment_rules")).
		WillReturnError(errors.New("db down"))

	_, err = NewPostgresStore(db).Create(context.Background(), newTestRule())
	assert.Error(t, err)
}

func TestGet_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	r := newTestRule()
	r.ID = "abc"
	r.CreatedAt = time.Now().UTC()
	r.UpdatedAt = r.CreatedAt
	whenJSON, actionJSON, mergeJSON := ruleToRow(r)

	mock.ExpectQuery(regexp.QuoteMeta("FROM gateway_enrichment_rules WHERE id =")).
		WithArgs("abc").
		WillReturnRows(sqlmock.NewRows(rowColumns()).AddRow(
			r.ID, r.ConnectionName, r.ToolName, whenJSON, actionJSON, mergeJSON,
			r.Description, r.Enabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt,
		))

	got, err := NewPostgresStore(db).Get(context.Background(), "abc")
	require.NoError(t, err)
	assert.Equal(t, "abc", got.ID)
	assert.Equal(t, r.ConnectionName, got.ConnectionName)
	assert.Equal(t, r.WhenPredicate.Kind, got.WhenPredicate.Kind)
}

func TestGet_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta("FROM gateway_enrichment_rules WHERE id =")).
		WithArgs("missing").
		WillReturnRows(sqlmock.NewRows(rowColumns()))

	_, err = NewPostgresStore(db).Get(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrRuleNotFound)
}

func TestList_FiltersAndOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	r := newTestRule()
	r.ID = "1"
	r.CreatedAt = time.Now().UTC()
	r.UpdatedAt = r.CreatedAt
	whenJSON, actionJSON, mergeJSON := ruleToRow(r)

	mock.ExpectQuery(regexp.QuoteMeta("FROM gateway_enrichment_rules WHERE 1=1")).
		WithArgs("crm", "crm__get_contact").
		WillReturnRows(sqlmock.NewRows(rowColumns()).AddRow(
			r.ID, r.ConnectionName, r.ToolName, whenJSON, actionJSON, mergeJSON,
			r.Description, r.Enabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt,
		))

	got, err := NewPostgresStore(db).List(context.Background(), "crm", "crm__get_contact", true)
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestList_Empty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta("FROM gateway_enrichment_rules WHERE 1=1")).
		WillReturnRows(sqlmock.NewRows(rowColumns()))

	got, err := NewPostgresStore(db).List(context.Background(), "", "", false)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestUpdate_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	r := newTestRule()
	r.ID = "abc"

	mock.ExpectExec(regexp.QuoteMeta("UPDATE gateway_enrichment_rules")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	got, err := NewPostgresStore(db).Update(context.Background(), r)
	require.NoError(t, err)
	assert.Equal(t, "abc", got.ID)
	assert.False(t, got.UpdatedAt.IsZero())
}

func TestUpdate_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	r := newTestRule()
	r.ID = "missing"

	mock.ExpectExec(regexp.QuoteMeta("UPDATE gateway_enrichment_rules")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, err = NewPostgresStore(db).Update(context.Background(), r)
	assert.ErrorIs(t, err, ErrRuleNotFound)
}

func TestUpdate_NoID(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = NewPostgresStore(db).Update(context.Background(), newTestRule())
	assert.Error(t, err)
}

func TestDelete_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM gateway_enrichment_rules")).
		WithArgs("abc").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = NewPostgresStore(db).Delete(context.Background(), "abc")
	assert.NoError(t, err)
}

func TestDelete_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM gateway_enrichment_rules")).
		WithArgs("missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = NewPostgresStore(db).Delete(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrRuleNotFound)
}

func TestValidate(t *testing.T) {
	valid := newTestRule()
	require.NoError(t, valid.Validate())

	cases := []struct {
		name string
		mut  func(r *Rule)
	}{
		{"no connection", func(r *Rule) { r.ConnectionName = "" }},
		{"no tool", func(r *Rule) { r.ToolName = "" }},
		{"no source", func(r *Rule) { r.EnrichAction.Source = "" }},
		{"no operation", func(r *Rule) { r.EnrichAction.Operation = "" }},
		{"bad source", func(r *Rule) { r.EnrichAction.Source = "s3" }},
		{"bad predicate kind", func(r *Rule) { r.WhenPredicate.Kind = "weird" }},
		{"response_contains no paths", func(r *Rule) {
			r.WhenPredicate = Predicate{Kind: PredicateResponseContains}
		}},
		{"bad merge kind", func(r *Rule) { r.MergeStrategy.Kind = "nope" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRule()
			tc.mut(&r)
			assert.Error(t, r.Validate())
		})
	}
}

func TestList_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta("FROM gateway_enrichment_rules WHERE 1=1")).
		WillReturnError(errors.New("db down"))

	_, err = NewPostgresStore(db).List(context.Background(), "", "", false)
	assert.Error(t, err)
}

func TestUpdate_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	r := newTestRule()
	r.ID = "abc"
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gateway_enrichment_rules")).
		WillReturnError(errors.New("db down"))

	_, err = NewPostgresStore(db).Update(context.Background(), r)
	assert.Error(t, err)
}

func TestDelete_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM gateway_enrichment_rules")).
		WillReturnError(errors.New("db down"))

	err = NewPostgresStore(db).Delete(context.Background(), "any")
	assert.Error(t, err)
}

func TestScanRule_BadJSON(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	// Malformed when_predicate JSON should cause scanRule to error.
	mock.ExpectQuery(regexp.QuoteMeta("FROM gateway_enrichment_rules WHERE id =")).
		WithArgs("abc").
		WillReturnRows(sqlmock.NewRows(rowColumns()).AddRow(
			"abc", "crm", "t", []byte("not json"), []byte("{}"), []byte("{}"),
			"", true, "", now, now,
		))

	_, err = NewPostgresStore(db).Get(context.Background(), "abc")
	assert.Error(t, err)
}

func TestGenerateID(t *testing.T) {
	id1, err := GenerateID()
	require.NoError(t, err)
	id2, err := GenerateID()
	require.NoError(t, err)
	assert.Len(t, id1, 32)
	assert.Len(t, id2, 32)
	assert.NotEqual(t, id1, id2)
}
