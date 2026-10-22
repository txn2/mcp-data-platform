package extaccountwire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/extaccount"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

func cookie() browsersession.CookieConfig {
	return browsersession.CookieConfig{Name: "s", Key: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
}

// routed reports the pattern the mux serves a request with, empty when none.
func routed(mux *http.ServeMux, method, path string) string {
	_, pattern := mux.Handler(httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody))
	return pattern
}

// A deployment without a database, a signing key or a session authenticator
// has no external accounts and mounts nothing (#2092).
func TestMountNeedsADatabaseAKeyAndSessions(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	auth := browsersession.NewAuthenticator(cookie())
	for name, d := range map[string]Deps{
		"no database": {Cookie: cookie(), SessionAuth: auth},
		"no key":      {DB: db, SessionAuth: auth},
		"no sessions": {DB: db, Cookie: cookie()},
	} {
		mux := http.NewServeMux()
		assert.Nil(t, Mount(mux, d), name)
		assert.Empty(t, routed(mux, http.MethodPost, extaccountRequestPath), name)
	}
}

const extaccountRequestPath = "/portal/auth/email-link"

// With all three, the service is returned, the sign-in routes are mounted,
// and the admin routes are mounted only behind the admin API's wrap.
func TestMountRegistersTheRoutes(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	passthrough := func(h http.Handler) http.Handler { return h }

	mux := http.NewServeMux()
	svc := Mount(mux, Deps{
		DB: db, Cookie: cookie(), SessionAuth: browsersession.NewAuthenticator(cookie()),
		Flow: &browsersession.Flow{}, AdminWrap: passthrough,
		Author: func(*http.Request) string { return "admin@example.com" },
	})
	require.NotNil(t, svc)
	assert.NotEmpty(t, routed(mux, http.MethodPost, extaccountRequestPath))
	assert.NotEmpty(t, routed(mux, http.MethodGet, extaccount.ClaimPath))
	assert.NotEmpty(t, routed(mux, http.MethodGet, "/api/v1/admin/external-users"))

	bare := http.NewServeMux()
	require.NotNil(t, Mount(bare, Deps{DB: db, Cookie: cookie(), SessionAuth: browsersession.NewAuthenticator(cookie())}))
	assert.Empty(t, routed(bare, http.MethodGet, "/api/v1/admin/external-users"), "no admin API, no admin routes")
}

type fakeUsers struct {
	user.Store
	byEmail map[string]*user.User
	err     error
}

func (f fakeUsers) Get(_ context.Context, email string) (*user.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	if u, ok := f.byEmail[email]; ok {
		return u, nil
	}
	return nil, user.ErrNotFound
}

// An address the identity provider has signed in is internal; one an
// administrator added by hand, or no one, is not.
func TestKnownInternal(t *testing.T) {
	assert.Nil(t, knownInternal(nil))
	check := knownInternal(fakeUsers{byEmail: map[string]*user.User{
		"staff@example.com":   {Email: "staff@example.com", Source: user.SourceAuth},
		"invited@example.com": {Email: "invited@example.com"},
	}})
	ctx := context.Background()
	for email, want := range map[string]bool{"staff@example.com": true, "invited@example.com": false, "nobody@example.com": false} {
		got, err := check(ctx, email)
		require.NoError(t, err, email)
		assert.Equal(t, want, got, email)
	}
	_, err := knownInternal(fakeUsers{err: errors.New("down")})(ctx, "a@example.com")
	assert.Error(t, err)
}

// The settings read the stored section, and the defaults when it is absent,
// unreadable or out of range.
func TestSettingsStore(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := settingsStore{section: settingsjson.New[extaccount.Settings](db, extaccount.SettingsSection)}
	ctx := context.Background()
	cols := []string{"value", "updated_by", "updated_at"}

	mock.ExpectQuery("SELECT value").WillReturnRows(sqlmock.NewRows(cols).
		AddRow(`{"session_hours":8,"invite_hours":48,"link_minutes":10}`, "a", time.Now()))
	assert.Equal(t, extaccount.Settings{SessionHours: 8, InviteHours: 48, LinkMinutes: 10}, s.Read(ctx))
	mock.ExpectQuery("SELECT value").WillReturnRows(sqlmock.NewRows(cols).AddRow(`{"session_hours":0}`, "a", time.Now()))
	assert.Equal(t, extaccount.Defaults(), s.Read(ctx))
	mock.ExpectQuery("SELECT value").WillReturnError(errors.New("down"))
	assert.Equal(t, extaccount.Defaults(), s.Read(ctx))

	mock.ExpectExec("INSERT INTO platform_settings").WillReturnResult(sqlmock.NewResult(0, 1))
	assert.NoError(t, s.Write(ctx, extaccount.Defaults(), "admin@example.com"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Assembled from a platform with no database, the feature is off; a persona
// is read from the persona store only where there is one.
func TestMountPlatformWithoutADatabase(t *testing.T) {
	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server: platform.ServerConfig{Name: "test"}, Semantic: platform.SemanticConfig{Provider: "noop"},
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	assert.Nil(t, MountPlatform(http.NewServeMux(), p, nil, Platform{SigningKey: cookie().Key}))
	_, ok := storedPersona(p)(context.Background(), "client")
	assert.False(t, ok)
}
