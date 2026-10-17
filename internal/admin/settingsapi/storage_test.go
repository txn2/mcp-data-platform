package settingsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/platform/storagecap"
)

// fakeStorage models the real section: unwritten reads as ErrNotFound.
type fakeStorage struct {
	stored *settingsjson.Stored[storagecap.Settings]
	getErr error
	setErr error
}

func (f *fakeStorage) Get(context.Context) (settingsjson.Stored[storagecap.Settings], error) {
	if f.getErr != nil {
		return settingsjson.Stored[storagecap.Settings]{}, f.getErr
	}
	if f.stored == nil {
		return settingsjson.Stored[storagecap.Settings]{}, settingsjson.ErrNotFound
	}
	return *f.stored, nil
}

func (f *fakeStorage) Set(_ context.Context, v storagecap.Settings, author string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.stored = &settingsjson.Stored[storagecap.Settings]{Value: v, UpdatedBy: author, UpdatedAt: time.Now()}
	return nil
}

func storageOf(t *testing.T, body []byte) storagecap.View {
	t.Helper()
	var v storagecap.View
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestStorageSettings round-trips the storage settings: the defaults before a
// write, a write that sets a budget, the database limit and the cluster, a
// later write that keeps what it leaves out, and every refusal as a 400.
func TestStorageSettings(t *testing.T) {
	store := &fakeStorage{}
	mux := testMux(Config{Storage: store, Channels: opsChannelStore(), Mutable: true})

	res := doJSON(t, mux, http.MethodGet, storagePath, nil)
	if v := storageOf(t, res.Body.Bytes()); res.Code != http.StatusOK || v.HistoryDays != 180 || v.UsedPercent.Warning != 75 {
		t.Fatalf("defaults: %d %s", res.Code, res.Body.String())
	}
	res = doJSON(t, mux, http.MethodPut, storagePath, map[string]any{
		"database_limit_bytes": 1 << 30,
		"budgets":              map[string]int64{"portal": 1 << 40},
		"seaweedfs":            map[string]any{"master_url": "http://seaweedfs:9333"},
		"days_left":            map[string]float64{"warning": 20, "critical": 5},
		"channels":             []string{"ops"},
	})
	v := storageOf(t, res.Body.Bytes())
	if res.Code != http.StatusOK || v.Budgets["portal"] != 1<<40 || v.SeaweedFS.MasterURL != "http://seaweedfs:9333" || v.UpdatedBy != "admin@example.com" {
		t.Fatalf("write: %d %s", res.Code, res.Body.String())
	}
	res = doJSON(t, mux, http.MethodPut, storagePath, map[string]any{"history_days": 30})
	if v := storageOf(t, res.Body.Bytes()); res.Code != http.StatusOK || v.DatabaseLimitBytes != 1<<30 || v.DaysLeft.Warning != 20 || v.HistoryDays != 30 {
		t.Fatalf("a partial write did not keep the rest: %d %s", res.Code, res.Body.String())
	}
	for name, body := range map[string]map[string]any{
		"bad budget":      {"budgets": map[string]int64{"portal": 0}},
		"bad master":      {"seaweedfs": map[string]any{"master_url": "seaweedfs:9333"}},
		"inverted":        {"used_percent": map[string]float64{"warning": 95, "critical": 90}},
		"unknown field":   {"budget": 1},
		"unknown channel": {"channels": []string{"nope"}},
	} {
		if res := doJSON(t, mux, http.MethodPut, storagePath, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, res.Code, res.Body.String())
		}
	}
}

func TestStorageSettingsFailures(t *testing.T) {
	down := errors.New("db down")
	unreadable := testMux(Config{Storage: &fakeStorage{getErr: down}, Mutable: true})
	if res := doJSON(t, unreadable, http.MethodGet, storagePath, nil); res.Code != http.StatusInternalServerError {
		t.Errorf("GET unreadable: %d", res.Code)
	}
	if res := doJSON(t, unreadable, http.MethodPut, storagePath, map[string]any{}); res.Code != http.StatusInternalServerError {
		t.Errorf("PUT unreadable: %d", res.Code)
	}
	unwritable := testMux(Config{Storage: &fakeStorage{setErr: down}, Mutable: true})
	if res := doJSON(t, unwritable, http.MethodPut, storagePath, map[string]any{}); res.Code != http.StatusInternalServerError {
		t.Errorf("PUT unwritable: %d", res.Code)
	}
	if res := doJSON(t, testMux(Config{Storage: &fakeStorage{}}), http.MethodPut, storagePath, map[string]any{}); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("file config mode: %d", res.Code)
	}
}
