package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// A graphql connection whose own configuration names stored secrets sends
// their values, read as each request is sent, and an answer that carries one
// reaches the caller redacted (#2066).
func TestConnectionConfigSecretsAreSentAndRedacted(t *testing.T) {
	values := map[string]string{"gq-token": "gq-token-value-1", "gq-tenant": "tenant-value-1"}
	prev := secretref.SetConnectionSource(func(_ context.Context, name, connection string) (string, error) {
		if connection != "gql" {
			return "", fmt.Errorf("secret %q may not be used by connection %q", name, connection)
		}
		return values[name], nil
	})
	t.Cleanup(func() { secretref.SetConnectionSource(prev) })

	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"leaked gq-token-value-1"}}}`)
	tk := newToolkit(t, u, "flat", map[string]any{
		"auth_mode":      "bearer",
		"credential":     "{{secret:gq-token}}",
		"static_headers": map[string]any{"x-tenant": "{{secret:gq-tenant}}"},
	})
	out := callQuery(t, tk, QueryInput{Connection: "gql", Query: `{ dataset(urn:"x") { urn } }`})
	headers := u.lastHeaders()
	if got := headers.Get("Authorization"); got != "Bearer gq-token-value-1" {
		t.Errorf("Authorization = %q", got)
	}
	if got := headers.Get("x-tenant"); got != "tenant-value-1" {
		t.Errorf("x-tenant = %q", got)
	}
	text, _ := json.Marshal(out)
	if strings.Contains(string(text), "gq-token-value-1") || !strings.Contains(string(text), secretref.Redaction("gq-token")) {
		t.Errorf("the answer was not redacted: %s", text)
	}
}
