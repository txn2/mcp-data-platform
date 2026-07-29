package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakeNotificationSettings implements notification.SettingsStore. The full
// settings-route behavior suite lives with the implementation in
// internal/admin/settingsapi; the tests here cover the admin-side wiring only.
type fakeNotificationSettings struct {
	settings *notification.SMTPSettings
}

func (f *fakeNotificationSettings) GetSMTP(context.Context) (*notification.SMTPSettings, error) {
	if f.settings == nil {
		return nil, notification.ErrNotFound
	}
	clone := *f.settings
	return &clone, nil
}

func (f *fakeNotificationSettings) SetSMTP(_ context.Context, s notification.SMTPSettings, _ string) error {
	f.settings = &s
	return nil
}

// fakeNotificationPrefs implements notification.PrefsStore.
type fakeNotificationPrefs struct {
	modes map[string]string
}

func (f *fakeNotificationPrefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	p := notification.DefaultPrefs(email)
	if mode, ok := f.modes[email]; ok {
		p.Mode = mode
	}
	return p, nil
}

func (*fakeNotificationPrefs) Set(_ context.Context, email string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

func TestRequestAuthor(t *testing.T) {
	t.Parallel()
	base := func() *http.Request {
		return httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	}
	if got := requestAuthor(base()); got != "" {
		t.Errorf("unauthenticated author = %q; want empty", got)
	}
	withUser := func(u *User) *http.Request {
		r := base()
		return r.WithContext(context.WithValue(r.Context(), adminUserKey, u))
	}
	if got := requestAuthor(withUser(&User{Email: "a@b.io", UserID: "u1"})); got != "a@b.io" {
		t.Errorf("author = %q; want email", got)
	}
	if got := requestAuthor(withUser(&User{UserID: "u1"})); got != "u1" {
		t.Errorf("author = %q; want user ID fallback", got)
	}
}

// TestSettingsRoutesWiring proves the admin handler mounts the settingsapi
// surface with its dependencies: reads work, the recipient-status route
// follows the prefs store, and the test-send route follows its sender.
func TestSettingsRoutesWiring(t *testing.T) {
	t.Parallel()
	deps := Deps{
		NotificationSettings: &fakeNotificationSettings{},
		SendTestEmail:        func(context.Context, string) error { return nil },
		NotificationPrefs:    &fakeNotificationPrefs{modes: map[string]string{"bob@example.com": notification.ModeOff}},
		ConfigStore:          &mockConfigStore{mode: "database"},
	}
	h := NewHandler(deps, nil)

	if res := doJSON(t, h, http.MethodGet, "/api/v1/admin/settings/smtp", nil); res.Code != http.StatusOK {
		t.Errorf("GET settings = %d; want 200", res.Code)
	}
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/settings/smtp/recipient-status?to=bob%40example.com", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"opted_out":true`) {
		t.Errorf("recipient-status = %d %s; want 200 with opted_out true", res.Code, res.Body.String())
	}
	res = doJSON(t, h, http.MethodPost, "/api/v1/admin/settings/smtp/test", notification.TestEmailRequest{To: "a@b.io"})
	if res.Code != http.StatusOK {
		t.Errorf("POST test = %d; want 200 (%s)", res.Code, res.Body.String())
	}
}

// TestSettingsRoutesWiring_FileMode proves file config mode swaps the write
// routes for the shared 405 responder while reads stay available.
func TestSettingsRoutesWiring_FileMode(t *testing.T) {
	t.Parallel()
	h := NewHandler(Deps{NotificationSettings: &fakeNotificationSettings{}}, nil)

	if res := doJSON(t, h, http.MethodGet, "/api/v1/admin/settings/smtp", nil); res.Code != http.StatusOK {
		t.Errorf("GET in file mode = %d; want 200", res.Code)
	}
	if res := doJSON(t, h, http.MethodPut, "/api/v1/admin/settings/smtp", notification.SMTPSettingsInput{Port: 587}); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT in file mode = %d; want 405", res.Code)
	}
}

// TestSettingsRoutesWiring_AbsentWithoutStore proves the surface disappears
// entirely without a settings store (no database).
func TestSettingsRoutesWiring_AbsentWithoutStore(t *testing.T) {
	t.Parallel()
	h := NewHandler(Deps{ConfigStore: &mockConfigStore{mode: "database"}}, nil)
	if res := doJSON(t, h, http.MethodGet, "/api/v1/admin/settings/smtp", nil); res.Code != http.StatusNotFound {
		t.Fatalf("status without store = %d; want 404", res.Code)
	}
}
