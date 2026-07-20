package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func newMockPrefsStore(t *testing.T) (*PostgresPrefsStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	return NewPostgresPrefsStore(db), mock, func() { _ = db.Close() }
}

func prefsRows(mode string, shares, comments bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"email", "mode", "shares_enabled", "comments_enabled", "updated_at"}).
		AddRow("a@b.io", mode, shares, comments, time.Now())
}

func TestDefaultPrefs(t *testing.T) {
	p := DefaultPrefs("a@b.io")
	if p.Mode != ModeImmediate || !p.SharesEnabled || !p.CommentsEnabled {
		t.Errorf("defaults must be immediate-on: %+v", p)
	}
	if p.Email != "a@b.io" {
		t.Errorf("email not set: %+v", p)
	}
}

func TestValidMode(t *testing.T) {
	for _, m := range []string{ModeOff, ModeImmediate, ModeDaily} {
		if !ValidMode(m) {
			t.Errorf("ValidMode(%q) = false", m)
		}
	}
	for _, m := range []string{"", "weekly", "IMMEDIATE"} {
		if ValidMode(m) {
			t.Errorf("ValidMode(%q) = true", m)
		}
	}
}

func TestPrefsStore_Get(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode, shares_enabled, comments_enabled, updated_at").
		WithArgs("a@b.io").
		WillReturnRows(prefsRows(ModeDaily, true, false))

	p, err := store.Get(context.Background(), "a@b.io")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Mode != ModeDaily || !p.SharesEnabled || p.CommentsEnabled {
		t.Errorf("unexpected prefs: %+v", p)
	}
}

func TestPrefsStore_Get_DefaultsWhenAbsent(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode, shares_enabled, comments_enabled, updated_at").
		WillReturnRows(sqlmock.NewRows([]string{"email", "mode", "shares_enabled", "comments_enabled", "updated_at"}))

	p, err := store.Get(context.Background(), "new@b.io")
	if err != nil {
		t.Fatalf("Get absent: %v", err)
	}
	if p.Mode != ModeImmediate || !p.SharesEnabled || !p.CommentsEnabled {
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
		WillReturnRows(sqlmock.NewRows([]string{"email", "mode", "shares_enabled", "comments_enabled", "updated_at"}))
	mode := ModeDaily
	mock.ExpectQuery("INSERT INTO user_notification_prefs").
		WithArgs("a@b.io", ModeDaily, true, true).
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))

	p, err := store.Set(context.Background(), "a@b.io", PrefsUpdate{Mode: &mode})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if p.Mode != ModeDaily || !p.SharesEnabled || !p.CommentsEnabled {
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
		WillReturnRows(prefsRows(ModeDaily, false, true))
	comments := false
	mock.ExpectQuery("INSERT INTO user_notification_prefs").
		WithArgs("a@b.io", ModeDaily, false, false).
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))

	p, err := store.Set(context.Background(), "a@b.io", PrefsUpdate{CommentsEnabled: &comments})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if p.Mode != ModeDaily || p.SharesEnabled || p.CommentsEnabled {
		t.Errorf("partial update clobbered stored values: %+v", p)
	}
}

func TestPrefsStore_Set_InvalidMode(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode").
		WillReturnRows(prefsRows(ModeImmediate, true, true))
	bad := "hourly"

	if _, err := store.Set(context.Background(), "a@b.io", PrefsUpdate{Mode: &bad}); err == nil {
		t.Fatal("expected invalid-mode error")
	}
}

func TestPrefsStore_Set_UpsertError(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode").
		WillReturnRows(prefsRows(ModeImmediate, true, true))
	mock.ExpectQuery("INSERT INTO user_notification_prefs").
		WillReturnError(errors.New("write failed"))

	if _, err := store.Set(context.Background(), "a@b.io", PrefsUpdate{}); err == nil {
		t.Fatal("expected upsert error")
	}
}

func TestPrefsStore_Set_GetError(t *testing.T) {
	store, mock, done := newMockPrefsStore(t)
	defer done()

	mock.ExpectQuery("SELECT email, mode").WillReturnError(errors.New("read failed"))

	if _, err := store.Set(context.Background(), "a@b.io", PrefsUpdate{}); err == nil {
		t.Fatal("expected read error")
	}
}
