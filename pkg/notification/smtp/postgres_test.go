package smtp

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// reverseEncryptor is a StringEncryptor test double that reverses the
// string, making encryption observable without real crypto.
type reverseEncryptor struct{}

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func (reverseEncryptor) Encrypt(plaintext string) (string, error)  { return reverse(plaintext), nil }
func (reverseEncryptor) Decrypt(ciphertext string) (string, error) { return reverse(ciphertext), nil }

// failingEncryptor always errors.
type failingEncryptor struct{}

func (failingEncryptor) Encrypt(string) (string, error) { return "", errors.New("encrypt boom") }
func (failingEncryptor) Decrypt(string) (string, error) { return "", errors.New("decrypt boom") }

func newMockSettingsStore(t *testing.T, enc StringEncryptor) (*PostgresStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	return NewPostgresStore(db, enc), mock, func() { _ = db.Close() }
}

func smtpRow(t *testing.T, s Settings, updatedBy string, updatedAt time.Time) *sqlmock.Rows {
	t.Helper()
	raw, err := json.Marshal(s) // #nosec G117 -- test fixture; passwords here are fakes
	if err != nil {
		t.Fatal(err)
	}
	return sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
		AddRow(raw, updatedBy, updatedAt)
}

func TestSettingsStore_GetSMTP(t *testing.T) {
	store, mock, done := newMockSettingsStore(t, reverseEncryptor{})
	defer done()

	now := time.Now().UTC().Truncate(time.Second)
	stored := Settings{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		Username: "mailer", Password: reverse("secret"), From: "p@example.com", TLSMode: TLSModeStartTLS,
	}
	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").
		WithArgs(SettingsSection).
		WillReturnRows(smtpRow(t, stored, "admin@example.com", now))

	got, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Password != "secret" {
		t.Errorf("password not decrypted: %q", got.Password)
	}
	if got.Host != "smtp.example.com" || got.Port != 587 || !got.Enabled {
		t.Errorf("unexpected settings: %+v", got)
	}
	if got.UpdatedBy != "admin@example.com" || !got.UpdatedAt.Equal(now) {
		t.Errorf("audit columns not mapped: %q %v", got.UpdatedBy, got.UpdatedAt)
	}
}

func TestSettingsStore_GetSMTP_NotFound(t *testing.T) {
	store, mock, done := newMockSettingsStore(t, reverseEncryptor{})
	defer done()

	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").
		WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}))

	if _, err := store.Get(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSettingsStore_GetSMTP_QueryError(t *testing.T) {
	store, mock, done := newMockSettingsStore(t, reverseEncryptor{})
	defer done()

	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").
		WillReturnError(errors.New("connection reset"))

	if _, err := store.Get(context.Background()); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestSettingsStore_GetSMTP_BadJSON(t *testing.T) {
	store, mock, done := newMockSettingsStore(t, reverseEncryptor{})
	defer done()

	rows := sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
		AddRow([]byte("{not json"), "", time.Now())
	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").
		WillReturnRows(rows)

	if _, err := store.Get(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestSettingsStore_GetSMTP_DecryptError(t *testing.T) {
	store, mock, done := newMockSettingsStore(t, failingEncryptor{})
	defer done()

	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").
		WillReturnRows(smtpRow(t, Settings{Password: "x"}, "", time.Now()))

	if _, err := store.Get(context.Background()); err == nil {
		t.Fatal("expected decrypt error")
	}
}

func TestSettingsStore_SetSMTP_StoredValueEncrypted(t *testing.T) {
	// Capture the JSON argument to prove the password is stored encrypted
	// and the stale audit fields are stripped from the document.
	store, mock, done := newMockSettingsStore(t, reverseEncryptor{})
	defer done()

	var captured []byte
	mock.ExpectExec("INSERT INTO platform_settings").
		WithArgs(SettingsSection, capture{&captured}, "a@b.io").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.Set(context.Background(),
		Settings{Enabled: true, Password: "secret", UpdatedBy: "stale"}, "a@b.io"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
	if !strings.Contains(string(captured), reverse("secret")) {
		t.Errorf("stored JSON does not contain encrypted password: %s", captured)
	}
	if strings.Contains(string(captured), `"secret"`) {
		t.Errorf("stored JSON contains plaintext password: %s", captured)
	}
	if strings.Contains(string(captured), "stale") {
		t.Errorf("stored JSON carries stale audit field: %s", captured)
	}
}

// capture is a sqlmock argument matcher that records the driver value.
type capture struct{ dst *[]byte }

func (c capture) Match(v driver.Value) bool {
	switch val := v.(type) {
	case []byte:
		*c.dst = val
	case string:
		*c.dst = []byte(val)
	default:
		return false
	}
	return true
}

func TestSettingsStore_SetSMTP_EmptyPasswordKeepsStored(t *testing.T) {
	store, mock, done := newMockSettingsStore(t, reverseEncryptor{})
	defer done()

	// Existing row holds an encrypted password.
	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").
		WillReturnRows(smtpRow(t, Settings{Password: reverse("keepme")}, "", time.Now()))

	var captured []byte
	mock.ExpectExec("INSERT INTO platform_settings").
		WithArgs(SettingsSection, capture{&captured}, "a@b.io").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.Set(context.Background(), Settings{Host: "h"}, "a@b.io"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !strings.Contains(string(captured), reverse("keepme")) {
		t.Errorf("stored JSON lost the existing password: %s", captured)
	}
}

func TestSettingsStore_SetSMTP_EmptyPasswordNoExisting(t *testing.T) {
	store, mock, done := newMockSettingsStore(t, reverseEncryptor{})
	defer done()

	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").
		WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}))
	mock.ExpectExec("INSERT INTO platform_settings").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.Set(context.Background(), Settings{Host: "h"}, "a@b.io"); err != nil {
		t.Fatalf("Set with no existing row: %v", err)
	}
}

func TestSettingsStore_SetSMTP_EncryptError(t *testing.T) {
	store, _, done := newMockSettingsStore(t, failingEncryptor{})
	defer done()

	if err := store.Set(context.Background(), Settings{Password: "x"}, "a"); err == nil {
		t.Fatal("expected encrypt error")
	}
}

func TestSettingsStore_SetSMTP_ExecError(t *testing.T) {
	store, mock, done := newMockSettingsStore(t, reverseEncryptor{})
	defer done()

	mock.ExpectExec("INSERT INTO platform_settings").
		WillReturnError(errors.New("write failed"))

	if err := store.Set(context.Background(), Settings{Password: "x"}, "a"); err == nil {
		t.Fatal("expected exec error")
	}
}
