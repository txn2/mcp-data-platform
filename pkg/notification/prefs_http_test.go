package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// fakePrefsHTTPStore serves the PrefsAPI tests.
type fakePrefsHTTPStore struct {
	prefs  map[string]Prefs
	getErr error
	setErr error
	last   *PrefsUpdate
}

func (f *fakePrefsHTTPStore) Get(_ context.Context, email string) (Prefs, error) {
	if f.getErr != nil {
		return Prefs{}, f.getErr
	}
	if p, ok := f.prefs[email]; ok {
		return p, nil
	}
	return DefaultPrefs(email), nil
}

func (f *fakePrefsHTTPStore) Set(_ context.Context, email string, u PrefsUpdate) (Prefs, error) {
	if f.setErr != nil {
		return Prefs{}, f.setErr
	}
	f.last = &u
	p := DefaultPrefs(email)
	if u.Mode != nil {
		p.Mode = *u.Mode
	}
	if u.SharesEnabled != nil {
		p.SharesEnabled = *u.SharesEnabled
	}
	if u.CommentsEnabled != nil {
		p.CommentsEnabled = *u.CommentsEnabled
	}
	return p, nil
}

func prefsTestMux(store PrefsStore, email string) *http.ServeMux {
	api := &PrefsAPI{Store: store, UserEmail: func(*http.Request) string { return email }}
	mux := http.NewServeMux()
	api.Register(mux)
	return mux
}

func doPrefsReq(t *testing.T, mux *http.ServeMux, method string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, "/api/v1/portal/notification-prefs", rd)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestPrefsAPI_Get_Defaults(t *testing.T) {
	mux := prefsTestMux(&fakePrefsHTTPStore{}, "A@B.io")
	res := doPrefsReq(t, mux, http.MethodGet, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	var got PrefsResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != ModeImmediate || !got.SharesEnabled || !got.CommentsEnabled {
		t.Errorf("defaults wrong: %+v", got)
	}
}

func TestPrefsAPI_Get_Unauthorized(t *testing.T) {
	mux := prefsTestMux(&fakePrefsHTTPStore{}, "")
	if res := doPrefsReq(t, mux, http.MethodGet, nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", res.Code)
	}
}

func TestPrefsAPI_Get_NilUserResolver(t *testing.T) {
	api := &PrefsAPI{Store: &fakePrefsHTTPStore{}}
	mux := http.NewServeMux()
	api.Register(mux)
	if res := doPrefsReq(t, mux, http.MethodGet, nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", res.Code)
	}
}

func TestPrefsAPI_Get_StoreError(t *testing.T) {
	mux := prefsTestMux(&fakePrefsHTTPStore{getErr: context.DeadlineExceeded}, "a@b.io")
	if res := doPrefsReq(t, mux, http.MethodGet, nil); res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestPrefsAPI_Put(t *testing.T) {
	store := &fakePrefsHTTPStore{}
	mux := prefsTestMux(store, "A@B.io")
	mode := ModeDaily
	res := doPrefsReq(t, mux, http.MethodPut, PrefsRequest{Mode: &mode})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	if store.last == nil || store.last.Mode == nil || *store.last.Mode != ModeDaily {
		t.Errorf("store did not receive mode update: %+v", store.last)
	}
	var got PrefsResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != ModeDaily {
		t.Errorf("response mode = %q", got.Mode)
	}
}

func TestPrefsAPI_Put_InvalidMode(t *testing.T) {
	mux := prefsTestMux(&fakePrefsHTTPStore{}, "a@b.io")
	bad := "weekly"
	if res := doPrefsReq(t, mux, http.MethodPut, PrefsRequest{Mode: &bad}); res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", res.Code)
	}
}

func TestPrefsAPI_Put_BadBody(t *testing.T) {
	mux := prefsTestMux(&fakePrefsHTTPStore{}, "a@b.io")
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/v1/portal/notification-prefs", strings.NewReader("{bad"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", w.Code)
	}
}

