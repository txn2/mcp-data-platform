//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2662: an application database connection saved through one
// replica is served at once by the other. With no wait, the other replica
// creates an application naming it and answers manage_schema status on it; a
// connection no replica saved is still refused with the same message.
//
// Wire forms: the admin connection route takes a JSON object (config of
// strings and numbers, description a string); manage_application takes
// command, name, description, connection and schema as strings; manage_schema
// takes command and connection as strings, sent as literal tools/call params.

// TestIssue2662_AConnectionSavedOnOneReplicaServesOnTheOther: saved through
// one replica, the connection is named at once on the other by
// manage_application create and manage_schema status.
func TestIssue2662_AConnectionSavedOnOneReplicaServesOnTheOther(t *testing.T) {
	one, other := connectReplicaPair(t)
	conn := appdbConn(t, one, "acc-2662", "orders_app", "orders_secret")

	app := "acc2662-" + unique1579()
	res, text, err := other.callRaw("manage_application", map[string]any{
		"command": "create", "name": app, "description": "Acceptance #2662",
		"connection": conn, "schema": "acc2662_" + unique1579(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _, _ = other.callRaw("manage_application", map[string]any{"command": "delete", "application": app})
	})
	if res.IsError {
		t.Fatalf("the other replica refused an application naming the connection just saved: %s", text)
	}

	res, text, err = other.callRaw("manage_schema", map[string]any{"command": "status", "connection": conn})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || strings.Contains(text, "is not an application database connection") {
		t.Fatalf("the other replica refused manage_schema status on the connection just saved: %s", text)
	}
	status, body := other.rest(http.MethodGet, appdbPath(conn, "/health"), http.NoBody)
	if status != http.StatusOK {
		t.Errorf("the other replica's admin health route answered %d on the connection just saved: %v", status, body)
	}
}

// TestIssue2662_AConnectionNoReplicaSavedIsStillRefused: a name no replica
// saved is refused on both, with the message it had.
func TestIssue2662_AConnectionNoReplicaSavedIsStillRefused(t *testing.T) {
	forEachReplica(t, func(t *testing.T, c *client) {
		missing := "acc-2662-none-" + unique1579()
		res, text, err := c.callRaw("manage_application", map[string]any{
			"command": "create", "name": "acc2662-none-" + unique1579(), "connection": missing, "schema": "acc2662_none",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || !strings.Contains(text, `connection "`+missing+`" is not an application database connection`) {
			t.Fatalf("an unsaved connection was not refused with the message it had: %s", text)
		}
	})
}
