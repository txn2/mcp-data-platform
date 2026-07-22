package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		// #1023: reply_to and the footer fields are optional but validated
		// when present, even on a disabled config.
		{name: "valid reply_to", in: SMTPSettingsInput{ReplyTo: "support@example.com"}},
		{name: "bad reply_to", in: SMTPSettingsInput{ReplyTo: "not an address"}, wantErr: true},
		{name: "email support contact", in: SMTPSettingsInput{SupportContact: "help@example.com"}},
		{name: "url support contact", in: SMTPSettingsInput{SupportContact: "https://help.example.com/x"}},
		{name: "bad support contact", in: SMTPSettingsInput{SupportContact: "room 4"}, wantErr: true},
		{name: "schemeless url support contact", in: SMTPSettingsInput{SupportContact: "help.example.com"}, wantErr: true},
		{name: "hostless url support contact", in: SMTPSettingsInput{SupportContact: "https://"}, wantErr: true},
		{name: "about text at cap", in: SMTPSettingsInput{AboutText: strings.Repeat("a", 500)}},
		{name: "about text over cap", in: SMTPSettingsInput{AboutText: strings.Repeat("a", 501)}, wantErr: true},
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
		ReplyTo: "support@example.com", AboutText: "About.", SupportContact: "help@example.com",
		TLSMode: TLSModeStartTLS, UpdatedBy: "a@b.io",
	}
	v := s.View()
	if !v.PasswordSet {
		t.Error("password_set must reflect a stored password")
	}
	if v.Host != "h" || v.Username != "u" || v.UpdatedBy != "a@b.io" {
		t.Errorf("view mapping wrong: %+v", v)
	}
	if v.ReplyTo != "support@example.com" || v.AboutText != "About." || v.SupportContact != "help@example.com" {
		t.Errorf("view must carry the reply-to and footer fields: %+v", v)
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
		ReplyTo: "support@example.com", AboutText: "About.", SupportContact: "help@example.com",
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
	if s.ReplyTo != "support@example.com" || s.AboutText != "About." || s.SupportContact != "help@example.com" {
		t.Errorf("mapping must carry the reply-to and footer fields: %+v", s)
	}
}