func TestPrefsAPI_Put_Unauthorized(t *testing.T) {
	mux := prefsTestMux(&fakePrefsHTTPStore{}, "")
	if res := doPrefsReq(t, mux, http.MethodPut, PrefsRequest{}); res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", res.Code)
	}
}

func TestPrefsAPI_Put_StoreError(t *testing.T) {
	mux := prefsTestMux(&fakePrefsHTTPStore{setErr: context.DeadlineExceeded}, "a@b.io")
	if res := doPrefsReq(t, mux, http.MethodPut, PrefsRequest{}); res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestSnippet(t *testing.T) {
	if got := Snippet("short"); got != "short" {
		t.Errorf("short message altered: %q", got)
	}
	long := strings.Repeat("x", 500)
	got := Snippet(long)
	if len([]rune(got)) != snippetLimit+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("truncation wrong: len=%d", len(got))
	}
	multibyte := strings.Repeat("e", 300) + strings.Repeat("é", 300)
	got = Snippet(multibyte)
	if !strings.HasSuffix(got, "...") || strings.ContainsRune(got, '�') {
		t.Error("multibyte truncation corrupted the string")
	}
}

func TestPortalLink(t *testing.T) {
	if got := PortalLink("https://x.io/", "/assets/a1"); got != "https://x.io/portal/assets/a1" {
		t.Errorf("PortalLink = %q", got)
	}
	if got := PortalLink("", "/assets/a1"); got != "" {
		t.Errorf("empty base must yield empty link, got %q", got)
	}
}

