package platform

import (
	"strings"
	"testing"
)

// TestNew_RefusesATrinoConfigBeforeTheDatabase is #2014's ordering: a Trino
// connection the client refuses fails startup before the database is opened,
// let alone migrated. The DSN names a database that cannot be reached, so an
// error about it would mean the check ran too late.
func TestNew_RefusesATrinoConfigBeforeTheDatabase(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test-platform"},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Database: DatabaseConfig{DSN: "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"},
		Toolkits: map[string]any{
			"trino": map[string]any{
				"enabled": true,
				"default": "warehouse",
				"instances": map[string]any{
					"warehouse": map[string]any{
						"host": "trino.internal", "port": 8080, "user": "svc", "password": "p", "ssl": false,
					},
				},
			},
		},
	}
	_, err := New(WithConfig(cfg))
	if err == nil {
		t.Fatal("startup accepted a password over plain HTTP")
	}
	msg := err.Error()
	for _, want := range []string{"before any database migration ran", `"warehouse"`, "set ssl: true"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal %q does not say %q", msg, want)
		}
	}
	if strings.Contains(msg, "connecting to database") {
		t.Errorf("the database was reached before the Trino check: %s", msg)
	}
}
