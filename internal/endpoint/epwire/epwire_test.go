package epwire

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestBuild_NothingWithoutADatabase(t *testing.T) {
	assert.Nil(t, Build(Deps{}))
}

func TestBuild_AssemblesOverOneDatabase(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	e := Build(Deps{DB: db, Key: []byte("k"), BaseURL: "https://data.example.com"})
	require.NotNil(t, e)
	assert.Same(t, e.Bindings, e.Runner.Bindings)
	assert.Same(t, e.Bindings, e.Commands.Store)
	assert.Equal(t, []byte("k"), e.Checker.Key)
	assert.Equal(t, "https://data.example.com", e.Commands.BaseURL)

	mock.ExpectQuery("FROM scripts").WillReturnError(sqlmock.ErrCancelled)
	assert.Empty(t, e.Commands.ScriptName(context.Background(), "s1"), "an unreadable script is named nothing")

	now := time.Unix(1700000000, 0)
	mock.ExpectQuery("FROM scripts").WillReturnRows(sqlmock.NewRows([]string{
		"id", "name", "display_name", "description", "category", "source_code", "params",
		"owner_email", "tags", "enabled", "status", "superseded_by", "deprecated_at", "version",
		"created_at", "updated_at", "library", "library_loads", "exclusive",
	}).AddRow("s1", "lookup", "", "", "", "def handle(req):\n    return {}\n", []byte("[]"),
		"jane@example.com", "{}", true, "active", "", nil, 1, now, now, false, "{}", false))
	assert.Equal(t, "lookup", e.Commands.ScriptName(context.Background(), "s1"))

	mock.ExpectQuery("FROM gateway_secrets").WillReturnError(sqlmock.ErrCancelled)
	_, err = e.Checker.Secret(context.Background(), "turnstile-key")
	require.Error(t, err, "the provider's key is read from the stored secrets")

	src, err := e.Runner.Libraries(context.Background(), &script.Version{ID: "v1"})
	require.NoError(t, err, "with no managed-resource reader there are no pins to read")
	assert.NotNil(t, src)
}