func TestRecipientsExcluding(t *testing.T) {
	tests := []struct {
		name       string
		actor      string
		candidates []string
		want       []string
	}{
		{
			name: "owner and author", actor: "x@b.io",
			candidates: []string{"o@b.io", "t@b.io"}, want: []string{"o@b.io", "t@b.io"},
		},
		{
			name: "actor excluded case-insensitively", actor: "o@B.io",
			candidates: []string{"O@b.io", "t@b.io"}, want: []string{"t@b.io"},
		},
		{
			name: "duplicates collapsed", actor: "x@b.io",
			candidates: []string{"same@b.io", "SAME@b.io"}, want: []string{"same@b.io"},
		},
		{
			name: "empties dropped", actor: "x@b.io",
			candidates: []string{"", ""}, want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RecipientsExcluding(tc.actor, tc.candidates...)
			if len(got) != len(tc.want) {
				t.Fatalf("recipients = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("recipients = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestSMTPSettingsInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		in      SMTPSettingsInput
		wantErr bool
	}{
		{name: "valid enabled", in: SMTPSettingsInput{Enabled: true, Host: "h", Port: 587, From: "p@example.com"}},
		{name: "disable only defaults", in: SMTPSettingsInput{Enabled: false}},
		{name: "bad tls", in: SMTPSettingsInput{TLSMode: "ssl3"}, wantErr: true},
		{name: "port too high", in: SMTPSettingsInput{Port: 70000}, wantErr: true},
		{name: "negative port", in: SMTPSettingsInput{Port: -1}, wantErr: true},
		{name: "enabled no host", in: SMTPSettingsInput{Enabled: true, Port: 587}, wantErr: true},
		{name: "enabled bad from", in: SMTPSettingsInput{Enabled: true, Host: "h", From: "nope"}, wantErr: true},
		// Port 465 speaks implicit TLS only; the other two modes open plaintext
		// and stall until the send timeout instead of failing fast.
		{
			name:    "465 with starttls",
			in:      SMTPSettingsInput{Enabled: true, Host: "h", Port: 465, From: "p@example.com", TLSMode: TLSModeStartTLS},
			wantErr: true,
		},
		{
			name:    "465 with none",
			in:      SMTPSettingsInput{Enabled: true, Host: "h", Port: 465, From: "p@example.com", TLSMode: TLSModeNone},
			wantErr: true,
		},
		{
			// TLSMode defaults to starttls, so an omitted mode on 465 is the
			// same broken pairing and must not slip through the default.
			name:    "465 with defaulted mode",
			in:      SMTPSettingsInput{Enabled: true, Host: "h", Port: 465, From: "p@example.com"},
			wantErr: true,
		},
		{
			name: "465 with implicit",
			in:   SMTPSettingsInput{Enabled: true, Host: "h", Port: 465, From: "p@example.com", TLSMode: TLSModeImplicit},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.in.Validate()
			if tc.wantErr && msg == "" {
				t.Error("expected validation message")
			}
			if !tc.wantErr && msg != "" {
				t.Errorf("unexpected validation message: %q", msg)
			}
		})
	}
}

func TestSMTPSettingsView(t *testing.T) {
	s := SMTPSettings{
		Enabled: true, Host: "h", Port: 587, Username: "u",
		Password: "secret", From: "f@example.com", FromName: "F",
		TLSMode: TLSModeStartTLS, UpdatedBy: "a@b.io",
	}
	v := s.View()
	if !v.PasswordSet {
		t.Error("password_set must reflect a stored password")
	}
	if v.Host != "h" || v.Username != "u" || v.UpdatedBy != "a@b.io" {
		t.Errorf("view mapping wrong: %+v", v)
	}
	s.Password = ""
	if s.View().PasswordSet {
		t.Error("password_set must be false without a stored password")
	}

	u := UnconfiguredSMTPView()
	if u.Port != 587 || u.TLSMode != TLSModeStartTLS || u.Enabled {
		t.Errorf("unconfigured view wrong: %+v", u)
	}
}

// TestSMTPSettingsView_PlaintextAuthWarning covers #1072: TLSModeNone with a
// credential is accepted but hazardous, and the view is what carries the
// hazard to the operator.
func TestSMTPSettingsView_PlaintextAuthWarning(t *testing.T) {
	tests := []struct {
		name     string
		settings SMTPSettings
		want     bool
	}{
		{"none with password", SMTPSettings{TLSMode: TLSModeNone, Password: "p"}, true},
		{"none with username", SMTPSettings{TLSMode: TLSModeNone, Username: "u"}, true},
		{"none without credentials", SMTPSettings{TLSMode: TLSModeNone}, false},
		{"starttls with credentials", SMTPSettings{TLSMode: TLSModeStartTLS, Username: "u", Password: "p"}, false},
		{"implicit with credentials", SMTPSettings{TLSMode: TLSModeImplicit, Username: "u", Password: "p"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warnings := tc.settings.View().Warnings
			got := slices.Contains(warnings, PlaintextAuthWarning)
			if got != tc.want {
				t.Errorf("plaintext warning = %v; want %v (warnings: %v)", got, tc.want, warnings)
			}
		})
	}
}

func TestTestEmailRequest_Validate(t *testing.T) {
	ok := TestEmailRequest{To: "a@b.io"}
	if msg := ok.Validate(); msg != "" {
		t.Errorf("valid recipient rejected: %s", msg)
	}
	bad := TestEmailRequest{To: "not an address"}
	if msg := bad.Validate(); msg == "" {
		t.Error("invalid recipient accepted")
	}
}

func TestSMTPSettingsInput_SettingsMapping(t *testing.T) {
	in := SMTPSettingsInput{
		Enabled: true, Host: "h", Username: "u", Password: "p",
		From: "f@example.com", FromName: "F",
	}
	if msg := in.Validate(); msg != "" {
		t.Fatalf("Validate: %s", msg)
	}
	s := in.Settings()
	if s.Port != 587 || s.TLSMode != TLSModeStartTLS {
		t.Errorf("defaults not applied: %+v", s)
	}
	if s.Host != "h" || s.Password != "p" || s.From != "f@example.com" {
		t.Errorf("mapping wrong: %+v", s)
	}
}

// fakeDeliverySettings serves the SMTP state behind delivery_available. A nil
// smtp with a nil err models the store contract for "never configured" by
// returning ErrNotFound.
type fakeDeliverySettings struct {
	smtp *SMTPSettings
	err  error
}

func (f *fakeDeliverySettings) GetSMTP(context.Context) (*SMTPSettings, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.smtp == nil {
		return nil, ErrNotFound
	}
	return f.smtp, nil
}

func (*fakeDeliverySettings) SetSMTP(context.Context, SMTPSettings, string) error { return nil }

func deliveryTestMux(settings SettingsStore) *http.ServeMux {
	api := &PrefsAPI{
		Store:     &fakePrefsHTTPStore{},
		Settings:  settings,
		UserEmail: func(*http.Request) string { return "a@b.io" },
	}
	mux := http.NewServeMux()
	api.Register(mux)
	return mux
}

// The preferences page must be able to say that nothing it stores can be
// delivered, without the caller needing the admin-only SMTP endpoint (#1099).
func TestPrefsAPI_DeliveryAvailable(t *testing.T) {
	tests := []struct {
		name     string
		settings SettingsStore
		want     bool
	}{
		{
			name:     "never configured",
			settings: &fakeDeliverySettings{},
			want:     false,
		},
		{
			name:     "configured but disabled",
			settings: &fakeDeliverySettings{smtp: &SMTPSettings{Enabled: false, Host: "smtp.example.com"}},
			want:     false,
		},
		{
			name:     "enabled with no host",
			settings: &fakeDeliverySettings{smtp: &SMTPSettings{Enabled: true, Host: ""}},
			want:     false,
		},
		{
			name:     "enabled with a host",
			settings: &fakeDeliverySettings{smtp: &SMTPSettings{Enabled: true, Host: "smtp.example.com"}},
			want:     true,
		},
		{
			name:     "no settings store wired",
			settings: nil,
			want:     true,
		},
		{
			name:     "read failure reports available",
			settings: &fakeDeliverySettings{err: context.DeadlineExceeded},
			want:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := deliveryTestMux(tt.settings)
			for _, method := range []string{http.MethodGet, http.MethodPut} {
				var body any
				if method == http.MethodPut {
					body = map[string]any{"mode": ModeDaily}
				}
				res := doPrefsReq(t, mux, method, body)
				if res.Code != http.StatusOK {
					t.Fatalf("%s status = %d (%s)", method, res.Code, res.Body.String())
				}
				var got PrefsResponse
				if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.DeliveryAvailable != tt.want {
					t.Errorf("%s delivery_available = %v; want %v", method, got.DeliveryAvailable, tt.want)
				}
			}
		})
	}
}

// The signal is derived, so the non-admin response must carry no SMTP detail:
// asserted on the encoded shape rather than on the struct fields, because it
// is the wire format a non-admin caller sees.
func TestPrefsAPI_ExposesNoSMTPDetail(t *testing.T) {
	mux := deliveryTestMux(&fakeDeliverySettings{smtp: &SMTPSettings{
		Enabled: true, Host: "smtp.internal.example.com", Port: 2525,
		Username: "mailer@example.com", Password: "s3cret",
		From: "platform@example.com", FromName: "Platform", TLSMode: TLSModeStartTLS,
		UpdatedBy: "admin@example.com",
	}})
	res := doPrefsReq(t, mux, http.MethodGet, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}

	var fields map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	allowed := []string{"mode", "shares_enabled", "comments_enabled", "mentions_enabled", "delivery_available"}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			t.Errorf("unexpected field %q in the non-admin preferences response", key)
		}
	}
	for _, secret := range []string{
		"smtp.internal.example.com", "2525", "mailer@example.com", "s3cret",
		"platform@example.com", "Platform", TLSModeStartTLS, "admin@example.com",
	} {
		if strings.Contains(res.Body.String(), secret) {
			t.Errorf("SMTP value %q leaked into the non-admin response: %s", secret, res.Body.String())
		}
	}
}
