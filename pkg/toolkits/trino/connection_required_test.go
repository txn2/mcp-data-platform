package trino

import (
	"context"
	"strings"
	"testing"

	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

func TestConnectionRequiredMiddleware_Before(t *testing.T) {
	conns := []ConnectionDescription{
		{Name: trinoTestWarehouse, Description: "Data warehouse for analytics", IsDefault: true},
		{Name: "elasticsearch", Description: "Elasticsearch for sales data", IsDefault: false},
		{Name: "cassandra", Description: "", IsDefault: false},
	}
	mw := NewConnectionRequiredMiddleware(conns)

	t.Run("passes when connection is set", func(t *testing.T) {
		tc := trinotools.NewToolContext(trinotools.ToolQuery, trinotools.QueryInput{
			SQL:        "SELECT 1",
			Connection: trinoTestWarehouse,
		})

		_, err := mw.Before(context.Background(), tc)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("rejects when connection is empty", func(t *testing.T) {
		tc := trinotools.NewToolContext(trinotools.ToolQuery, trinotools.QueryInput{
			SQL: "SELECT 1",
		})

		_, err := mw.Before(context.Background(), tc)
		if err == nil {
			t.Fatal("expected error for missing connection")
		}

		errMsg := err.Error()
		if !strings.Contains(errMsg, "multiple Trino connections") {
			t.Errorf("error should mention multiple connections, got: %s", errMsg)
		}
		if !strings.Contains(errMsg, trinoTestWarehouse) {
			t.Errorf("error should list warehouse, got: %s", errMsg)
		}
		if !strings.Contains(errMsg, "elasticsearch") {
			t.Errorf("error should list elasticsearch, got: %s", errMsg)
		}
		if !strings.Contains(errMsg, "Data warehouse for analytics") {
			t.Errorf("error should include descriptions, got: %s", errMsg)
		}
		if !strings.Contains(errMsg, "(default)") {
			t.Errorf("error should mark default connection, got: %s", errMsg)
		}
	})

	t.Run("skips list_connections tool", func(t *testing.T) {
		tc := trinotools.NewToolContext(trinotools.ToolListConnections, trinotools.ListConnectionsInput{})

		_, err := mw.Before(context.Background(), tc)
		if err != nil {
			t.Errorf("list_connections should be skipped, got: %v", err)
		}
	})

	t.Run("passes for describe_table with connection", func(t *testing.T) {
		tc := trinotools.NewToolContext(trinotools.ToolDescribeTable, trinotools.DescribeTableInput{
			Catalog:    "hive",
			Schema:     "default",
			Table:      "users",
			Connection: trinoTestWarehouse,
		})

		_, err := mw.Before(context.Background(), tc)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("rejects describe_table without connection", func(t *testing.T) {
		tc := trinotools.NewToolContext(trinotools.ToolDescribeTable, trinotools.DescribeTableInput{
			Catalog: "hive",
			Schema:  "default",
			Table:   "users",
		})

		_, err := mw.Before(context.Background(), tc)
		if err == nil {
			t.Fatal("expected error for missing connection on describe_table")
		}
	})

	t.Run("rejects list_catalogs without connection", func(t *testing.T) {
		tc := trinotools.NewToolContext(trinotools.ToolListCatalogs, trinotools.ListCatalogsInput{})

		_, err := mw.Before(context.Background(), tc)
		if err == nil {
			t.Fatal("expected error for missing connection on list_catalogs")
		}
	})

	t.Run("passes list_schemas with connection", func(t *testing.T) {
		tc := trinotools.NewToolContext(trinotools.ToolListSchemas, trinotools.ListSchemasInput{
			Catalog:    "hive",
			Connection: "elasticsearch",
		})

		_, err := mw.Before(context.Background(), tc)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("connection without description in error", func(t *testing.T) {
		_, err := mw.Before(context.Background(), trinotools.NewToolContext(
			trinotools.ToolQuery, trinotools.QueryInput{SQL: "SELECT 1"},
		))
		if err == nil {
			t.Fatal("expected error")
		}
		// cassandra has no description, should just show the name
		if !strings.Contains(err.Error(), "cassandra") {
			t.Errorf("error should list cassandra, got: %s", err.Error())
		}
	})
}

func TestConnectionRequiredMiddleware_After(t *testing.T) {
	mw := NewConnectionRequiredMiddleware(nil)
	result, err := mw.After(context.Background(), nil, nil, nil)
	if result != nil {
		t.Error("expected nil result passthrough")
	}
	if err != nil {
		t.Errorf("expected nil error passthrough, got: %v", err)
	}
}

func TestNewConnectionRequiredMiddleware_SortsDeterministically(t *testing.T) {
	conns := []ConnectionDescription{
		{Name: "zebra"},
		{Name: "alpha"},
		{Name: "middle"},
	}
	mw := NewConnectionRequiredMiddleware(conns)

	if mw.connections[0].Name != "alpha" {
		t.Errorf("expected first connection to be 'alpha', got %q", mw.connections[0].Name)
	}
	if mw.connections[1].Name != "middle" {
		t.Errorf("expected second connection to be 'middle', got %q", mw.connections[1].Name)
	}
	if mw.connections[2].Name != "zebra" {
		t.Errorf("expected third connection to be 'zebra', got %q", mw.connections[2].Name)
	}
}

func TestExtractConnectionFromInput(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		if got := extractConnectionFromInput(nil); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("non-struct input", func(t *testing.T) {
		if got := extractConnectionFromInput("not a struct"); got != "" {
			t.Errorf("expected empty for string, got %q", got)
		}
	})

	t.Run("struct without Connection field", func(t *testing.T) {
		type noConn struct {
			SQL string
		}
		if got := extractConnectionFromInput(noConn{SQL: "SELECT 1"}); got != "" {
			t.Errorf("expected empty for struct without Connection, got %q", got)
		}
	})

	t.Run("struct with Connection field", func(t *testing.T) {
		type withConn struct {
			Connection string
		}
		if got := extractConnectionFromInput(withConn{Connection: trinoTestWarehouse}); got != trinoTestWarehouse {
			t.Errorf("expected 'warehouse', got %q", got)
		}
	})

	t.Run("pointer to struct", func(t *testing.T) {
		type withConn struct {
			Connection string
		}
		input := &withConn{Connection: "prod"}
		if got := extractConnectionFromInput(input); got != "prod" {
			t.Errorf("expected 'prod', got %q", got)
		}
	})

	t.Run("nil pointer", func(t *testing.T) {
		var input *trinotools.QueryInput
		if got := extractConnectionFromInput(input); got != "" {
			t.Errorf("expected empty for nil pointer, got %q", got)
		}
	})

	t.Run("struct with non-string Connection", func(t *testing.T) {
		type badConn struct {
			Connection int
		}
		if got := extractConnectionFromInput(badConn{Connection: 42}); got != "" {
			t.Errorf("expected empty for non-string Connection, got %q", got)
		}
	})

	t.Run("real QueryInput", func(t *testing.T) {
		input := trinotools.QueryInput{SQL: "SELECT 1", Connection: trinoTestWarehouse}
		if got := extractConnectionFromInput(input); got != trinoTestWarehouse {
			t.Errorf("expected 'warehouse', got %q", got)
		}
	})

	t.Run("real ExecuteInput", func(t *testing.T) {
		input := trinotools.ExecuteInput{SQL: "INSERT INTO t VALUES (1)", Connection: trinoTestWarehouse}
		if got := extractConnectionFromInput(input); got != trinoTestWarehouse {
			t.Errorf("expected 'warehouse', got %q", got)
		}
	})
}

func TestFormatAvailableConnections(t *testing.T) {
	mw := NewConnectionRequiredMiddleware([]ConnectionDescription{
		{Name: trinoTestWarehouse, Description: "Analytics warehouse", IsDefault: true},
		{Name: "elasticsearch", Description: "Sales data", IsDefault: false},
		{Name: "bare", Description: "", IsDefault: false},
	})

	output := mw.formatAvailableConnections()

	if !strings.Contains(output, "Available connections:") {
		t.Error("expected header")
	}
	if !strings.Contains(output, "bare") {
		t.Error("expected bare connection")
	}
	if !strings.Contains(output, "warehouse (default): Analytics warehouse") {
		t.Errorf("expected formatted default with description, got:\n%s", output)
	}
	if !strings.Contains(output, "elasticsearch: Sales data") {
		t.Errorf("expected formatted non-default with description, got:\n%s", output)
	}
}
