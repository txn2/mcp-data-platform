//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestThrowawayDatabases_RealDB runs every statement testdb.go sends against
// PostgreSQL: the CREATEDB check, the template build under its advisory lock,
// the rename into place, a clone from it, the forced drop and the sweep.
func TestThrowawayDatabases_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		server, target := rt.server, rt.target
		tk := newRealToolkit(t, rt.platform, map[string]map[string]any{"target": target})
		tk.SetMigrationStore(&memStore{})
		ctx := admin()
		t.Cleanup(func() {
			rows, err := server.Query("SELECT datname FROM pg_database WHERE starts_with(datname, 'mcp')")
			if err != nil {
				return
			}
			var names []string
			for rows.Next() {
				var n string
				if rows.Scan(&n) == nil && (scratchName.MatchString(n) || len(n) > len(templatePrefix) && n[:len(templatePrefix)] == templatePrefix) {
					names = append(names, n)
				}
			}
			_ = rows.Close()
			for _, n := range names {
				_, _ = server.Exec("DROP DATABASE IF EXISTS " + n + " WITH (FORCE)")
			}
		})
		exists := func(name string) bool {
			var ok bool
			if err := server.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&ok); err != nil {
				t.Fatal(err)
			}
			return ok
		}

		schema(t, ctx, tk, schemaInput{Command: schemaAdd, Schema: "app", Version: 1, Name: "users", Up: "CREATE TABLE users (id bigserial PRIMARY KEY, email text NOT NULL)"}, false)
		added := schema(t, ctx, tk, schemaInput{Command: schemaAdd, Schema: "app", Version: 2, Name: "unique email", Up: "ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email)"}, false)
		if fs, _ := added["findings"].([]any); len(fs) != 0 {
			t.Errorf("the migration was not applied from zero cleanly: %v", fs)
		}
		held, err := tk.migrations.List(ctx, "target", "app")
		if err != nil {
			t.Fatal(err)
		}
		if tmpl := templateName("target", "app", held); !exists(tmpl) {
			t.Fatalf("add left no template %s", tmpl)
		}

		db, err := tk.TestDatabase(ctx, "target", []string{"app"})
		if err != nil {
			t.Fatal(err)
		}
		insert := Statement{Connection: "target", SQL: "INSERT INTO app.users (email) VALUES ($1)", Params: []any{"a@example.com"}}
		if _, err := db.Execute(ctx, insert); err != nil {
			t.Fatalf("insert on the clone: %v", err)
		}
		var failed *FailedError
		if _, err := db.Execute(ctx, insert); !errors.As(err, &failed) || failed.Statement == nil || failed.Statement.SQLState != "23505" {
			t.Errorf("a duplicate on the clone = %v, want the unique constraint's 23505", err)
		}
		res, err := tk.Query(ctx, Statement{Connection: "target", SQL: "SELECT to_regclass('app.users') IS NULL AS absent"})
		if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != true {
			t.Errorf("the connection's own database holds the table the clone was built with: %v %v", res, err)
		}
		clone := db.Database
		db.Close()
		if exists(clone) {
			t.Errorf("the clone %s outlived its test", clone)
		}

		leftover := fmt.Sprintf("mcpc_%d_0123456789abcdef", time.Now().Add(-2*time.Hour).Unix())
		if _, err := server.Exec("CREATE DATABASE " + leftover); err != nil {
			t.Fatal(err)
		}
		n, err := tk.SweepTestDatabases(context.Background(), time.Hour)
		if err != nil || n < 1 || exists(leftover) {
			t.Errorf("sweep dropped %d (%v); the leftover clone exists: %v", n, err, exists(leftover))
		}
	})
}
