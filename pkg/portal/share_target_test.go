package portal

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These exercise the share store's GetActiveShareForTarget (the public-link
// auto-promote lookup). They live in the portal package because the share store
// and its constants stay here; only the thread data layer moved to threads.

func shareCols() []string {
	return []string{
		"id", "asset_id", "collection_id", "prompt_id", "token", "created_by", "shared_with_user_id", "shared_with_email",
		"expires_at", "revoked", "hide_expiration", "notice_text", "access_count", "last_accessed_at", "created_at", "permission", "origin",
	}
}

func TestGetActiveShareForTargetAsset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := NewPostgresShareStore(db)

	mock.ExpectQuery("FROM portal_shares").
		WithArgs("asset_1", "u1", "u1@example.com").
		WillReturnRows(sqlmock.NewRows(shareCols()).AddRow(
			"s1", "asset_1", nil, nil, "tok", "owner@example.com", "u1", "u1@example.com",
			nil, false, false, "", 0, nil, time.Now(), string(PermissionViewer), string(OriginPublicLinkLogin),
		))

	got, err := store.GetActiveShareForTarget(context.Background(), targetTypeAsset, "asset_1", "u1", "u1@example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, OriginPublicLinkLogin, got.Origin)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetActiveShareForTargetNoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := NewPostgresShareStore(db)

	mock.ExpectQuery("FROM portal_shares").
		WithArgs("col_1", "u1", "u1@example.com").
		WillReturnError(sql.ErrNoRows)

	got, err := store.GetActiveShareForTarget(context.Background(), targetTypeCollection, "col_1", "u1", "u1@example.com")
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetActiveShareForTargetUnsupportedType(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := NewPostgresShareStore(db)

	got, err := store.GetActiveShareForTarget(context.Background(), targetTypePrompt, "p1", "u1", "u1@example.com")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestNoopShareStoreGetActiveShareForTarget(t *testing.T) {
	got, err := (&noopShareStore{}).GetActiveShareForTarget(context.Background(), targetTypeAsset, "a", "u", "e")
	require.NoError(t, err)
	assert.Nil(t, got)
}
