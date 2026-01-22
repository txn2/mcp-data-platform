package trino

import (
	"context"
	"testing"

	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

func TestReadOnlyInterceptor_Intercept(t *testing.T) {
	interceptor := NewReadOnlyInterceptor()
	ctx := context.Background()

	t.Run("allows SELECT queries", func(t *testing.T) {
		queries := []string{
			"SELECT * FROM users",
			"select name from orders",
			"  SELECT id FROM products WHERE active = true",
			"-- comment\nSELECT * FROM table",
			"/* block comment */ SELECT id FROM test",
		}
		for _, q := range queries {
			result, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err != nil {
				t.Errorf("SELECT should be allowed: %q, got error: %v", q, err)
			}
			if result != q {
				t.Errorf("query should be unchanged: got %q, want %q", result, q)
			}
		}
	})

	t.Run("allows SHOW queries", func(t *testing.T) {
		queries := []string{
			"SHOW TABLES",
			"SHOW CATALOGS",
			"SHOW SCHEMAS FROM hive",
			"show columns from users",
		}
		for _, q := range queries {
			result, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err != nil {
				t.Errorf("SHOW should be allowed: %q, got error: %v", q, err)
			}
			if result != q {
				t.Errorf("query should be unchanged: got %q, want %q", result, q)
			}
		}
	})

	t.Run("allows DESCRIBE queries", func(t *testing.T) {
		queries := []string{
			"DESCRIBE users",
			"describe hive.default.orders",
		}
		for _, q := range queries {
			result, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err != nil {
				t.Errorf("DESCRIBE should be allowed: %q, got error: %v", q, err)
			}
			if result != q {
				t.Errorf("query should be unchanged: got %q, want %q", result, q)
			}
		}
	})

	t.Run("allows EXPLAIN queries", func(t *testing.T) {
		queries := []string{
			"EXPLAIN SELECT * FROM users",
			"EXPLAIN ANALYZE SELECT * FROM orders",
		}
		for _, q := range queries {
			result, err := interceptor.Intercept(ctx, q, trinotools.ToolExplain)
			if err != nil {
				t.Errorf("EXPLAIN should be allowed: %q, got error: %v", q, err)
			}
			if result != q {
				t.Errorf("query should be unchanged: got %q, want %q", result, q)
			}
		}
	})

	t.Run("blocks INSERT queries", func(t *testing.T) {
		queries := []string{
			"INSERT INTO users VALUES (1, 'test')",
			"insert into orders (id) values (1)",
			"  INSERT INTO products SELECT * FROM temp",
			"-- comment\nINSERT INTO test VALUES (1)",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("INSERT should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks UPDATE queries", func(t *testing.T) {
		queries := []string{
			"UPDATE users SET name = 'test'",
			"update orders set status = 'complete'",
			"UPDATE products SET price = 0 WHERE id = 1",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("UPDATE should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks DELETE queries", func(t *testing.T) {
		queries := []string{
			"DELETE FROM users",
			"delete from orders where id = 1",
			"DELETE FROM products WHERE active = false",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("DELETE should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks DROP queries", func(t *testing.T) {
		queries := []string{
			"DROP TABLE users",
			"drop schema test",
			"DROP TABLE IF EXISTS temp",
			"DROP VIEW my_view",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("DROP should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks CREATE queries", func(t *testing.T) {
		queries := []string{
			"CREATE TABLE users (id INT)",
			"create schema test",
			"CREATE VIEW my_view AS SELECT * FROM users",
			"CREATE TABLE AS SELECT * FROM temp",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("CREATE should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks ALTER queries", func(t *testing.T) {
		queries := []string{
			"ALTER TABLE users ADD COLUMN email VARCHAR",
			"alter table orders drop column temp",
			"ALTER SCHEMA test RENAME TO test2",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("ALTER should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks TRUNCATE queries", func(t *testing.T) {
		queries := []string{
			"TRUNCATE TABLE users",
			"truncate orders",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("TRUNCATE should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks GRANT queries", func(t *testing.T) {
		queries := []string{
			"GRANT SELECT ON users TO admin",
			"grant all on orders to role_analyst",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("GRANT should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks REVOKE queries", func(t *testing.T) {
		queries := []string{
			"REVOKE SELECT ON users FROM admin",
			"revoke all on orders from role_analyst",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("REVOKE should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks MERGE queries", func(t *testing.T) {
		queries := []string{
			"MERGE INTO target USING source ON target.id = source.id",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("MERGE should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks CALL queries", func(t *testing.T) {
		queries := []string{
			"CALL system.sync_partition_metadata('catalog', 'schema', 'table')",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("CALL should be blocked: %q", q)
			}
		}
	})

	t.Run("blocks write with comments", func(t *testing.T) {
		queries := []string{
			"-- This is a comment\nDELETE FROM users",
			"/* comment */ INSERT INTO users VALUES (1)",
			"/* multi\nline */ DROP TABLE test",
		}
		for _, q := range queries {
			_, err := interceptor.Intercept(ctx, q, trinotools.ToolQuery)
			if err == nil {
				t.Errorf("write with comments should be blocked: %q", q)
			}
		}
	})

	t.Run("error message is clear", func(t *testing.T) {
		_, err := interceptor.Intercept(ctx, "DELETE FROM users", trinotools.ToolQuery)
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "write operations not allowed in read-only mode" {
			t.Errorf("unexpected error message: %v", err)
		}
	})
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
