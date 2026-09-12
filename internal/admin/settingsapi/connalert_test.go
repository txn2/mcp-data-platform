package settingsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/connalert"
)

// fakeConnAlert implements connalert.SettingsStore, modeling the real store's
// contract: an unwritten section reads as ErrNotFound, not as a zero-valued
// configuration.
type fakeConnAlert struct {
	settings *connalert.Settings
	getErr   error
	setErr   error
	lastSet  *connalert.Settings
	author   string
}

func (f *fakeConnAlert) Get(context.Context) (*connalert.Settings, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.settings == nil {
		return nil, connalert.ErrNotFound
	}
	clone := *f.settings
	return &clone, nil
}

func (f *fakeConnAlert) Set(_ context.Context, s connalert.Settings, author string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.lastSet, f.author, f.settings = &s, author, &s
	return nil
}

func decodeConnView(t *testing.T, body []byte) connalert.SettingsView {
	t.Helper()
	var v connalert.SettingsView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	return v
}

// TestGetConnAlert_Unconfigured is what an operator sees before touching the
// page: the alert is already on, and the warning states plainly that only the
// person who authorized a connection is told.
func TestGetConnAlert_Unconfigured(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{ConnectionAlert: &fakeConnAlert{}, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, connAlertPath, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	view := decodeConnView(t, res.Body.Bytes())
	if !view.Enabled {
		t.Error("the alert must be on before an operator configures anything")
	}
	if view.EscalateAfterHours != connalert.DefaultEscalateAfterHours {
		t.Errorf("escalate_after_hours = %d; want the default", view.EscalateAfterHours)
	}
	if len(view.Warnings) != 1 || view.Warnings[0] != connalert.NoRecipientsWarning {
		t.Errorf("warnings = %v; want the no-escalation warning", view.Warnings)
	}
}

func TestSetConnAlert(t *testing.T) {
	t.Parallel()
	store := &fakeConnAlert{}
	mux := testMux(Config{ConnectionAlert: store, Mutable: true})
	res := doJSON(t, mux, http.MethodPut, connAlertPath, map[string]any{
		"enabled":              true,
		"escalate_after_hours": 6,
		"recipients":           []string{"Ops <OPS@Example.com>", "ops@example.com"},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200: %s", res.Code, res.Body.String())
	}
	if store.lastSet == nil {
		t.Fatal("nothing was stored")
	}
	if store.author != "admin@example.com" {
		t.Errorf("author = %q", store.author)
	}
	view := decodeConnView(t, res.Body.Bytes())
	if len(view.Recipients) != 1 || view.Recipients[0] != "ops@example.com" {
		t.Errorf("recipients = %v; the response must show what was stored, normalized", view.Recipients)
	}
	if len(view.Warnings) != 0 {
		t.Errorf("warnings = %v; a configured escalation warns about nothing", view.Warnings)
	}
}

func TestSetConnAlert_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body map[string]any
	}{
		{"an out-of-range window", map[string]any{"escalate_after_hours": 100000}},
		{"an unparseable address", map[string]any{"recipients": []string{"nope"}}},
		{"an unknown field", map[string]any{"escalate_after_hourz": 6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mux := testMux(Config{ConnectionAlert: &fakeConnAlert{}, Mutable: true})
			res := doJSON(t, mux, http.MethodPut, connAlertPath, tt.body)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400: %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestConnAlert_StoreFailures(t *testing.T) {
	t.Parallel()
	t.Run("an unreadable section is 500", func(t *testing.T) {
		mux := testMux(Config{
			ConnectionAlert: &fakeConnAlert{getErr: errors.New("db down")}, Mutable: true,
		})
		res := doJSON(t, mux, http.MethodGet, connAlertPath, nil)
		if res.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d; want 500", res.Code)
		}
	})

	t.Run("a failed write is 500", func(t *testing.T) {
		mux := testMux(Config{
			ConnectionAlert: &fakeConnAlert{setErr: errors.New("db down")}, Mutable: true,
		})
		res := doJSON(t, mux, http.MethodPut, connAlertPath, map[string]any{"enabled": true})
		if res.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d; want 500", res.Code)
		}
	})
}

// TestConnAlert_FileConfigMode pins that reading the configuration still works
// where writing it cannot.
func TestConnAlert_FileConfigMode(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{ConnectionAlert: &fakeConnAlert{}, Mutable: false})
	if res := doJSON(t, mux, http.MethodGet, connAlertPath, nil); res.Code != http.StatusOK {
		t.Errorf("GET status = %d; want 200", res.Code)
	}
	res := doJSON(t, mux, http.MethodPut, connAlertPath, map[string]any{"enabled": true})
	if res.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT status = %d; want 405", res.Code)
	}
}

// TestConnAlert_Unmounted is the deployment with no database: an operator must
// not be able to name recipients for an alert nothing will ever send.
func TestConnAlert_Unmounted(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Mutable: true})
	if res := doJSON(t, mux, http.MethodGet, connAlertPath, nil); res.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 with no store", res.Code)
	}
}
