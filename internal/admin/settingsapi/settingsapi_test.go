package settingsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/notification/smtp"
)

// fakeSettings implements smtp.SettingsStore.
type fakeSettings struct {
	settings *smtp.Settings
	getErr   error
	setErr   error
	lastSet  *smtp.Settings
	author   string
}

func (f *fakeSettings) Get(context.Context) (*smtp.Settings, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.settings == nil {
		return nil, smtp.ErrNotFound
	}
	clone := *f.settings
	return &clone, nil
}

func (f *fakeSettings) Set(_ context.Context, s smtp.Settings, author string) error {
	if f.setErr != nil {
		return f.setErr
	}
	// Model the real store's contract: an empty incoming password keeps the
	// stored one, so the admin UI can stay write-only (settings_postgres.go
	// encryptedPassword). A fake that dropped it here would report no stored
	// credential on every save that left the password field untouched.
	if s.Password == "" && f.settings != nil {
		s.Password = f.settings.Password
	}
	f.lastSet = &s
	f.author = author
	f.settings = &s
	return nil
}

// fakePrefs implements notification.PrefsStore for the recipient-status route.
type fakePrefs struct {
	modes  map[string]string
	getErr error
}

func (f *fakePrefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	if f.getErr != nil {
		return notification.Prefs{}, f.getErr
	}
	p := notification.DefaultPrefs(email)
	if mode, ok := f.modes[email]; ok {
		p.Mode = mode
	}
	return p, nil
}

func (*fakePrefs) Set(_ context.Context, email string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

// strictDecode stands in for the parent's injected strict decoder.
func strictDecode(_ http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("invalid request body")
	}
	return nil
}

// testMux mounts the routes with test defaults over cfg's stores and mode.
func testMux(cfg Config) *http.ServeMux {
	if cfg.Author == nil {
		cfg.Author = func(*http.Request) string { return "admin@example.com" }
	}
	if cfg.Decode == nil {
		cfg.Decode = strictDecode
	}
	if cfg.ReadOnly == nil {
		cfg.ReadOnly = func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "not available in file config mode")
		}
	}
	mux := http.NewServeMux()
	Register(mux, cfg)
	return mux
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rc io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rc = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, rc)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestGetSMTP_Unconfigured(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{}, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	var got smtp.SettingsView
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Port != 587 || got.TLSMode != smtp.TLSModeStartTLS || got.Enabled {
		t.Errorf("unconfigured defaults wrong: %+v", got)
	}
}

func TestGetSMTP_NeverReturnsPassword(t *testing.T) {
	t.Parallel()
	store := &fakeSettings{settings: &smtp.Settings{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		Password: "super-secret", From: "p@example.com", TLSMode: smtp.TLSModeStartTLS,
	}}
	mux := testMux(Config{Settings: store, Mutable: true})

	res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	body := res.Body.String()
	if strings.Contains(body, "super-secret") || strings.Contains(body, `"password"`) {
		t.Errorf("response leaks the password: %s", body)
	}
	var got smtp.SettingsView
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if !got.PasswordSet {
		t.Error("password_set must report a stored password")
	}
}

func TestGetSMTP_StoreError(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{getErr: errors.New("db down")}, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestSetSMTP(t *testing.T) {
	t.Parallel()
	store := &fakeSettings{}
	mux := testMux(Config{Settings: store, Mutable: true})

	res := doJSON(t, mux, http.MethodPut, "/api/v1/admin/settings/smtp", smtp.SettingsInput{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		Password: "s3cret", From: "p@example.com", TLSMode: smtp.TLSModeStartTLS,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	if store.lastSet == nil || store.lastSet.Password != "s3cret" || store.lastSet.Host != "smtp.example.com" {
		t.Errorf("store received wrong settings: %+v", store.lastSet)
	}
	if store.author != "admin@example.com" {
		t.Errorf("author = %q; want the injected request author", store.author)
	}
	if strings.Contains(res.Body.String(), "s3cret") {
		t.Errorf("response echoes the password: %s", res.Body.String())
	}
}

func TestSetSMTP_Validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  smtp.SettingsInput
	}{
		{name: "bad tls mode", req: smtp.SettingsInput{Port: 587, TLSMode: "ssl3"}},
		{name: "port too high", req: smtp.SettingsInput{Port: 70000}},
		{name: "negative port", req: smtp.SettingsInput{Port: -1}},
		{name: "enabled without host", req: smtp.SettingsInput{Enabled: true, Port: 587}},
		{name: "enabled with bad from", req: smtp.SettingsInput{Enabled: true, Host: "h", Port: 587, From: "nope"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mux := testMux(Config{Settings: &fakeSettings{}, Mutable: true})
			res := doJSON(t, mux, http.MethodPut, "/api/v1/admin/settings/smtp", tc.req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400 (%s)", res.Code, res.Body.String())
			}
		})
	}
}

// TestSetSMTP_PlaintextCredentialWarning covers #1072: tls_mode none with a
// credential authenticates over an unencrypted connection, and the save
// response is where the operator sees it.
func TestSetSMTP_PlaintextCredentialWarning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  smtp.SettingsInput
		warn bool
	}{
		{
			name: "none with password",
			req:  smtp.SettingsInput{Enabled: true, Host: "relay.internal", Port: 25, Password: "s3cret", From: "p@example.com", TLSMode: smtp.TLSModeNone},
			warn: true,
		},
		{
			name: "none with username only",
			req:  smtp.SettingsInput{Enabled: true, Host: "relay.internal", Port: 25, Username: "mailer", From: "p@example.com", TLSMode: smtp.TLSModeNone},
			warn: true,
		},
		{
			name: "none without credentials",
			req:  smtp.SettingsInput{Enabled: true, Host: "relay.internal", Port: 25, From: "p@example.com", TLSMode: smtp.TLSModeNone},
			warn: false,
		},
		{
			name: "starttls with password",
			req:  smtp.SettingsInput{Enabled: true, Host: "smtp.example.com", Port: 587, Username: "mailer", Password: "s3cret", From: "p@example.com", TLSMode: smtp.TLSModeStartTLS},
			warn: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mux := testMux(Config{Settings: &fakeSettings{}, Mutable: true})
			res := doJSON(t, mux, http.MethodPut, "/api/v1/admin/settings/smtp", tc.req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
			}
			var got smtp.SettingsView
			if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			hasWarning := slices.Contains(got.Warnings, smtp.PlaintextAuthWarning)
			if hasWarning != tc.warn {
				t.Errorf("plaintext warning present = %v; want %v (warnings: %v)", hasWarning, tc.warn, got.Warnings)
			}
		})
	}
}

