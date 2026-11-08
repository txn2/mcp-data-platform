//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeApps is an Applications over a map: which application holds each
// schema, and the run role each application was given.
type fakeApps struct {
	mu      sync.Mutex
	holders map[string]string
	roles   map[string][2]string
}

func (f *fakeApps) Holder(_ context.Context, _, schema string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.holders[schema], nil
}

func (f *fakeApps) RunRole(_ context.Context, _, application string) (role, password string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.roles[application]
	return r[0], r[1], nil
}

// TestRunRole_RealDB is #2539's run role against PostgreSQL: created with a
// SCRAM verifier the server accepts and never the password, statements of
// the application's scripts run as it, it is granted the write role a
// migration defines, it reaches no other schema, a role of the same name
// already on the server is refused, and it is dropped. A schema no
// application holds is refused for migrations and listed by status.
func TestRunRole_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		tk := newRealToolkit(t, rt.platform, map[string]map[string]any{"target": rt.target})
		tk.SetMigrationStore(&memStore{})
		n := targetSeq.Add(1)
		shop, other := fmt.Sprintf("rr%d_%d", os.Getpid(), n), fmt.Sprintf("ro%d_%d", os.Getpid(), n)
		t.Cleanup(func() {
			db, _ := rt.target[cfgKeyDatabase].(string)
			for _, r := range []string{runRoleNameFor(db, shop), writeRoleName(shop), runRoleNameFor(db, other)} {
				_, _ = rt.server.Exec(`DROP ROLE IF EXISTS "` + r + `"`)
			}
		})
		apps := &fakeApps{holders: map[string]string{shop: "shop"}, roles: map[string][2]string{}}
		tk.SetApplications(apps.Holder, apps.RunRole)
		ctx := context.Background()

		role, password, err := tk.CreateRunRole(ctx, "target", shop)
		if err != nil {
			t.Fatalf("creating the run role: %v", err)
		}
		rr := struct{ Role, Password string }{role, password}
		db, _ := rt.target[cfgKeyDatabase].(string)
		if rr.Role != runRoleNameFor(db, shop) || len(rr.Password) < 40 {
			t.Fatalf("run role %+v", rr)
		}
		var stored string
		if err := rt.server.QueryRow(`SELECT rolpassword FROM pg_authid WHERE rolname = $1`, rr.Role).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(stored, "SCRAM-SHA-256$4096:") || strings.Contains(stored, rr.Password) {
			t.Fatalf("the server stores %q: want a SCRAM verifier, never the password", stored)
		}
		apps.roles["app_shop"] = [2]string{rr.Role, rr.Password}

		// The schema and a write role its migration defines, then the grant.
		for _, stmt := range []string{
			"CREATE SCHEMA " + shop, "CREATE TABLE " + shop + ".orders (id int)",
			"CREATE SCHEMA " + other, "CREATE TABLE " + other + ".secret (id int)",
			"CREATE ROLE " + writeRoleName(shop) + " NOLOGIN", "GRANT USAGE ON SCHEMA " + shop + " TO " + writeRoleName(shop),
			"GRANT SELECT, INSERT ON " + shop + ".orders TO " + writeRoleName(shop), "REVOKE ALL ON SCHEMA " + other + " FROM PUBLIC",
		} {
			execOK(t, tk, Statement{Connection: "target", SQL: stmt})
		}
		granted, err := tk.GrantRunRole(ctx, "target", shop)
		if err != nil || granted != writeRoleName(shop) {
			t.Fatalf("granting the write role: %q %v", granted, err)
		}
		if again, err := tk.GrantRunRole(ctx, "target", shop); err != nil || again != "" {
			t.Fatalf("a second grant grants nothing: %q %v", again, err)
		}
		if is, err := tk.IsMember(ctx, "target", rr.Role, writeRoleName(shop)); err != nil || !is {
			t.Fatalf("the run role is a member of the write role: %v %v", is, err)
		}

		appCtx := WithIdentity(ctx, Identity{Application: "app_shop", Source: SourceScript})
		res, err := tk.Query(appCtx, Statement{Connection: "target", SQL: "SELECT current_user"})
		if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != rr.Role {
			t.Fatalf("an application's statement runs as its run role: %v %v", res, err)
		}
		if _, err := tk.Execute(appCtx, Statement{Connection: "target", SQL: "INSERT INTO " + shop + ".orders VALUES (1)"}); err != nil {
			t.Fatalf("the run role writes its schema: %v", err)
		}
		_, err = tk.Query(appCtx, Statement{Connection: "target", SQL: "SELECT * FROM " + other + ".secret"})
		if err == nil || sqlStateOf(t, err) != "42501" {
			t.Fatalf("the run role reaches no other schema: %v", err)
		}
		_, err = tk.Execute(appCtx, Statement{Connection: "target", SQL: "DROP TABLE " + shop + ".orders"})
		if err == nil || sqlStateOf(t, err) != "42501" {
			t.Fatalf("the run role changes no table (only a migration does): %v", err)
		}
		plain, err := tk.Query(ctx, Statement{Connection: "target", SQL: "SELECT current_user"})
		if err != nil || plain.Rows[0][0] == rr.Role {
			t.Fatalf("a statement for no application runs as the connection's user: %v %v", plain, err)
		}

		var refused *RefusedError
		if _, _, err := tk.CreateRunRole(ctx, "target", shop); !errors.As(err, &refused) || refused.Code != codeRoleExists {
			t.Fatalf("a role of the same name is refused: %v", err)
		}

		// A schema no application holds: migrations refused, listed by status.
		_, err = tk.runSchema(ctx, schemaInput{Command: schemaAdd, Connection: "target", Schema: other, Version: 1, Name: "x", Up: "SELECT 1"})
		if !errors.As(err, &refused) || refused.Code != codeSchemaNotHeld || !strings.Contains(err.Error(), "manage_application add_part") {
			t.Fatalf("a migration on a schema no application holds is refused naming the next step: %v", err)
		}
		if _, err := tk.PendingPlan(ctx, "target", other); !errors.As(err, &refused) || refused.Code != codeSchemaNotHeld {
			t.Fatalf("the portal's plan is refused too: %v", err)
		}

		if err := tk.DropRunRole(ctx, "target", shop); err != nil {
			t.Fatalf("dropping the run role: %v", err)
		}
		var exists bool
		if err := rt.server.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, rr.Role).Scan(&exists); err != nil || exists {
			t.Fatalf("the run role is gone: %v %v", exists, err)
		}
		if err := tk.DropRunRole(ctx, "target", shop); err != nil {
			t.Fatalf("dropping a role already gone: %v", err)
		}
	})
}

// TestRunRole_RealDB_NeedsCreateRole: a connection whose user cannot create
// roles is refused naming the statement that grants it.
func TestRunRole_RealDB_NeedsCreateRole(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		user, _ := realRoles(t, rt.server)
		cfg := map[string]any{}
		for k, v := range rt.target {
			cfg[k] = v
		}
		cfg[cfgKeyUser], cfg[cfgKeyPassword] = user, "pw-"+user
		tk := newRealToolkit(t, rt.platform, map[string]map[string]any{"target": cfg})
		_, _, err := tk.CreateRunRole(context.Background(), "target", "nocr")
		var refused *RefusedError
		if !errors.As(err, &refused) || refused.Code != codeCreateRoleRequired || !strings.Contains(err.Error(), "CREATEROLE") {
			t.Fatalf("want the CREATEROLE refusal, got %v", err)
		}
	})
}
