//go:build integration

package scriptstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestRealDB_AVersionKeepsTheSubjectItWasSavedAs is #2450 against the migrated
// schema: the subject the saving session authenticated as reads back on the
// version, and each version keeps its own author's.
func TestRealDB_AVersionKeepsTheSubjectItWasSavedAs(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("ingest", "admin@example.com")
	require.NoError(t, s.Create(ctx, sc, script.Author{Email: "admin@example.com", Roles: []string{"admin"}, Subject: "apikey:admin"}))
	first, err := s.GetVersion(ctx, sc.ID, sc.Version)
	require.NoError(t, err)
	assert.Equal(t, "apikey:admin", first.AuthorSubject)

	live, err := s.GetByID(ctx, sc.ID)
	require.NoError(t, err)
	live.Source = "def main():\n    return 2\n"
	require.NoError(t, s.UpdateWithVersion(ctx, live, script.Author{Email: "admin@example.com", Roles: []string{"admin"}, Subject: "oidc-sub"}))
	second, err := s.GetVersion(ctx, sc.ID, live.Version)
	require.NoError(t, err)
	assert.Equal(t, "oidc-sub", second.AuthorSubject)

	_, err = db.ExecContext(ctx, `UPDATE script_versions SET author_subject = '' WHERE script_id = $1`, sc.ID)
	require.NoError(t, err)
	old, err := s.GetVersion(ctx, sc.ID, live.Version)
	require.NoError(t, err)
	assert.Empty(t, old.AuthorSubject, "a version saved before the column reads back with none")
}
