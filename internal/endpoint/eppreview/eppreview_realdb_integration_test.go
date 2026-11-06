//go:build integration

package eppreview

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestRealDB_APreviewExpiresAndGoesWithItsScript is the preview store on a
// migrated database: a preview reads back with the identity it was opened
// with, reads as none once expired and is removed by the next create, and
// goes with its script.
func TestRealDB_APreviewExpiresAndGoesWithItsScript(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	scripts := scriptstore.New(db)
	sc := &script.Script{Name: "intake", Source: form, OwnerEmail: "jane@example.com", Enabled: true}
	require.NoError(t, scripts.Create(ctx, sc, script.Author{Email: "jane@example.com"}))

	s := NewStore(db)
	p := &Previews{Store: s, Versions: scripts}
	opened, err := p.Open(ctx, jane, sc, 0, "")
	require.NoError(t, err)
	rec, err := s.Get(ctx, opened.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"analyst"}, rec.Roles)
	assert.Equal(t, 1, rec.Version)
	owner, err := s.Owner(ctx, opened.ID)
	require.NoError(t, err)
	assert.Equal(t, "jane@example.com", owner)

	s.now = func() time.Time { return time.Now().Add(TTL + time.Minute) }
	_, err = s.Get(ctx, opened.ID)
	require.ErrorIs(t, err, ErrNotFound, "an expired preview reads as none")
	again, err := p.Open(ctx, jane, sc, 0, form)
	require.NoError(t, err)
	var n int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM form_previews WHERE id = $1`, opened.ID).Scan(&n))
	assert.Zero(t, n, "the next create removed the expired preview")

	_, err = scripts.Delete(ctx, sc.ID)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM form_previews WHERE id = $1`, again.ID).Scan(&n))
	assert.Zero(t, n, "a preview goes with its script")
}
