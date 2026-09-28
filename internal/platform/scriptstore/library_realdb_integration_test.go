//go:build integration

package scriptstore

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/libraryuse"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestRealDB_Libraries runs the library statements against the real schema
// (#1941): a library's versions read by name, the scripts loading it, the
// refusal to delete it while loaded, and its name unique across owners.
func TestRealDB_Libraries(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	lib := &script.Script{Name: "scaler", Source: "def scale(n):\n    return n * 2\n", OwnerEmail: "jane@example.com", Enabled: true}
	require.NoError(t, s.Create(ctx, lib, testAuthor))
	assert.True(t, lib.Library)
	lib.Source = "def scale(n):\n    return n * 3\n"
	require.NoError(t, s.UpdateWithVersion(ctx, lib, testAuthor))

	v1, err := s.LibrarySource(ctx, "scaler", 1)
	require.NoError(t, err)
	assert.Contains(t, v1, "n * 2", "version 1 is the code saved first")
	v2, err := s.LibrarySource(ctx, "scaler", 2)
	require.NoError(t, err)
	assert.Contains(t, v2, "n * 3")
	_, err = s.LibrarySource(ctx, "scaler", 3)
	require.ErrorIs(t, err, scriptlib.ErrNotFound)

	// Another owner's library of the same name is refused; another owner's
	// script of the same name is not.
	require.Error(t, s.Create(ctx, &script.Script{Name: "scaler", Source: "def f():\n    return 1\n", OwnerEmail: "bob@example.com", Enabled: true}, testAuthor))
	require.NoError(t, s.Create(ctx, &script.Script{Name: "scaler", Source: "def main():\n    print(1)\n", OwnerEmail: "bob@example.com", Enabled: true}, testAuthor))
	_, err = s.LibrarySource(ctx, "scaler", 1)
	require.NoError(t, err, "a load still resolves the library, not bob's script")

	user := &script.Script{Name: "weekly", Source: "load(\"lib:scaler@1\", \"scale\")\n\ndef main():\n    print(scale(1))\n", OwnerEmail: "bob@example.com", Enabled: true}
	require.NoError(t, s.Create(ctx, user, testAuthor))
	assert.Equal(t, []string{"scaler@1"}, user.Loads)
	uses, err := s.UsedBy(ctx, "scaler")
	require.NoError(t, err)
	assert.Equal(t, []libraryuse.Use{{ScriptID: user.ID, Name: "weekly", OwnerEmail: "bob@example.com", Version: 1}}, uses)
	got, err := s.GetByID(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"scaler@1"}, got.Loads)
	contract, err := s.Contract(ctx, lib.ID)
	require.NoError(t, err)
	assert.Len(t, contract.UsedBy, 1)

	_, err = s.Delete(ctx, lib.ID)
	var inUse *libraryuse.InUseError
	require.True(t, errors.As(err, &inUse), "%v", err)
	assert.Equal(t, []string{"weekly"}, inUse.Users)

	// Once the script stops loading it, the library can go.
	user.Source = "def main():\n    print(1)\n"
	require.NoError(t, s.UpdateWithVersion(ctx, user, testAuthor))
	_, err = s.Delete(ctx, lib.ID)
	require.NoError(t, err)

	libs := true
	listed, err := s.List(ctx, script.ListFilter{Library: &libs})
	require.NoError(t, err)
	assert.Empty(t, listed)
}
