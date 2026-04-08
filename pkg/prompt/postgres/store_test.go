package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

var selectColumns = []string{
	"id", "name", "display_name", "description", "content", "arguments",
	"category", "scope", "personas", "owner_email", "source", "enabled",
	"created_at", "updated_at",
}

func newTestPrompt() *prompt.Prompt {
	return &prompt.Prompt{
		Name:        "test-prompt",
		DisplayName: "Test Prompt",
		Description: "A test prompt",
		Content:     "Do something with {topic}",
		Arguments: []prompt.Argument{
			{Name: "topic", Description: "The topic", Required: true},
		},
		Category:   "workflow",
		Scope:      prompt.ScopeGlobal,
		Personas:   []string{},
		OwnerEmail: "admin@example.com",
		Source:     prompt.SourceOperator,
		Enabled:    true,
	}
}

func TestNew(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	assert.Equal(t, db, store.db)
}

func TestCreate_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	now := time.Now().UTC()

	argsJSON, err := json.Marshal(p.Arguments)
	require.NoError(t, err)

	mock.ExpectQuery("INSERT INTO prompts").WithArgs(
		p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
		p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail,
		p.Source, p.Enabled,
	).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
		AddRow("uuid-123", now, now))

	err = store.Create(context.Background(), p)
	assert.NoError(t, err)
	assert.Equal(t, "uuid-123", p.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()

	mock.ExpectQuery("INSERT INTO prompts").
		WillReturnError(errors.New("connection refused"))

	err = store.Create(context.Background(), p)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	now := time.Now().UTC()
	argsJSON := []byte(`[{"name":"topic","description":"The topic","required":true}]`)

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE name").WithArgs("test-prompt").
		WillReturnRows(sqlmock.NewRows(selectColumns).AddRow(
			"uuid-123", "test-prompt", "Test Prompt", "A test prompt",
			"Do something with {topic}", argsJSON,
			"workflow", "global", pq.Array([]string{}), "admin@example.com",
			"operator", true, now, now,
		))

	p, err := store.Get(context.Background(), "test-prompt")
	assert.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "uuid-123", p.ID)
	assert.Equal(t, "test-prompt", p.Name)
	assert.Len(t, p.Arguments, 1)
	assert.Equal(t, "topic", p.Arguments[0].Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE name").WithArgs("missing").
		WillReturnRows(sqlmock.NewRows(selectColumns))

	p, err := store.Get(context.Background(), "missing")
	assert.NoError(t, err)
	assert.Nil(t, p)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetByID_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	now := time.Now().UTC()
	argsJSON := []byte(`[]`)

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE id").WithArgs("uuid-123").
		WillReturnRows(sqlmock.NewRows(selectColumns).AddRow(
			"uuid-123", "my-prompt", "My Prompt", "desc",
			"content", argsJSON,
			"", "personal", pq.Array([]string{}), "user@example.com",
			"operator", true, now, now,
		))

	p, err := store.GetByID(context.Background(), "uuid-123")
	assert.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "my-prompt", p.Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdate_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	p.ID = "uuid-123"

	argsJSON, err := json.Marshal(p.Arguments)
	require.NoError(t, err)

	mock.ExpectExec("UPDATE prompts").WithArgs(
		p.ID, p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
		p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail,
		p.Source, p.Enabled,
	).WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Update(context.Background(), p)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdate_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	p.ID = "missing"

	mock.ExpectExec("UPDATE prompts").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.Update(context.Background(), p)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDelete_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectExec("DELETE FROM prompts WHERE name").WithArgs("test-prompt").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Delete(context.Background(), "test-prompt")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteByID_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectExec("DELETE FROM prompts WHERE id").WithArgs("uuid-123").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.DeleteByID(context.Background(), "uuid-123")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_NoFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	now := time.Now().UTC()
	argsJSON := []byte(`[]`)

	mock.ExpectQuery("SELECT .+ FROM prompts ORDER BY").
		WillReturnRows(sqlmock.NewRows(selectColumns).
			AddRow("id-1", "prompt-a", "Prompt A", "desc", "content", argsJSON,
				"", "global", pq.Array([]string{}), "", "operator", true, now, now).
			AddRow("id-2", "prompt-b", "Prompt B", "desc", "content", argsJSON,
				"", "personal", pq.Array([]string{}), "user@example.com", "operator", true, now, now))

	result, err := store.List(context.Background(), prompt.ListFilter{})
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_WithScopeFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	now := time.Now().UTC()
	argsJSON := []byte(`[]`)

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE scope = \\$1").WithArgs("global").
		WillReturnRows(sqlmock.NewRows(selectColumns).
			AddRow("id-1", "prompt-a", "Prompt A", "desc", "content", argsJSON,
				"", "global", pq.Array([]string{}), "", "operator", true, now, now))

	result, err := store.List(context.Background(), prompt.ListFilter{Scope: "global"})
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_WithPersonaFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	now := time.Now().UTC()
	argsJSON := []byte(`[]`)

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE personas && \\$1").
		WithArgs(pq.Array([]string{"analyst"})).
		WillReturnRows(sqlmock.NewRows(selectColumns).
			AddRow("id-1", "prompt-a", "Prompt A", "desc", "content", argsJSON,
				"", "persona", pq.Array([]string{"analyst"}), "", "operator", true, now, now))

	result, err := store.List(context.Background(), prompt.ListFilter{
		Personas: []string{"analyst"},
	})
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_WithSearchFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE .+ILIKE").WithArgs("%inventory%").
		WillReturnRows(sqlmock.NewRows(selectColumns))

	result, err := store.List(context.Background(), prompt.ListFilter{Search: "inventory"})
	assert.NoError(t, err)
	assert.Empty(t, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_WithEnabledFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	enabled := true

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE enabled = \\$1").WithArgs(true).
		WillReturnRows(sqlmock.NewRows(selectColumns))

	result, err := store.List(context.Background(), prompt.ListFilter{Enabled: &enabled})
	assert.NoError(t, err)
	assert.Empty(t, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCount_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectQuery("SELECT COUNT.+ FROM prompts").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	count, err := store.Count(context.Background(), prompt.ListFilter{})
	assert.NoError(t, err)
	assert.Equal(t, 5, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCount_WithFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectQuery("SELECT COUNT.+ FROM prompts WHERE scope").WithArgs("global").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	count, err := store.Count(context.Background(), prompt.ListFilter{Scope: "global"})
	assert.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildWhere_MultipleConditions(t *testing.T) {
	enabled := true
	where, args := buildWhere(prompt.ListFilter{
		Scope:      "persona",
		OwnerEmail: "user@example.com",
		Enabled:    &enabled,
	})
	assert.Contains(t, where, "scope = $1")
	assert.Contains(t, where, "owner_email = $2")
	assert.Contains(t, where, "enabled = $3")
	assert.Len(t, args, 3)
}

func TestBuildWhere_Empty(t *testing.T) {
	where, args := buildWhere(prompt.ListFilter{})
	assert.Empty(t, where)
	assert.Nil(t, args)
}
