//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2236: the api and graphql connection editors set
// oauth_token_lifetime as "Token lifetime". The editor saves through the
// admin connection route, so these criteria save through it as the editor
// does: a value saved is the value read back, an empty field stores nothing,
// and a negative value is refused with the message the editor shows.
//
// Wire forms: oauth_token_lifetime is sent as a duration string ("45m") and
// as a JSON number of seconds (900), the two forms the key admits, and left
// out for an empty field; the rest of the config is strings.

// issue2236Config is an oauth client_credentials connection of kind to the
// dev stack's Keycloak with the lifetime given; nil leaves the key out.
func issue2236Config(kind, name string, lifetime any) map[string]any {
	upstream := map[string]string{"api": "base_url", "graphql": "endpoint_url"}[kind]
	cfg := map[string]any{
		upstream: issue1759KeycloakBase(), "connection_name": name,
		"auth_mode": "oauth", "oauth_grant": "client_credentials",
		"oauth_token_url": issue1759KeycloakBase() + "/realms/mcp-platform/protocol/openid-connect/token",
		"oauth_client_id": "acc-2236", "oauth_client_secret": "acc-2236-secret",
	}
	if lifetime != nil {
		cfg["oauth_token_lifetime"] = lifetime
	}
	return cfg
}

// issue2236Save saves the connection as the editor does and returns the
// status and the decoded answer.
func issue2236Save(t *testing.T, c *client, kind, name string, cfg map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"config": cfg, "description": "Acceptance #2236."})
	if err != nil {
		t.Fatal(err)
	}
	return c.rest(http.MethodPut, "/api/v1/admin/connection-instances/"+kind+"/"+name, bytes.NewReader(raw))
}

// TestIssue2236_TheTokenLifetimeSavesAndReadsBack: on an api and a graphql
// connection, a lifetime saved as a duration and as seconds reads back as
// saved, an empty field stores nothing, and a negative value is refused
// naming the key.
func TestIssue2236_TheTokenLifetimeSavesAndReadsBack(t *testing.T) {
	c := connect(t)
	for _, kind := range []string{"api", "graphql"} {
		t.Run(kind, func(t *testing.T) {
			name := fmt.Sprintf("acc-2236-%s-%d", kind, time.Now().UnixNano())
			t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/"+kind+"/"+name, http.NoBody) })
			for _, value := range []any{"45m", float64(900)} {
				if status, out := issue2236Save(t, c, kind, name, issue2236Config(kind, name, value)); status != http.StatusOK && status != http.StatusCreated {
					t.Fatalf("saving a lifetime of %v answered %d: %v", value, status, out)
				}
				_, read := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/"+kind+"/"+name, http.NoBody)
				cfg, _ := read["config"].(map[string]any)
				if fmt.Sprint(cfg["oauth_token_lifetime"]) != fmt.Sprint(value) {
					t.Errorf("a lifetime saved as %v (%T) reads back as %v", value, value, cfg["oauth_token_lifetime"])
				}
			}

			if status, out := issue2236Save(t, c, kind, name, issue2236Config(kind, name, nil)); status != http.StatusOK && status != http.StatusCreated {
				t.Fatalf("saving with the field empty answered %d: %v", status, out)
			}
			_, read := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/"+kind+"/"+name, http.NoBody)
			if cfg, _ := read["config"].(map[string]any); cfg["oauth_token_lifetime"] != nil {
				t.Errorf("an empty field stored a lifetime: %v", cfg["oauth_token_lifetime"])
			}

			status, out := issue2236Save(t, c, kind, name, issue2236Config(kind, name, "-5m"))
			if status != http.StatusBadRequest {
				t.Fatalf("a negative lifetime answered %d: %v", status, out)
			}
			if detail, _ := out["detail"].(string); !strings.Contains(detail, "oauth_token_lifetime must not be negative") {
				t.Errorf("the refusal says %q; want it to name the key", detail)
			}
		})
	}
}
