package settingsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
)

const reviewAlertPath = "/api/v1/admin/settings/review-queue-alert"

// fakeReviewAlert implements reviewalert.SettingsStore, modeling the real
// store's contract: an unwritten section reads as ErrNotFound, not as a
// zero-valued configuration.
type fakeReviewAlert struct {
	settings *reviewalert.Settings
	getErr   error
	setErr   error
	lastSet  *reviewalert.Settings
	author   string
}

func (f *fakeReviewAlert) Get(context.Context) (*reviewalert.Settings, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.settings == nil {
		return nil, reviewalert.ErrNotFound
	}
	clone := *f.settings
	return &clone, nil
}

func (f *fakeReviewAlert) Set(_ context.Context, s reviewalert.Settings, author string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.lastSet, f.author, f.settings = &s, author, &s
	return nil
}

func decodeView(t *testing.T, body []byte) reviewalert.SettingsView {
	t.Helper()
	var v reviewalert.SettingsView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	return v
}

func TestGetReviewAlert_Unconfigured(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{ReviewAlert: &fakeReviewAlert{}, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, reviewAlertPath, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	v := decodeView(t, res.Body.Bytes())
	if !v.Enabled || v.OldestPendingDays != reviewalert.DefaultOldestPendingDays {
		t.Errorf("unconfigured view must carry the defaults: %+v", v)
	}
	if v.Recipients == nil {
		t.Error("recipients must serialize as [] so the UI has no third state")
	}
	if !slices.Contains(v.Warnings, reviewalert.NoRecipientsWarning) {
		t.Errorf("warnings = %v; want the no-recipients warning", v.Warnings)
	}
}

func TestGetReviewAlert_Stored(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{ReviewAlert: &fakeReviewAlert{settings: &reviewalert.Settings{
		Enabled: true, PendingThreshold: 25, OldestPendingDays: 14, CooldownHours: 6,
		Recipients: []string{"ops@example.com"},
	}}, Mutable: true})

	res := doJSON(t, mux, http.MethodGet, reviewAlertPath, nil)
	v := decodeView(t, res.Body.Bytes())
	if v.PendingThreshold != 25 || v.CooldownHours != 6 {
		t.Errorf("stored values not served: %+v", v)
	}
	if len(v.Warnings) != 0 {
		t.Errorf("a deliverable configuration warns about nothing: %v", v.Warnings)
	}
}

func TestGetReviewAlert_StoreFailure(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{ReviewAlert: &fakeReviewAlert{getErr: errors.New("db down")}, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, reviewAlertPath, nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestSetReviewAlert(t *testing.T) {
	t.Parallel()
	store := &fakeReviewAlert{}
	mux := testMux(Config{ReviewAlert: store, Mutable: true})

	res := doJSON(t, mux, http.MethodPut, reviewAlertPath, map[string]any{
		"enabled":             true,
		"pending_threshold":   25,
		"oldest_pending_days": 30,
		"cooldown_hours":      12,
		"recipients":          []string{"Ops Lead <Ops@Example.com>"},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (%s)", res.Code, res.Body.String())
	}
	if store.lastSet == nil {
		t.Fatal("settings were not stored")
	}
	if got := store.lastSet.Recipients; len(got) != 1 || got[0] != "ops@example.com" {
		t.Errorf("recipients stored unnormalized: %v", got)
	}
	if store.author != "admin@example.com" {
		t.Errorf("author = %q", store.author)
	}
	// The write answers with the stored state, so the UI needs no refetch.
	if v := decodeView(t, res.Body.Bytes()); v.CooldownHours != 12 {
		t.Errorf("response view = %+v", v)
	}
}

func TestSetReviewAlert_Invalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body map[string]any
	}{
		{"bad recipient", map[string]any{"recipients": []string{"nope"}}},
		{"negative threshold", map[string]any{"pending_threshold": -1}},
		{"cooldown out of range", map[string]any{"cooldown_hours": 10_000}},
		{"unknown field", map[string]any{"pending_threshold_typo": 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := &fakeReviewAlert{}
			mux := testMux(Config{ReviewAlert: store, Mutable: true})
			res := doJSON(t, mux, http.MethodPut, reviewAlertPath, tt.body)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400", res.Code)
			}
			if store.lastSet != nil {
				t.Error("an invalid write must not reach the store")
			}
		})
	}
}

func TestSetReviewAlert_StoreFailure(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{ReviewAlert: &fakeReviewAlert{setErr: errors.New("db down")}, Mutable: true})
	res := doJSON(t, mux, http.MethodPut, reviewAlertPath, map[string]any{"enabled": true})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestReviewAlert_FileConfigModeIsReadOnly(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{ReviewAlert: &fakeReviewAlert{}, Mutable: false})

	if res := doJSON(t, mux, http.MethodGet, reviewAlertPath, nil); res.Code != http.StatusOK {
		t.Errorf("reads stay available in file mode: %d", res.Code)
	}
	res := doJSON(t, mux, http.MethodPut, reviewAlertPath, map[string]any{"enabled": true})
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d; want 405", res.Code)
	}
}

func TestReviewAlert_NoStoreLeavesRoutesUnmounted(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Settings: &fakeSettings{}, Mutable: true})
	if res := doJSON(t, mux, http.MethodGet, reviewAlertPath, nil); res.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 with no store wired", res.Code)
	}
}

// TestSMTPRoutesMountWithoutAReviewAlertStore is the converse: the two
// settings surfaces are independent, so neither store's absence may unmount
// the other's routes.
func TestSMTPRoutesMountWithoutAReviewAlertStore(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{ReviewAlert: &fakeReviewAlert{}, Mutable: true})
	if res := doJSON(t, mux, http.MethodGet, "/api/v1/admin/settings/smtp", nil); res.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 with no SMTP store wired", res.Code)
	}
}
