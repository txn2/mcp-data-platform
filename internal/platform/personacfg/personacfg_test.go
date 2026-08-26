package personacfg_test

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/internal/platform/personacfg"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// A persona's API route rules had no file field at all: `api_routes:` decoded
// as an unrecognized key and never reached the persona the authorizer
// evaluates, so the rules the enforcement path already honored could not be
// written anywhere (#1479).
func TestPersonaDef_APIRoutesDecodeFromYAML(t *testing.T) {
	const doc = `
display_name: Analyst
roles: [analyst]
tools:
  allow: ["*"]
connections:
  allow: ["*"]
api_routes:
  - connection: "crm-*"
    methods: [DELETE]
    paths: ["/v1/orders/{id}"]
    action: deny
`
	var def personacfg.PersonaDef
	dec := yaml.NewDecoder(strings.NewReader(doc))
	dec.KnownFields(true)
	if err := dec.Decode(&def); err != nil {
		t.Fatalf("api_routes is not a recognized persona key: %v", err)
	}

	p := def.ToPersona("analyst", "file")
	if len(p.APIRoutes) != 1 {
		t.Fatalf("APIRoutes = %d, want 1", len(p.APIRoutes))
	}
	rule := p.APIRoutes[0]
	if rule.Connection != "crm-*" || rule.Action != persona.ActionDeny {
		t.Errorf("rule = %+v, want the crm-* deny", rule)
	}
	if len(rule.Paths) != 1 || rule.Paths[0] != "/v1/orders/{id}" {
		t.Errorf("paths = %v, want the declared path unchanged", rule.Paths)
	}
	if p.Source != "file" {
		t.Errorf("source = %q, want file", p.Source)
	}
}

// A persona declaring no rules must be indistinguishable from one written
// before the field existed: both leave the connection-level check as the only
// gate, which nil APIRoutes is what expresses.
func TestPersonaDef_NoAPIRoutesIsNil(t *testing.T) {
	p := personacfg.PersonaDef{DisplayName: "Analyst"}.ToPersona("analyst", "file")
	if p.APIRoutes != nil {
		t.Errorf("APIRoutes = %v, want nil", p.APIRoutes)
	}
}

func TestPersonaDef_ToPersonaCarriesEveryField(t *testing.T) {
	def := personacfg.PersonaDef{
		DisplayName: "Analyst",
		Description: "reads data",
		Roles:       []string{"analyst"},
		Tools:       personacfg.ToolRulesDef{Allow: []string{"trino_*"}, Deny: []string{"trino_execute"}},
		Connections: personacfg.ConnectionRulesDef{Allow: []string{"prod-*"}, Deny: []string{"prod-write"}},
		Context:     personacfg.ContextDef{DescriptionPrefix: "hello", AgentInstructionsSuffix: "bye"},
		Priority:    7,
	}
	p := def.ToPersona("analyst", "database")

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"name", p.Name, "analyst"},
		{"display name", p.DisplayName, "Analyst"},
		{"description", p.Description, "reads data"},
		{"tools allow", p.Tools.Allow[0], "trino_*"},
		{"tools deny", p.Tools.Deny[0], "trino_execute"},
		{"connections allow", p.Connections.Allow[0], "prod-*"},
		{"connections deny", p.Connections.Deny[0], "prod-write"},
		{"description prefix", p.Context.DescriptionPrefix, "hello"},
		{"instructions suffix", p.Context.AgentInstructionsSuffix, "bye"},
		{"priority", p.Priority, 7},
		{"source", p.Source, "database"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
}
