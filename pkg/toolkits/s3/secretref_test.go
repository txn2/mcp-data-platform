package s3

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// TestParseMultiConfig_FillsStoredSecrets is #1806 for s3: the keys naming a
// stored secret are read as the client is built, and a connection whose
// secret cannot be read is skipped rather than built on the environment's
// credentials.
func TestParseMultiConfig_FillsStoredSecrets(t *testing.T) {
	prev := secretref.SetConnectionSource(func(_ context.Context, name, connection string) (string, error) {
		if connection == "lake" {
			return "v-" + name, nil
		}
		return "", errors.New("not allowed")
	})
	t.Cleanup(func() { secretref.SetConnectionSource(prev) })
	mc, err := ParseMultiConfig("lake", map[string]map[string]any{
		"lake":  {"access_key_id": "{{secret:id}}", "secret_access_key": "{{secret:key}}", "session_token": "{{secret:tok}}"},
		"other": {"access_key_id": "{{secret:id}}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := mc.Instances["lake"]
	if got.AccessKeyID != "v-id" || got.SecretAccessKey != "v-key" || got.SessionToken != "v-tok" {
		t.Errorf("lake = %+v", got)
	}
	if _, ok := mc.Instances["other"]; ok {
		t.Error("a connection the secret is not allowed on was built")
	}

	tk := &Toolkit{}
	err = tk.AddConnection("other", map[string]any{"access_key_id": "{{secret:id}}"})
	if err == nil || !strings.Contains(err.Error(), `connection "other" field access_key_id`) {
		t.Errorf("AddConnection: %v", err)
	}
}
