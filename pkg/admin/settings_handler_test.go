package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakeNotificationSettings implements notification.SettingsStore.
type fakeNotificationSettings struct {
	settings *notification.SMTPSettings
	getErr   error
	setErr   error
	lastSet  *notification.SMTPSettings
	author   string
}

func (f *fakeNotificationSettings) GetSMTP(context.Context) (*notification.SMTPSettings, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.settings == nil {
		return nil, notification.ErrNotFound
	}
	clone := *f.settings
	return &clone, nil
}

func (f *fakeNotificationSettings) SetSMTP(_ context.Context, s notification.SMTPSettings, author string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.lastSet = &s
	f.author = author
	f.settings = &s
	return nil
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

func settingsTestHandler(store notification.SettingsStore, sendTest func(context.Context, string) error, mutable bool) *Handler {
	deps := Deps{NotificationSettings: store, SendTestEmail: sendTest}
	if mutable {
		deps.ConfigStore = &mockConfigStore{mode: "database"}
	}
	return NewHandler(deps, nil)
}

func TestGetSMTPSettings_Unconfigured(t *testing.T) {
	t.Parallel()
	h := settingsTestHandler(&fakeNotificationSettings{}, nil, true)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/settings/smtp", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	var got notification.SMTPSettingsView
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Port != 587 || got.TLSMode != notification.TLSModeStartTLS || got.Enabled {
		t.Errorf("unconfigured defaults wrong: %+v", got)
	}
}

func TestGetSMTPSettings_NeverReturnsPassword(t *testing.T) {
	t.Parallel()
	store := &fakeNotificationSettings{settings: &notification.SMTPSettings{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		Password: "super-secret", From: "p@example.com", TLSMode: notification.TLSModeStartTLS,
	}}
	h := settingsTestHandler(store, nil, true)

	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/settings/smtp", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	body := res.Body.String()
	if strings.Contains(body, "super-secret") || strings.Contains(body, `"password"`) {
		t.Errorf("response leaks the password: %s", body)
	}
	var got notification.SMTPSettingsView
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if !got.PasswordSet {
		t.Error("password_set must report a stored password")
	}
}

func TestGetSMTPSettings_StoreError(t *testing.T) {
	t.Parallel()
	h := settingsTestHandler(&fakeNotificationSettings{getErr: errors.New("db down")}, nil, true)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/settings/smtp", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestSetSMTPSettings(t *testing.T) {
	t.Parallel()
	store := &fakeNotificationSettings{}
	h := settingsTestHandler(store, nil, true)

	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/settings/smtp", notification.SMTPSettingsInput{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		Password: "s3cret", From: "p@example.com", TLSMode: notification.TLSModeStartTLS,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	if store.lastSet == nil || store.lastSet.Password != "s3cret" || store.lastSet.Host != "smtp.example.com" {
		t.Errorf("store received wrong settings: %+v", store.lastSet)
	}
	if strings.Contains(res.Body.String(), "s3cret") {
		t.Errorf("response echoes the password: %s", res.Body.String())
	}
}

func TestSetSMTPSettings_Validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  notification.SMTPSettingsInput
	}{
		{name: "bad tls mode", req: notification.SMTPSettingsInput{Port: 587, TLSMode: "ssl3"}},
		{name: "port too high", req: notification.SMTPSettingsInput{Port: 70000}},
		{name: "negative port", req: notification.SMTPSettingsInput{Port: -1}},
		{name: "enabled without host", req: notification.SMTPSettingsInput{Enabled: true, Port: 587}},
		{name: "enabled with bad from", req: notification.SMTPSettingsInput{Enabled: true, Host: "h", Port: 587, From: "nope"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := settingsTestHandler(&fakeNotificationSettings{}, nil, true)
			res := doJSON(t, h, http.MethodPut, "/api/v1/admin/settings/smtp", tc.req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400 (%s)", res.Code, res.Body.String())
			}
		})
	}
}

func TestSetSMTPSettings_DisableOnly(t *testing.T) {
	t.Parallel()
	store := &fakeNotificationSettings{}
	h := settingsTestHandler(store, nil, true)

	// A minimal disable call omits port/host/from and must succeed.
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/settings/smtp", notification.SMTPSettingsInput{Enabled: false})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	if store.lastSet == nil || store.lastSet.Enabled || store.lastSet.Port != 587 {
		t.Errorf("disable call stored wrong settings: %+v", store.lastSet)
	}
}

func TestSetSMTPSettings_StoreError(t *testing.T) {
	t.Parallel()
	h := settingsTestHandler(&fakeNotificationSettings{setErr: errors.New("boom")}, nil, true)
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/settings/smtp", notification.SMTPSettingsInput{
		Port: 587, TLSMode: notification.TLSModeStartTLS,
	})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestSMTPSettings_ReadOnlyMode(t *testing.T) {
	t.Parallel()
	h := settingsTestHandler(&fakeNotificationSettings{}, nil, false)

	if res := doJSON(t, h, http.MethodGet, "/api/v1/admin/settings/smtp", nil); res.Code != http.StatusOK {
		t.Errorf("GET in file mode = %d; want 200", res.Code)
	}
	if res := doJSON(t, h, http.MethodPut, "/api/v1/admin/settings/smtp", notification.SMTPSettingsInput{Port: 587}); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT in file mode = %d; want 405", res.Code)
	}
	if res := doJSON(t, h, http.MethodPost, "/api/v1/admin/settings/smtp/test", notification.TestEmailRequest{To: "a@b.io"}); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST test in file mode = %d; want 405", res.Code)
	}
}

func TestSMTPSettings_RoutesAbsentWithoutStore(t *testing.T) {
	t.Parallel()
	h := NewHandler(Deps{ConfigStore: &mockConfigStore{mode: "database"}}, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/settings/smtp", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status without store = %d; want 404", res.Code)
	}
}

func TestSendTestEmail(t *testing.T) {
	t.Parallel()
	var sentTo string
	send := func(_ context.Context, to string) error {
		sentTo = to
		return nil
	}
	h := settingsTestHandler(&fakeNotificationSettings{}, send, true)

	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/settings/smtp/test", notification.TestEmailRequest{To: "admin@example.com"})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	if sentTo != "admin@example.com" {
		t.Errorf("sent to %q", sentTo)
	}
}

func TestSendTestEmail_InvalidRecipient(t *testing.T) {
	t.Parallel()
	h := settingsTestHandler(&fakeNotificationSettings{}, func(context.Context, string) error { return nil }, true)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/settings/smtp/test", notification.TestEmailRequest{To: "not an address"})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", res.Code)
	}
}

func TestSendTestEmail_SMTPNotConfigured(t *testing.T) {
	t.Parallel()
	send := func(context.Context, string) error { return notification.ErrSMTPNotConfigured }
	h := settingsTestHandler(&fakeNotificationSettings{}, send, true)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/settings/smtp/test", notification.TestEmailRequest{To: "a@b.io"})
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409", res.Code)
	}
}

func TestSendTestEmail_DeliveryFailure(t *testing.T) {
	t.Parallel()
	send := func(context.Context, string) error { return errors.New("smtp refused") }
	h := settingsTestHandler(&fakeNotificationSettings{}, send, true)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/settings/smtp/test", notification.TestEmailRequest{To: "a@b.io"})
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502", res.Code)
	}
}

func TestSendTestEmail_Unavailable(t *testing.T) {
	t.Parallel()
	// With no SendTestEmail wired the test route is never registered.
	h := settingsTestHandler(&fakeNotificationSettings{}, nil, true)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/settings/smtp/test", notification.TestEmailRequest{To: "a@b.io"})
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", res.Code)
	}
}
