package resource

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

// TestThumbnailWrites_ADeletedRowIsErrNoSuchResource holds #2367: a thumbnail write
// that finds no row reports ErrNoSuchResource, which the renderer reads as a file
// deleted while it was drawn, and keeps the text the store always returned.
func TestThumbnailWrites_ADeletedRowIsErrNoSuchResource(t *testing.T) {
	s, mock := folderStorePG(t)
	mock.ExpectExec("UPDATE resources").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE resources").WillReturnResult(sqlmock.NewResult(0, 0))

	err := s.SetThumbnail(context.Background(), "gone1", ThumbnailCapture{Variant: ThumbnailVariantLight, S3Key: "k", CapturedAt: time.Now()})
	assert.True(t, errors.Is(err, ErrNoSuchResource), "SetThumbnail = %v", err)
	assert.EqualError(t, err, "resource not found: gone1")

	err = s.RecordThumbnailFailure(context.Background(), "gone2", "boom", time.Now())
	assert.True(t, errors.Is(err, ErrNoSuchResource), "RecordThumbnailFailure = %v", err)
	assert.EqualError(t, err, "resource not found: gone2")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestThumbnailWrites_AFailedWriteIsNotErrNoSuchResource keeps a database failure
// distinct from a deleted row: the renderer still reports it.
func TestThumbnailWrites_AFailedWriteIsNotErrNoSuchResource(t *testing.T) {
	s, mock := folderStorePG(t)
	mock.ExpectExec("UPDATE resources").WillReturnError(errors.New("db down"))
	err := s.SetThumbnail(context.Background(), "r1", ThumbnailCapture{Variant: ThumbnailVariantLight})
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrNoSuchResource))
}
