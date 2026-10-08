package resource

import (
	"context"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// TestThumbnailBacklog_SharesTheClaimsPredicate: the backlog counts what the
// claim would lease, so the owed predicate is the same text in both, each
// with its own numbering (#1897).
func TestThumbnailBacklog_SharesTheClaimsPredicate(t *testing.T) {
	claim, _ := buildThumbnailClaim(1, 0, 1)
	backlog, args := buildThumbnailBacklog(1)
	if len(args) != 5 {
		t.Fatalf("backlog binds %d values, want 5", len(args))
	}
	for _, fragment := range []string{"thumbnail_failed_at IS NULL OR thumbnail_failed_at < updated_at", "thumbnail_dark_s3_key = ''"} {
		if !strings.Contains(claim, fragment) || !strings.Contains(backlog, fragment) {
			t.Errorf("%q is not in both statements", fragment)
		}
	}
	if !strings.Contains(claim, "thumbnail_renderer < $4") || !strings.Contains(backlog, "thumbnail_renderer < $3") {
		t.Error("each statement numbers the renderer generation its own way")
	}
}

func TestThumbnailBacklog(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	query, _ := buildThumbnailBacklog(1)
	mock.ExpectQuery(regexp.QuoteMeta(query)).WillReturnRows(sqlmock.NewRows([]string{"p", "w"}).AddRow(7, 2))
	pending, waiting, err := workStore(t, db).ThumbnailBacklog(context.Background(), 1)
	if err != nil || pending != 7 || waiting != 2 {
		t.Fatalf("got %d, %d, %v", pending, waiting, err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(query)).WillReturnError(errWorkDB)
	if _, _, err := workStore(t, db).ThumbnailBacklog(context.Background(), 1); err == nil {
		t.Fatal("a failed count is an error")
	}
}
