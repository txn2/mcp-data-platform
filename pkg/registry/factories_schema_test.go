package registry_test

import (
	"encoding/json"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Every kind an operator can create a connection under answers with a schema,
// which is what makes "what does this connection take?" answerable through the
// API instead of by reading the deployment's configuration file (#1805).
func TestConnectionConfigSchema(t *testing.T) {
	for _, kind := range []string{"trino", "s3", "api", "graphql", "mcp"} {
		raw := registry.ConnectionConfigSchema(kind)
		if raw == nil {
			t.Errorf("kind %q has no config schema", kind)
			continue
		}
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Errorf("the %s schema is not valid JSON: %v", kind, err)
			continue
		}
		if schema.Type != "object" {
			t.Errorf("the %s schema describes a %q, not an object", kind, schema.Type)
		}
		if len(schema.Properties) == 0 {
			t.Errorf("the %s schema names no properties", kind)
		}
	}
}

// A kind with no schema answers nothing rather than an empty object, so a
// caller can tell "not described" from "takes nothing".
func TestConnectionConfigSchema_UnknownKind(t *testing.T) {
	if raw := registry.ConnectionConfigSchema("sqlite"); raw != nil {
		t.Errorf("an unknown kind answered with %s", raw)
	}
}
