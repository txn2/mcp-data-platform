package settingsapi

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptalert"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakeScriptRunAlert models the real store: unwritten reads as ErrNotFound.
type fakeScriptRunAlert struct {
	settings *scriptalert.Settings
	getErr   error
	setErr   error
}

func (f *fakeScriptRunAlert) Get(context.Context) (*scriptalert.Settings, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.settings == nil {
		return nil, scriptalert.ErrNotFound
	}
	clone := *f.settings
	return &clone, nil
}

func (f *fakeScriptRunAlert) Set(_ context.Context, s scriptalert.Settings, _ string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.settings = &s
	return nil
}

// opsChannelStore holds one channel, ops.
func opsChannelStore() *fakeChannels {
	return newFakeChannels(notification.Channel{
		Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: "c", Target: "T",
	})
}

// channelsOfBody reads the channels a settings response carries.
func channelsOfBody(t *testing.T, body []byte) []string {
	t.Helper()
	var v struct {
		Channels []string `json:"channels"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v.Channels
}

// TestAlertChannels_EveryWireForm sends channels present, empty and absent to
// each of the three alert routes: present stores it, empty clears it, and
// absent keeps what was stored, so a client that predates channels cannot
// clear them by saving recipients.
func TestAlertChannels_EveryWireForm(t *testing.T) {
	routes := []struct {
		name string
		path string
		cfg  func() Config
		body map[string]any
	}{
		{
			"review queue", reviewAlertPath, func() Config { return Config{ReviewAlert: &fakeReviewAlert{}} },
			map[string]any{"enabled": true, "oldest_pending_days": 30, "recipients": []string{}},
		},
		{
			"connection", connAlertPath, func() Config { return Config{ConnectionAlert: &fakeConnAlert{}} },
			map[string]any{"enabled": true, "recipients": []string{}},
		},
		{
			"scheduled run", scriptRunAlertPath, func() Config { return Config{ScriptRunAlert: &fakeScriptRunAlert{}} },
			map[string]any{},
		},
		{
			"storage", storagePath, func() Config { return Config{Storage: &fakeStorage{}} },
			map[string]any{"history_days": 90},
		},
		{
			"webhook", webhookAlertPath, func() Config { return Config{WebhookAlert: &fakeWebhookAlert{}} },
			map[string]any{"not_reporting_seconds": 60},
		},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			cfg := rt.cfg()
			cfg.Channels, cfg.Mutable = opsChannelStore(), true
			mux := testMux(cfg)
			with := func(channels any) map[string]any {
				out := maps.Clone(rt.body)
				if channels != nil {
					out["channels"] = channels
				}
				return out
			}

			res := doJSON(t, mux, http.MethodPut, rt.path, with([]string{" ops ", "ops"}))
			if res.Code != http.StatusOK || len(channelsOfBody(t, res.Body.Bytes())) != 1 {
				t.Fatalf("present: %d %s", res.Code, res.Body.String())
			}
			res = doJSON(t, mux, http.MethodPut, rt.path, with(nil))
			if got := channelsOfBody(t, res.Body.Bytes()); res.Code != http.StatusOK || len(got) != 1 || got[0] != "ops" {
				t.Fatalf("absent did not keep the stored channels: %d %s", res.Code, res.Body.String())
			}
			res = doJSON(t, mux, http.MethodPut, rt.path, with([]string{}))
			if got := channelsOfBody(t, res.Body.Bytes()); res.Code != http.StatusOK || got == nil || len(got) != 0 {
				t.Fatalf("empty did not clear the channels to []: %d %s", res.Code, res.Body.String())
			}
			res = doJSON(t, mux, http.MethodPut, rt.path, with([]string{"no-such-channel"}))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("an unknown channel was accepted: %d %s", res.Code, res.Body.String())
			}
			res = doJSON(t, mux, http.MethodGet, rt.path, nil)
			if got := channelsOfBody(t, res.Body.Bytes()); res.Code != http.StatusOK || got == nil {
				t.Fatalf("GET: %d %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestAlertChannels_FailuresAndModes(t *testing.T) {
	broken := newFakeChannels()
	broken.getErr = errors.New("db down")
	mux := testMux(Config{ScriptRunAlert: &fakeScriptRunAlert{}, Channels: broken, Mutable: true})
	if res := doJSON(t, mux, http.MethodPut, scriptRunAlertPath, map[string]any{"channels": []string{"ops"}}); res.Code != http.StatusInternalServerError {
		t.Errorf("a channel store failure: %d", res.Code)
	}
	noStore := testMux(Config{ScriptRunAlert: &fakeScriptRunAlert{}, Mutable: true})
	if res := doJSON(t, noStore, http.MethodPut, scriptRunAlertPath, map[string]any{"channels": []string{"ops"}}); res.Code != http.StatusBadRequest {
		t.Errorf("a channel named with no channel store: %d", res.Code)
	}
	if res := doJSON(t, noStore, http.MethodPut, scriptRunAlertPath, map[string]any{"channels": "ops"}); res.Code != http.StatusBadRequest {
		t.Errorf("a string where a list belongs: %d", res.Code)
	}
	many := make([]string, 11)
	for i := range many {
		many[i] = string(rune('a' + i))
	}
	if res := doJSON(t, noStore, http.MethodPut, scriptRunAlertPath, map[string]any{"channels": many}); res.Code != http.StatusBadRequest {
		t.Errorf("too many channels: %d", res.Code)
	}
	unreadable := testMux(Config{ScriptRunAlert: &fakeScriptRunAlert{getErr: errors.New("db down")}, Mutable: true})
	if res := doJSON(t, unreadable, http.MethodGet, scriptRunAlertPath, nil); res.Code != http.StatusInternalServerError {
		t.Errorf("GET with an unreadable store: %d", res.Code)
	}
	if res := doJSON(t, unreadable, http.MethodPut, scriptRunAlertPath, map[string]any{}); res.Code != http.StatusInternalServerError {
		t.Errorf("PUT absent with an unreadable store: %d", res.Code)
	}
	unwritable := testMux(Config{ScriptRunAlert: &fakeScriptRunAlert{setErr: errors.New("db down")}, Mutable: true})
	if res := doJSON(t, unwritable, http.MethodPut, scriptRunAlertPath, map[string]any{"channels": []string{}}); res.Code != http.StatusInternalServerError {
		t.Errorf("PUT with an unwritable store: %d", res.Code)
	}
	fileMode := testMux(Config{ScriptRunAlert: &fakeScriptRunAlert{}})
	if res := doJSON(t, fileMode, http.MethodPut, scriptRunAlertPath, map[string]any{}); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("file config mode: %d", res.Code)
	}
	if res := doJSON(t, testMux(Config{}), http.MethodGet, scriptRunAlertPath, nil); res.Code == http.StatusOK {
		t.Error("the route answered with no store")
	}
	reviewBroken := testMux(Config{ReviewAlert: &fakeReviewAlert{getErr: errors.New("db down")}, Mutable: true})
	if res := doJSON(t, reviewBroken, http.MethodPut, reviewAlertPath, map[string]any{"enabled": true}); res.Code != http.StatusInternalServerError {
		t.Errorf("review PUT absent with an unreadable store: %d", res.Code)
	}
	connBroken := testMux(Config{ConnectionAlert: &fakeConnAlert{getErr: errors.New("db down")}, Mutable: true})
	if res := doJSON(t, connBroken, http.MethodPut, connAlertPath, map[string]any{"enabled": true}); res.Code != http.StatusInternalServerError {
		t.Errorf("connection PUT absent with an unreadable store: %d", res.Code)
	}
}
