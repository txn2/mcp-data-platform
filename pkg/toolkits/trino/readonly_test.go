package trino

import (
	"context"
	"testing"

	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

// assertQueriesAllowed is a test helper that asserts all queries are allowed by the interceptor.
func assertQueriesAllowed(t *testing.T, interceptor *ReadOnlyInterceptor, queries []string, tool trinotools.ToolName, label string) {
	t.Helper()
	ctx := context.Background()
	for _, q := range queries {
		result, err := interceptor.Intercept(ctx, q, tool)
		if err != nil {
			t.Errorf("%s should be allowed: %q, got error: %v", label, q, err)
		}
		if result != q {
			t.Errorf("query should be unchanged: got %q, want %q", result, q)
		}
	}
}

// assertQueriesBlocked is a test helper that asserts all queries are blocked by the interceptor.
func assertQueriesBlocked(t *testing.T, interceptor *ReadOnlyInterceptor, queries []string, label string) {
	t.Helper()
	ctx := context.Background()
	for _, q := range queries {
		_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
		if err == nil {
			t.Errorf("%s should be blocked: %q", label, q)
		}
	}
}

func TestReadOnlyInterceptor_AllowsSelectQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesAllowed(t, interceptor, []string{
		"SELECT * FROM users",
		"select name from orders",
		"  SELECT id FROM products WHERE active = true",
		"-- comment\nSELECT * FROM table",
		"/* block comment */ SELECT id FROM test",
	}, trinotools.ToolQuery, "SELECT")
}

func TestReadOnlyInterceptor_AllowsShowQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesAllowed(t, interceptor, []string{
		"SHOW TABLES",
		"SHOW CATALOGS",
		"SHOW SCHEMAS FROM hive",
		"show columns from users",
	}, trinotools.ToolQuery, "SHOW")
}

func TestReadOnlyInterceptor_AllowsDescribeQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesAllowed(t, interceptor, []string{
		"DESCRIBE users",
		"describe hive.default.orders",
	}, trinotools.ToolQuery, "DESCRIBE")
}

func TestReadOnlyInterceptor_AllowsExplainQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesAllowed(t, interceptor, []string{
		"EXPLAIN SELECT * FROM users",
		"EXPLAIN ANALYZE SELECT * FROM orders",
	}, trinotools.ToolExplain, "EXPLAIN")
}

func TestReadOnlyInterceptor_BlocksInsertQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"INSERT INTO users VALUES (1, 'test')",
		"insert into orders (id) values (1)",
		"  INSERT INTO products SELECT * FROM temp",
		"-- comment\nINSERT INTO test VALUES (1)",
	}, "INSERT")
}

func TestReadOnlyInterceptor_BlocksUpdateQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"UPDATE users SET name = 'test'",
		"update orders set status = 'complete'",
		"UPDATE products SET price = 0 WHERE id = 1",
	}, "UPDATE")
}

func TestReadOnlyInterceptor_BlocksDeleteQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"DELETE FROM users",
		"delete from orders where id = 1",
		"DELETE FROM products WHERE active = false",
	}, "DELETE")
}

func TestReadOnlyInterceptor_BlocksDropQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"DROP TABLE users",
		"drop schema test",
		"DROP TABLE IF EXISTS temp",
		"DROP VIEW my_view",
	}, "DROP")
}

func TestReadOnlyInterceptor_BlocksCreateQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"CREATE TABLE users (id INT)",
		"create schema test",
		"CREATE VIEW my_view AS SELECT * FROM users",
		"CREATE TABLE AS SELECT * FROM temp",
	}, "CREATE")
}

func TestReadOnlyInterceptor_BlocksAlterQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"ALTER TABLE users ADD COLUMN email VARCHAR",
		"alter table orders drop column temp",
		"ALTER SCHEMA test RENAME TO test2",
	}, "ALTER")
}

func TestReadOnlyInterceptor_BlocksTruncateQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"TRUNCATE TABLE users",
		"truncate orders",
	}, "TRUNCATE")
}

func TestReadOnlyInterceptor_BlocksGrantQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"GRANT SELECT ON users TO admin",
		"grant all on orders to role_analyst",
	}, "GRANT")
}

func TestReadOnlyInterceptor_BlocksRevokeQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"REVOKE SELECT ON users FROM admin",
		"revoke all on orders from role_analyst",
	}, "REVOKE")
}

func TestReadOnlyInterceptor_BlocksMergeQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"MERGE INTO target USING source ON target.id = source.id",
	}, "MERGE")
}

func TestReadOnlyInterceptor_BlocksCallQueries(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"CALL system.sync_partition_metadata('catalog', 'schema', 'table')",
	}, "CALL")
}

func TestReadOnlyInterceptor_BlocksWriteWithComments(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	assertQueriesBlocked(t, interceptor, []string{
		"-- This is a comment\nDELETE FROM users",
		"/* comment */ INSERT INTO users VALUES (1)",
		"/* multi\nline */ DROP TABLE test",
	}, "write with comments")
}

func TestReadOnlyInterceptor_ErrorMessage(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	ctx := context.Background()
	_, err := interceptor.Intercept(ctx, "DELETE FROM users", trinotools.ToolQuery)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "write operations not allowed in read-only mode" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestIsWriteQuery(t *testing.T) {
	t.Run("SELECT with subquery containing write keyword is allowed", func(t *testing.T) {
		// This should be allowed because DELETE is not at the start
		sql := "SELECT * FROM users WHERE delete_flag = true"
		if isWriteQuery(sql) {
			t.Errorf("SELECT with 'delete' in WHERE should be allowed: %q", sql)
		}
	})

	t.Run("SELECT with INSERT in column name is allowed", func(t *testing.T) {
		sql := "SELECT insert_date FROM orders"
		if isWriteQuery(sql) {
			t.Errorf("SELECT with 'insert' in column name should be allowed: %q", sql)
		}
	})
}
