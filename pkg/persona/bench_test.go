package persona

import (
	"fmt"
	"testing"
)

// Every tool call and every tools/list asks the persona filter (#2716): a
// deployment-sized registry of 12 personas with glob allow/deny rules, the
// full tool inventory filtered, and one connection check.
func benchFilter(b *testing.B) (*ToolFilter, *Persona, []string) {
	b.Helper()
	reg := NewRegistry()
	var analyst *Persona
	for i := range 12 {
		p := &Persona{
			Name:        fmt.Sprintf("persona-%d", i),
			Roles:       []string{fmt.Sprintf("role-%d", i)},
			Tools:       ToolRules{Allow: []string{"*"}, Deny: []string{"*_delete_*", "manage_schema", "trino_execute"}},
			Connections: ConnectionRules{Allow: []string{"prod-*", "warehouse", "crm"}, Deny: []string{"prod-finance"}},
		}
		if err := reg.Register(p); err != nil {
			b.Fatal(err)
		}
		if i == 5 {
			analyst = p
		}
	}
	tools := make([]string, 0, 64)
	for _, base := range []string{"trino", "api", "graphql", "manage", "memory", "s3", "save", "search"} {
		for _, verb := range []string{"query", "describe_table", "export", "list", "invoke_endpoint", "delete_object", "discover", "capture"} {
			tools = append(tools, base+"_"+verb)
		}
	}
	return NewToolFilter(reg), analyst, tools
}

func BenchmarkFilterTools(b *testing.B) {
	f, p, tools := benchFilter(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = f.FilterTools(p, tools)
	}
}

func BenchmarkIsConnectionAllowed(b *testing.B) {
	f, p, _ := benchFilter(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = f.IsConnectionAllowed(p, "prod-orders")
	}
}
