package settingsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/webhook/whalert"
)

// fakeWebhookAlert models the real section: unwritten reads as ErrNotFound.
type fakeWebhookAlert struct {
	stored *settingsjson.Stored[whalert.Settings]
	getErr error
	setErr error
}

func (f *fakeWebhookAlert) Get(context.Context) (settingsjson.Stored[whalert.Settings], error) {
	if f.getErr != nil {
		return settingsjson.Stored[whalert.Settings]{}, f.getErr
	}
	if f.stored == nil {
		return settingsjson.Stored[whalert.Settings]{}, settingsjson.ErrNotFound
	}
	return *f.stored, nil
}

func (f *fakeWebhookAlert) Set(_ context.Context, v whalert.Settings, author string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.stored = &settingsjson.Stored[whalert.Settings]{Value: v, UpdatedBy: author, UpdatedAt: time.Now()}
	return nil
}

// TestWebhookAlertSettings round-trips the thresholds: the defaults before a
// write, a write of global and per-source thresholds, and the refusals.
func TestWebhookAlertSettings(t *testing.T) {
	mux := testMux(Config{WebhookAlert: &fakeWebhookAlert{}, Channels: opsChannelStore(), Mutable: true})
	res := doJSON(t, mux, http.MethodGet, webhookAlertPath, nil)
	var v whalert.View
	if err := json.Unmarshal(res.Body.Bytes(), &v); err != nil || res.Code != http.StatusOK || v.SpoolFillPercent.Warning != 50 {
		t.Fatalf("defaults: %d %s", res.Code, res.Body.String())
	}
	res = doJSON(t, mux, http.MethodPut, webhookAlertPath, map[string]any{
		"channels":          []string{"ops"},
		"spool_age_percent": map[string]float64{"warning": 10, "critical": 20},
		"sources":           map[string]any{"esp": map[string]any{"silent_after_minutes": 30}},
	})
	if err := json.Unmarshal(res.Body.Bytes(), &v); err != nil || res.Code != http.StatusOK || v.SpoolAgePercent.Critical != 20 || *v.Sources["esp"].SilentAfterMinutes != 30 {
		t.Fatalf("write: %d %s", res.Code, res.Body.String())
	}
	for name, body := range map[string]map[string]any{
		"inverted":        {"spool_fill_percent": map[string]float64{"warning": 90, "critical": 50}},
		"unknown channel": {"channels": []string{"nope"}},
		"unknown field":   {"spool": 1},
	} {
		if res := doJSON(t, mux, http.MethodPut, webhookAlertPath, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, res.Code, res.Body.String())
		}
	}
}

func TestWebhookAlertSettingsFailures(t *testing.T) {
	down := errors.New("db down")
	unreadable := testMux(Config{WebhookAlert: &fakeWebhookAlert{getErr: down}, Mutable: true})
	if res := doJSON(t, unreadable, http.MethodGet, webhookAlertPath, nil); res.Code != http.StatusInternalServerError {
		t.Errorf("GET unreadable: %d", res.Code)
	}
	if res := doJSON(t, unreadable, http.MethodPut, webhookAlertPath, map[string]any{}); res.Code != http.StatusInternalServerError {
		t.Errorf("PUT unreadable: %d", res.Code)
	}
	unwritable := testMux(Config{WebhookAlert: &fakeWebhookAlert{setErr: down}, Mutable: true})
	if res := doJSON(t, unwritable, http.MethodPut, webhookAlertPath, map[string]any{}); res.Code != http.StatusInternalServerError {
		t.Errorf("PUT unwritable: %d", res.Code)
	}
}
