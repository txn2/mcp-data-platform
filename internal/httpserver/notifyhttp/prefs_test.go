package notifyhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/notification/smtp"
)

// fakePrefsHTTPStore serves the PrefsAPI tests.
type fakePrefsHTTPStore struct {
	prefs  map[string]notification.Prefs
	getErr error
	setErr error
	last   *notification.PrefsUpdate
}

func (f *fakePrefsHTTPStore) Get(_ context.Context, email string) (notification.Prefs, error) {
	if f.getErr != nil {
		return notification.Prefs{}, f.getErr
	}
	if p, ok := f.prefs[email]; ok {
		return p, nil
	}
	return notification.DefaultPrefs(email), nil
}

func (f *fakePrefsHTTPStore) Set(_ context.Context, email string, u notification.PrefsUpdate) (notification.Prefs, error) {
	if f.setErr != nil {
		return notification.Prefs{}, f.setErr
	}
	f.last = &u
	p := notification.DefaultPrefs(email)
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

func prefsTestMux(store notification.PrefsStore, email string) *http.ServeMux {
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
	if got.Mode != notification.ModeImmediate || !got.SharesEnabled || !got.CommentsEnabled {
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
	mode := notification.ModeDaily
	res := doPrefsReq(t, mux, http.MethodPut, PrefsRequest{Mode: &mode})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	if store.last == nil || store.last.Mode == nil || *store.last.Mode != notification.ModeDaily {
		t.Errorf("store did not receive mode update: %+v", store.last)
	}
	var got PrefsResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != notification.ModeDaily {
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

// fakeDeliverySettings serves the SMTP state behind delivery_available. A nil
// smtp with a nil err models the store contract for "never configured" by
// returning smtp.ErrNotFound.
type fakeDeliverySettings struct {
	smtp *smtp.Settings
	err  error
}

func (f *fakeDeliverySettings) Get(context.Context) (*smtp.Settings, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.smtp == nil {
		return nil, smtp.ErrNotFound
	}
	return f.smtp, nil
}

func (*fakeDeliverySettings) Set(context.Context, smtp.Settings, string) error { return nil }

func deliveryTestMux(settings smtp.SettingsStore) *http.ServeMux {
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
		settings smtp.SettingsStore
		want     bool
	}{
		{
			name:     "never configured",
			settings: &fakeDeliverySettings{},
			want:     false,
		},
		{
			name:     "configured but disabled",
			settings: &fakeDeliverySettings{smtp: &smtp.Settings{Enabled: false, Host: "smtp.example.com"}},
			want:     false,
		},
		{
			name:     "enabled with no host",
			settings: &fakeDeliverySettings{smtp: &smtp.Settings{Enabled: true, Host: ""}},
			want:     false,
		},
		{
			name:     "enabled with a host",
			settings: &fakeDeliverySettings{smtp: &smtp.Settings{Enabled: true, Host: "smtp.example.com"}},
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
					body = map[string]any{"mode": notification.ModeDaily}
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
	mux := deliveryTestMux(&fakeDeliverySettings{smtp: &smtp.Settings{
		Enabled: true, Host: "smtp.internal.example.com", Port: 2525,
		Username: "mailer@example.com", Password: "s3cret",
		From: "platform@example.com", FromName: "Platform", TLSMode: smtp.TLSModeStartTLS,
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
		"platform@example.com", "Platform", smtp.TLSModeStartTLS, "admin@example.com",
	} {
		if strings.Contains(res.Body.String(), secret) {
			t.Errorf("SMTP value %q leaked into the non-admin response: %s", secret, res.Body.String())
		}
	}
}