// TestSetSMTP_WarningSurvivesUnchangedPassword guards the case the input
// alone cannot see: the operator switches an already-credentialed relay to
// tls_mode none and leaves the write-only password field empty, so the stored
// credential is what goes out in the clear.
func TestSetSMTP_WarningSurvivesUnchangedPassword(t *testing.T) {
	t.Parallel()
	store := &fakeSettings{settings: &smtp.Settings{
		Enabled: true, Host: "relay.internal", Port: 587, Username: "mailer",
		Password: "stored", From: "p@example.com", TLSMode: smtp.TLSModeStartTLS,
	}}
	mux := testMux(Config{Settings: store, Mutable: true})

	res := doJSON(t, mux, http.MethodPut, "/api/v1/admin/settings/smtp", smtp.SettingsInput{
		Enabled: true, Host: "relay.internal", Port: 25, Username: "mailer",
		From: "p@example.com", TLSMode: smtp.TLSModeNone,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	var got smtp.SettingsView
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.Warnings, smtp.PlaintextAuthWarning) {
		t.Errorf("warnings = %v; want the plaintext-credential warning", got.Warnings)
	}
}

func TestSetSMTP_DecodeError(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{}, Mutable: true})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/settings/smtp", strings.NewReader("{not json"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", w.Code)
	}
}

func TestSetSMTP_DisableOnly(t *testing.T) {
	t.Parallel()
	store := &fakeSettings{}
	mux := testMux(Config{Settings: store, Mutable: true})

	// A minimal disable call omits port/host/from and must succeed.
	res := doJSON(t, mux, http.MethodPut, "/api/v1/admin/settings/smtp", smtp.SettingsInput{Enabled: false})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	if store.lastSet == nil || store.lastSet.Enabled || store.lastSet.Port != 587 {
		t.Errorf("disable call stored wrong settings: %+v", store.lastSet)
	}
}

func TestSetSMTP_StoreError(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{setErr: errors.New("boom")}, Mutable: true})
	res := doJSON(t, mux, http.MethodPut, "/api/v1/admin/settings/smtp", smtp.SettingsInput{
		Port: 587, TLSMode: smtp.TLSModeStartTLS,
	})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestReadOnlyMode(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{}, Mutable: false})

	if res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp", nil); res.Code != http.StatusOK {
		t.Errorf("GET in file mode = %d; want 200", res.Code)
	}
	if res := doJSON(t, mux, http.MethodPut, "/api/v1/admin/settings/smtp", smtp.SettingsInput{Port: 587}); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT in file mode = %d; want 405", res.Code)
	}
	if res := doJSON(t, mux, http.MethodPost, "/api/v1/admin/settings/smtp/test", smtp.TestEmailRequest{To: "a@b.io"}); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST test in file mode = %d; want 405", res.Code)
	}
}

func TestRoutesAbsentWithoutStore(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: nil, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status without store = %d; want 404", res.Code)
	}
}

func TestSendTest(t *testing.T) {
	t.Parallel()
	var sentTo string
	send := func(_ context.Context, to string) error {
		sentTo = to
		return nil
	}
	mux := testMux(Config{Settings: &fakeSettings{}, SendTest: send, Mutable: true})

	res := doJSON(t, mux, http.MethodPost, "/api/v1/admin/settings/smtp/test", smtp.TestEmailRequest{To: "admin@example.com"})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	if sentTo != "admin@example.com" {
		t.Errorf("sent to %q", sentTo)
	}
}

