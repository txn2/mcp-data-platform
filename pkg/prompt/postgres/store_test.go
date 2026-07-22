package postgres

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

var selectColumns = []string{
	"id", "name", "display_name", "description", "content", "arguments",
	"category", "scope", "personas", "owner_email", "source", "enabled",
	"tags", "status", "approved_by", "approved_at", "deprecated_at",
	"superseded_by", "review_requested", "requested_scope", "requested_personas",
	"version", "collection_id", "created_at", "updated_at",
}

// testRowTime is the fixed created_at/updated_at value used by promptRow; the
// SELECT-mocking tests do not assert on timestamps.
var testRowTime = time.Unix(1700000000, 0).UTC()

// promptRow returns a full result row in promptColumns order for a global
// prompt, so SELECT-mocking tests do not each repeat 24 values.
func promptRow(id, name, scope string, argsJSON []byte, owner string) []driver.Value {
	return []driver.Value{
		id, name, "Test Prompt", "A test prompt", "Do something with {topic}", argsJSON,
		"workflow", scope, pq.Array([]string{}), owner, "operator", true,
		pq.Array([]string{}), "approved", "", nil, nil, "",
		false, "", pq.Array([]string{}), 1, "",
		testRowTime, testRowTime,
	}
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
		Category: "workflow",
		Scope:    prompt.ScopeGlobal,
		// Slice fields are non-nil, matching what Create/Update persist: the
		// store normalizes nil slices to empty so pq.Array binds '{}' rather
		// than NULL into the NOT NULL personas/tags/requested_personas columns.
		Personas:          []string{},
		Tags:              []string{},
		RequestedPersonas: []string{},
		OwnerEmail:        "admin@example.com",
		Source:            prompt.SourceOperator,
		Enabled:           true,
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

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO prompts").WithArgs(
		p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
		p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail,
		p.Source, p.Enabled,
		pq.Array(p.Tags), prompt.StatusDraft, "", nil, nil, "",
		false, "", pq.Array(p.RequestedPersonas), nil,
	).WillReturnRows(sqlmock.NewRows([]string{"id", "version", "created_at", "updated_at"}).
		AddRow("uuid-123", 1, now, now))
	mock.ExpectExec("INSERT INTO prompt_versions").WithArgs(
		"uuid-123", 1, p.DisplayName, p.Description, p.Content, argsJSON,
		pq.Array(p.Tags), p.OwnerEmail, prompt.VersionStatusApplied, "", nil,
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.Create(context.Background(), p)
	assert.NoError(t, err)
	assert.Equal(t, "uuid-123", p.ID)
	assert.Equal(t, 1, p.Version)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_SystemPromptSkipsVersionRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	p.Source = prompt.SourceSystem
	now := time.Now().UTC()

	// System rows (config mirrors) create the prompts row only: no
	// prompt_versions INSERT is expected before the commit.
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO prompts").
		WillReturnRows(sqlmock.NewRows([]string{"id", "version", "created_at", "updated_at"}).
			AddRow("uuid-sys", 1, now, now))
	mock.ExpectCommit()

	err = store.Create(context.Background(), p)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO prompts").
		WillReturnError(errors.New("connection refused"))
	mock.ExpectRollback()

	err = store.Create(context.Background(), p)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	argsJSON := []byte(`[{"name":"topic","description":"The topic","required":true}]`)

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE name").WithArgs("test-prompt").
		WillReturnRows(sqlmock.NewRows(selectColumns).AddRow(
			promptRow("uuid-123", "test-prompt", "global", argsJSON, "admin@example.com")...,
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
	argsJSON := []byte(`[]`)

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE id").WithArgs("uuid-123").
		WillReturnRows(sqlmock.NewRows(selectColumns).AddRow(
			promptRow("uuid-123", "my-prompt", "personal", argsJSON, "user@example.com")...,
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
	p.Version = 1
	p.Status = prompt.StatusApproved

	argsJSON, err := json.Marshal(p.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, p, argsJSON)
	mock.ExpectExec("UPDATE prompts").WithArgs(
		p.ID, p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
		p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail,
		p.Source, p.Enabled,
		pq.Array(p.Tags), p.Status, p.ApprovedBy, p.ApprovedAt, p.DeprecatedAt,
		p.SupersededBy, p.ReviewRequested, p.RequestedScope, pq.Array(p.RequestedPersonas),
		indexjobs.TextHash(prompt.IndexText(p)), p.Version, nil,
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.Update(context.Background(), p)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdate_RejectsRacingGatedEdit verifies the store-level review gate: a
// plain Update carrying a content change against a row that is (as locked)
// an approved shared prompt is rejected as a conflict rather than applied.
func TestUpdate_RejectsRacingGatedEdit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	p.ID = "uuid-123"
	p.Version = 1
	p.Status = prompt.StatusApproved
	p.Content = "unreviewed new content"

	locked := *p
	locked.Content = "the approved content"
	argsJSON, err := json.Marshal(p.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, &locked, argsJSON)
	mock.ExpectRollback()

	err = store.Update(context.Background(), p)
	require.ErrorIs(t, err, prompt.ErrVersionConflict)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdate_ApprovalTransitionStampsVersion verifies the draft-to-approved
// transition copies the prompt's approval stamp onto its current version row
// within the same transaction, binding the approval to a specific snapshot.
func TestUpdate_ApprovalTransitionStampsVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	p.ID = "uuid-123"
	p.Version = 1
	p.Status = prompt.StatusApproved
	p.ApprovedBy = "admin@example.com"
	now := time.Now().UTC()
	p.ApprovedAt = &now

	locked := *p
	locked.Status = prompt.StatusDraft
	argsJSON, err := json.Marshal(p.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, &locked, argsJSON)
	mock.ExpectExec("UPDATE prompts").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE prompt_versions").WithArgs(
		p.ID, p.ApprovedBy, p.ApprovedAt,
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

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

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .+ FROM prompts WHERE id = .+ FOR UPDATE").WithArgs(p.ID).
		WillReturnRows(sqlmock.NewRows(selectColumns))
	mock.ExpectRollback()

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
	argsJSON := []byte(`[]`)

	mock.ExpectQuery("SELECT .+ FROM prompts ORDER BY").
		WillReturnRows(sqlmock.NewRows(selectColumns).
			AddRow(promptRow("id-1", "prompt-a", "global", argsJSON, "")...).
			AddRow(promptRow("id-2", "prompt-b", "personal", argsJSON, "user@example.com")...))

	result, err := store.List(context.Background(), prompt.ListFilter{})
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildWhere_SourceFilters(t *testing.T) {
	clause, args := buildWhere(prompt.ListFilter{Source: prompt.SourceSystem})
	assert.Contains(t, clause, "source = $1")
	assert.Equal(t, []any{prompt.SourceSystem}, args)

	clause, args = buildWhere(prompt.ListFilter{ExcludeSource: prompt.SourceSystem})
	assert.Contains(t, clause, "source <> $1")
	assert.Equal(t, []any{prompt.SourceSystem}, args)

	// Both, plus a following Search, keep correct placeholder numbering.
	clause, args = buildWhere(prompt.ListFilter{Source: prompt.SourceOperator, ExcludeSource: prompt.SourceSystem, Search: "x"})
	assert.Contains(t, clause, "source = $1")
	assert.Contains(t, clause, "source <> $2")
	assert.Contains(t, clause, "$3")
	assert.Equal(t, []any{prompt.SourceOperator, prompt.SourceSystem, "%x%"}, args)
}

func TestList_WithScopeFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	argsJSON := []byte(`[]`)

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE scope = \\$1").WithArgs("global").
		WillReturnRows(sqlmock.NewRows(selectColumns).
			AddRow(promptRow("id-1", "prompt-a", "global", argsJSON, "")...))

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
	argsJSON := []byte(`[]`)

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE personas && \\$1").
		WithArgs(pq.Array([]string{"analyst"})).
		WillReturnRows(sqlmock.NewRows(selectColumns).
			AddRow(promptRow("id-1", "prompt-a", "persona", argsJSON, "")...))

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

func TestBuildWhere_ReviewRequested(t *testing.T) {
	yes := true
	clause, params := buildWhere(prompt.ListFilter{ReviewRequested: &yes})
	if !strings.Contains(clause, "review_requested = $1") {
		t.Errorf("clause missing review_requested filter: %q", clause)
	}
	if len(params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(params))
	}
	if v, ok := params[0].(bool); !ok || !v {
		t.Errorf("expected param true, got %v", params[0])
	}
}
