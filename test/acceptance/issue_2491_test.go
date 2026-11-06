//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2491: creating an application whose connection names no
// application database connection is refused at create and stores nothing;
// the New application dialog's choices list the application database
// connections the caller reaches, each with its schemas, and a schema another
// application holds is marked with that application's name, before its first
// release creates it.
//
// Wire forms: manage_application takes command, name, connection and schema
// as strings; the dialog's choices are GET /api/v1/portal/application-databases.

func TestIssue2491_AConnectionThatIsNoApplicationDatabaseIsRefusedAtCreate(t *testing.T) {
	admin := connect(t)
	name := "acc-2491-" + unique1579()
	res, text, err := admin.callRaw("manage_application", map[string]any{"command": "create", "name": name, "connection": "appdb-qq", "schema": "shop"})
	if err != nil || !res.IsError || !strings.Contains(text, `connection "appdb-qq" is not an application database connection`) {
		t.Fatalf("create on a connection that does not exist: %v %s", err, text)
	}
	res, _, _ = admin.callRaw("manage_application", map[string]any{"command": "get", "application": name})
	if !res.IsError {
		t.Fatalf("a refused create stored the application")
	}
}

func TestIssue2491_TheDialogsChoicesMarkAHeldSchema(t *testing.T) {
	admin := connect(t)
	name := "acc-2491-" + unique1579()
	schema := "acc2491_" + unique1579()
	created := admin.call("manage_application", map[string]any{"command": "create", "name": name, "connection": conn2404, "schema": schema})
	if created["name"] != name {
		t.Fatalf("create: %v", created)
	}
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})

	status, body := admin.rest(http.MethodGet, "/api/v1/portal/application-databases", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the dialog's choices: %d %v", status, body)
	}
	conns, _ := body["connections"].([]any)
	var held, public bool
	for _, c := range conns {
		conn, _ := c.(map[string]any)
		if conn["connection"] != conn2404 {
			continue
		}
		for _, s := range conn["schemas"].([]any) {
			sch, _ := s.(map[string]any)
			if sch["name"] == schema && sch["held_by"] == name {
				held = true
			}
			if sch["name"] == "public" {
				public = true
			}
		}
	}
	if !held || !public {
		t.Fatalf("the choices for %s: held=%v public=%v in %s", conn2404, held, public, fmt.Sprint(conns))
	}
}
