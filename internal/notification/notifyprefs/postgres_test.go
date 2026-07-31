package notifyprefs

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func newMockPrefsStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	return NewPostgresStore(db), mock, func() { _ = db.Close() }
}

func prefsRows(mode string, shares, comments bool) *sqlmock.Rows {
	return prefsRowsWithMentions(mode, shares, comments, true)
}

func prefsRowsWithMentions(mode string, shares, comments, mentions bool) *sqlmock.Rows {
	return sqlmock.NewRows(prefsColumns).
		AddRow("a@b.io", mode, shares, comments, mentions, time.Now())
}

// prefsColumns mirrors the stored preference columns, in select order.
var prefsColumns = []string{
	"email", "mode", "shares_enabled", "comments_enabled", "mentions_enabled", "updated_at",
}

func TestPrefsStore_Get(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode, shares_enabled, comments_enabled, mentions_enabled, updated_at").
		WithArgs("a@b.io").
		WillReturnRows(prefsRows(notification.ModeDaily, true, false))

	p, err := store.Get(context.Background(), "a@b.io")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Mode != notification.ModeDaily || !p.SharesEnabled || p.CommentsEnabled {
		t.Errorf("unexpected prefs: %+v", p)
	}
}

func TestPrefsStore_Get_DefaultsWhenAbsent(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode, shares_enabled, comments_enabled, mentions_enabled, updated_at").
		WillReturnRows(sqlmock.NewRows(prefsColumns))

	p, err := store.Get(context.Background(), "new@b.io")
	if err != nil {
		t.Fatalf("Get absent: %v", err)
	}
	if p.Mode != notification.ModeImmediate || !p.SharesEnabled || !p.CommentsEnabled {
		t.Errorf("absent row must yield defaults: %+v", p)
	}
}

func TestPrefsStore_Get_Error(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode").WillReturnError(errors.New("boom"))

	if _, err := store.Get(context.Background(), "a@b.io"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPrefsStore_Set_AppliesOverDefaults(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	// No existing row: Set applies the update over the defaults.
	mock.ExpectQuery("SELECT email, mode").
		WillReturnRows(sqlmock.NewRows(prefsColumns))
	mode := notification.ModeDaily
	mock.ExpectQuery("INSERT INTO user_notification_prefs").
		WithArgs("a@b.io", notification.ModeDaily, true, true, true).
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))

	p, err := store.Set(context.Background(), "a@b.io", notification.PrefsUpdate{Mode: &mode})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if p.Mode != notification.ModeDaily || !p.SharesEnabled || !p.CommentsEnabled {
		t.Errorf("unexpected result: %+v", p)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPrefsStore_Set_PartialUpdatePreservesStored(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode").
		WillReturnRows(prefsRows(notification.ModeDaily, false, true))
	comments := false
	mock.ExpectQuery("INSERT INTO user_notification_prefs").
		WithArgs("a@b.io", notification.ModeDaily, false, false, true).
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))

	p, err := store.Set(context.Background(), "a@b.io", notification.PrefsUpdate{CommentsEnabled: &comments})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if p.Mode != notification.ModeDaily || p.SharesEnabled || p.CommentsEnabled {
		t.Errorf("partial update clobbered stored values: %+v", p)
	}
}

func TestPrefsStore_Set_InvalidMode(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode").
		WillReturnRows(prefsRows(notification.ModeImmediate, true, true))
	bad := "hourly"

	if _, err := store.Set(context.Background(), "a@b.io", notification.PrefsUpdate{Mode: &bad}); err == nil {
		t.Fatal("expected invalid-mode error")
	}
}

func TestPrefsStore_Set_UpsertError(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode").
		WillReturnRows(prefsRows(notification.ModeImmediate, true, true))
	mock.ExpectQuery("INSERT INTO user_notification_prefs").
		WillReturnError(errors.New("write failed"))

	if _, err := store.Set(context.Background(), "a@b.io", notification.PrefsUpdate{}); err == nil {
		t.Fatal("expected upsert error")
	}
}

func TestPrefsStore_Set_GetError(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode").WillReturnError(errors.New("read failed"))

	if _, err := store.Set(context.Background(), "a@b.io", notification.PrefsUpdate{}); err == nil {
		t.Fatal("expected read error")
	}
}
