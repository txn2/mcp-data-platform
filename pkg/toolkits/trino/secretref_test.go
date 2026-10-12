package trino

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// installTrinoSecret answers secret "wh" for connection "warehouse" only.
func installTrinoSecret(t *testing.T) {
	t.Helper()
	prev := secretref.SetConnectionSource(func(_ context.Context, name, connection string) (string, error) {
		if name == "wh" && connection == "warehouse" {
			return "s3cret", nil
		}
		return "", errors.New("not allowed")
	})
	t.Cleanup(func() { secretref.SetConnectionSource(prev) })
}

// TestParseMultiConfig_FillsAStoredSecret is #1806: a password naming a
// stored secret is read as the connection is built, and a connection whose
// secret cannot be read is not built with an empty or literal one.
func TestParseMultiConfig_FillsAStoredSecret(t *testing.T) {
	installTrinoSecret(t)
	mc, err := ParseMultiConfig("warehouse", map[string]map[string]any{
		"warehouse": {"host": "trino.example.com", "user": "svc", "password": "{{secret:wh}}", "ssl": true},
		"other":     {"host": "trino.example.com", "user": "svc", "password": "{{secret:wh}}", "ssl": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := mc.Instances["warehouse"].Password; got != "s3cret" {
		t.Errorf("password = %q; want the stored secret", got)
	}
	if _, ok := mc.Instances["other"]; ok {
		t.Error("a connection the secret is not allowed on was built")
	}
}

func TestAddConnection_RefusesAnUnreadableSecret(t *testing.T) {
	installTrinoSecret(t)
	tk := newMultiForTest(t)
	err := tk.AddConnection("other", map[string]any{"host": "h.example.com", "password": "{{secret:wh}}", "ssl": true})
	if err == nil || !strings.Contains(err.Error(), `connection "other" field password`) {
		t.Errorf("err = %v; want the unreadable secret named", err)
	}
}

func newMultiForTest(t *testing.T) *Toolkit {
	t.Helper()
	tk, err := NewMulti(MultiConfig{DefaultConnection: "warehouse", Instances: map[string]Config{
		"warehouse": {Host: "trino.example.com", User: "u"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

func TestAddConnection_FillsAStoredSecret(t *testing.T) {
	prev := secretref.SetConnectionSource(func(context.Context, string, string) (string, error) { return "pw", nil })
	t.Cleanup(func() { secretref.SetConnectionSource(prev) })
	tk := newMultiForTest(t)
	if err := tk.AddConnection("lake", map[string]any{"host": "h.example.com", "user": "u", "password": "{{secret:wh}}", "ssl": true}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	if got := tk.manager.Config().Connections["lake"].Password; got != "pw" {
		t.Errorf("the manager holds password %q; want the stored secret", got)
	}
}
