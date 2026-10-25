package portalstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// A grant's permission and a link's mode, expiry and banner are written in
// place, by id (#2291).
func TestPostgresShareStoreUpdatesInPlace(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresShareStore(db)
	ctx := context.Background()

	mock.ExpectExec("UPDATE portal_shares SET permission").WithArgs("s1", "editor").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.SetPermission(ctx, "s1", portaldomain.PermissionEditor))

	exp := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	mock.ExpectExec("UPDATE portal_shares SET access_mode").
		WithArgs("s1", "public", exp, "Board only", true).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.UpdateLink(ctx, "s1", portaldomain.LinkUpdate{
		AccessMode: portaldomain.AccessModePublic, ExpiresAt: &exp, NoticeText: "Board only", HideExpiration: true,
	}))

	mock.ExpectExec("UPDATE portal_shares SET access_mode").
		WithArgs("s1", "authenticated", nil, "", false).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.UpdateLink(ctx, "s1", portaldomain.LinkUpdate{AccessMode: portaldomain.AccessModeAuthenticated}))

	down := errors.New("down")
	mock.ExpectExec("UPDATE portal_shares SET permission").WillReturnError(down)
	assert.ErrorIs(t, store.SetPermission(ctx, "s1", portaldomain.PermissionViewer), down)
	mock.ExpectExec("UPDATE portal_shares SET access_mode").WillReturnError(down)
	assert.ErrorIs(t, store.UpdateLink(ctx, "s1", portaldomain.LinkUpdate{}), down)
	assert.NoError(t, mock.ExpectationsWereMet())
}
