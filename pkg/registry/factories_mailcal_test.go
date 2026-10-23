package registry

import (
	"testing"
)

// TestCalDAVAndIMAPAreBuiltIn proves both kinds (#2246, #2247) are reachable
// the way an admin save reaches a kind: a factory, a schema the portal reads,
// and a parser the save is validated by.
func TestCalDAVAndIMAPAreBuiltIn(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinFactories(r)
	valid := map[string]map[string]any{
		"caldav": {"url": "https://cal.example.com/dav/", "auth_mode": "basic", "username": "u", "password": "p"},
		"imap": {
			"host": "imap.example.com", "username": "u", "password": "p", "folders": []any{"Platform"},
			"allow_senders": []any{"@vendor.example"}, "trusted_authserv_id": "mx.example.com",
		},
	}
	for kind, cfg := range valid {
		factory, ok := r.GetAggregateFactory(kind)
		if !ok {
			t.Fatalf("%s has no factory; an admin save would land nowhere", kind)
		}
		tk, err := factory("main", map[string]map[string]any{"main": cfg, "broken": {}})
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		manager, ok := tk.(interface{ HasConnection(string) bool })
		if !ok {
			t.Fatalf("%s does not manage connections", kind)
		}
		if tk.Kind() != kind || !manager.HasConnection("main") || manager.HasConnection("broken") {
			t.Errorf("%s: kind %q, main served %v, broken served %v", kind, tk.Kind(), manager.HasConnection("main"), manager.HasConnection("broken"))
		}
		if len(ConnectionConfigSchema(kind)) == 0 {
			t.Errorf("%s declares no config schema", kind)
		}
		if err := ValidateConnectionConfig(kind, cfg); err != nil {
			t.Errorf("%s: a valid config was refused: %v", kind, err)
		}
		if err := ValidateConnectionConfig(kind, map[string]any{}); err == nil {
			t.Errorf("%s: an empty config was accepted", kind)
		}
	}
}
