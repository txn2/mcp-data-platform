package registry

import "testing"

func TestPostgresAggregateFactoryBuildsTheOneToolkit(t *testing.T) {
	tk, err := PostgresAggregateFactory("orders", map[string]map[string]any{
		"orders": {"host": "db.example.com", "database": "orders", "user": "app"},
		"broken": {"host": "db.example.com"},
	})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	if tk.Kind() != "postgres" || tk.Name() != "orders" {
		t.Errorf("toolkit = %s/%s", tk.Kind(), tk.Name())
	}
	manager, ok := tk.(interface{ HasConnection(string) bool })
	if !ok || !manager.HasConnection("orders") || manager.HasConnection("broken") {
		t.Error("the valid instance should register and the broken one should not")
	}
	if err := ValidateConnectionConfig("postgres", map[string]any{"host": "db"}); err == nil {
		t.Error("a postgres config with no database validated")
	}
	if err := ValidateConnectionConfig("postgres", map[string]any{"host": "db", "database": "d", "user": "u"}); err != nil {
		t.Errorf("a valid postgres config was refused: %v", err)
	}
}