func TestSendTest_InvalidRecipient(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{}, SendTest: func(context.Context, string) error { return nil }, Mutable: true})
	res := doJSON(t, mux, http.MethodPost, "/api/v1/admin/settings/smtp/test", smtp.TestEmailRequest{To: "not an address"})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", res.Code)
	}
}

func TestSendTest_SMTPNotConfigured(t *testing.T) {
	t.Parallel()
	send := func(context.Context, string) error { return smtp.ErrNotConfigured }
	mux := testMux(Config{Settings: &fakeSettings{}, SendTest: send, Mutable: true})
	res := doJSON(t, mux, http.MethodPost, "/api/v1/admin/settings/smtp/test", smtp.TestEmailRequest{To: "a@b.io"})
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409", res.Code)
	}
}

func TestSendTest_DeliveryFailure(t *testing.T) {
	t.Parallel()
	send := func(context.Context, string) error { return errors.New("smtp refused") }
	mux := testMux(Config{Settings: &fakeSettings{}, SendTest: send, Mutable: true})
	res := doJSON(t, mux, http.MethodPost, "/api/v1/admin/settings/smtp/test", smtp.TestEmailRequest{To: "a@b.io"})
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502", res.Code)
	}
}

// TestSendTest_FailureResponseIsInvariant is the anti-scan-oracle assertion
// (#1072): the host and port are operator-chosen and unrestricted, so a
// response that varied with the dial outcome would report reachable from
// refused from filtered for any address the server can route to.
func TestSendTest_FailureResponseIsInvariant(t *testing.T) {
	t.Parallel()
	failures := []error{
		&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")},
		&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")},
		errors.New("tls: first record does not look like a TLS handshake"),
		errors.New("550 5.7.1 relay access denied"),
	}
	var first string
	for i, failErr := range failures {
		send := func(context.Context, string) error { return failErr }
		mux := testMux(Config{Settings: &fakeSettings{}, SendTest: send, Mutable: true})
		res := doJSON(t, mux, http.MethodPost, "/api/v1/admin/settings/smtp/test", smtp.TestEmailRequest{To: "a@b.io"})
		if res.Code != http.StatusBadGateway {
			t.Fatalf("failure %d: status = %d; want 502", i, res.Code)
		}
		body := res.Body.String()
		if strings.Contains(body, "refused") || strings.Contains(body, "timeout") ||
			strings.Contains(body, "tls:") || strings.Contains(body, "5.7.1") {
			t.Errorf("failure %d: response reflects the underlying error: %s", i, body)
		}
		if i == 0 {
			first = body
			continue
		}
		if body != first {
			t.Errorf("failure %d body differs from the first:\n got %s\nwant %s", i, body, first)
		}
	}
}

func TestSendTest_Unavailable(t *testing.T) {
	t.Parallel()
	// With no SendTest wired the test route is never registered.
	mux := testMux(Config{Settings: &fakeSettings{}, Mutable: true})
	res := doJSON(t, mux, http.MethodPost, "/api/v1/admin/settings/smtp/test", smtp.TestEmailRequest{To: "a@b.io"})
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", res.Code)
	}
}

func TestRecipientStatus_OptedOut(t *testing.T) {
	t.Parallel()
	prefs := &fakePrefs{modes: map[string]string{"bob@example.com": notification.ModeOff}}
	mux := testMux(Config{Settings: &fakeSettings{}, Prefs: prefs, Mutable: true})

	// Mixed-case input must find the canonically keyed row.
	res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp/recipient-status?to=Bob%40Example.com", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	var body struct {
		To       string `json:"to"`
		OptedOut bool   `json:"opted_out"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.To != "bob@example.com" {
		t.Errorf("to = %q; want canonicalized address", body.To)
	}
	if !body.OptedOut {
		t.Error("opted_out = false; want true for a delivery-mode-off address")
	}
}

func TestRecipientStatus_DefaultNotOptedOut(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{}, Prefs: &fakePrefs{}, Mutable: true})

	res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp/recipient-status?to=new%40example.com", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"opted_out":false`) {
		t.Errorf("body = %s; want opted_out false for an address with no stored prefs", res.Body.String())
	}
}

func TestRecipientStatus_InvalidAddress(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{}, Prefs: &fakePrefs{}, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp/recipient-status?to=not-an-address", nil)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", res.Code)
	}
	if !strings.Contains(res.Header().Get("Content-Type"), "application/problem+json") {
		t.Errorf("error content type = %q; want problem+json", res.Header().Get("Content-Type"))
	}
}

func TestRecipientStatus_StoreError(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{}, Prefs: &fakePrefs{getErr: errors.New("db down")}, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp/recipient-status?to=bob%40example.com", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestRecipientStatus_RouteAbsentWithoutPrefs(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{}, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp/recipient-status?to=bob%40example.com", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status without prefs store = %d; want 404", res.Code)
	}
}
