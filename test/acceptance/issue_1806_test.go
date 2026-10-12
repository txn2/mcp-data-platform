//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// #1806: a database-managed trino or s3 connection names its credential as a
// stored secret, {{secret:<name>}}, instead of holding it literally. The
// admin API stores and returns the reference as written, refuses one the
// secret does not allow, and the connection reads the secret as it is built.
// The s3 connection is the dev stack's SeaweedFS, whose keys the secrets hold.
//
// Wire forms: each admin route takes one JSON object; a credential field is a
// string holding the reference.

// s3Config1806 is the dev SeaweedFS connection with its keys named as secrets.
func s3Config1806(idSecret, keySecret string) map[string]any {
	return map[string]any{
		"endpoint": issue1757S3Endpoint(), "region": "us-east-1", "use_path_style": true,
		"access_key_id": "{{secret:" + idSecret + "}}", "secret_access_key": "{{secret:" + keySecret + "}}",
	}
}

// put1806 saves a connection and returns the status.
func put1806(t *testing.T, c *client, kind, name string, cfg map[string]any) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodPut, "/api/v1/admin/connection-instances/"+kind+"/"+name, jsonBody(t, map[string]any{
		"config": cfg, "description": "Acceptance #1806: a credential named as a stored secret.",
	}))
}

// TestIssue1806_AnS3ConnectionReadsItsKeysFromStoredSecrets: the keys are
// never in the request body, the stored row or the read; the connection lists
// the bucket with them; and a key the secret is changed to is the one the next
// build of the connection uses.
func TestIssue1806_AnS3ConnectionReadsItsKeysFromStoredSecrets(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1806-s3-%d", time.Now().UnixNano())
	idSecret := issue2066Secret(t, c, name, "dev-access-key")
	keySecret := issue2066Secret(t, c, name, "dev-secret-key")
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/s3/"+name, http.NoBody) })

	if status, out := put1806(t, c, "s3", name, s3Config1806(idSecret, keySecret)); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection: %d %v", status, out)
	}
	_, got := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/s3/"+name, http.NoBody)
	cfg, _ := got["config"].(map[string]any)
	if cfg["access_key_id"] != "{{secret:"+idSecret+"}}" || cfg["secret_access_key"] != "{{secret:"+keySecret+"}}" {
		t.Fatalf("the read returns %v; want the references as written", cfg)
	}
	var stored string
	if err := devDB(t).QueryRowContext(t.Context(),
		`SELECT config::text FROM connection_instances WHERE kind = 's3' AND name = $1`, name).Scan(&stored); err != nil {
		t.Fatalf("reading the stored row: %v", err)
	}
	if containsAny(stored, "dev-access-key", "dev-secret-key") {
		t.Fatalf("the stored row holds a key: %s", stored)
	}

	if res, text, err := c.callRaw("s3_list", map[string]any{"connection": name, "purpose": "Acceptance #1806: list with keys from stored secrets."}); err != nil || res.IsError {
		t.Fatalf("s3_list with the stored keys: %v %s", err, text)
	}

	// A wrong key in the secret is what the next build of the connection uses.
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+keySecret, map[string]any{
		"description": "Acceptance #1806: rotated to a wrong key.", "value": "not-the-key", "allow_connections": []string{name},
	}); status != http.StatusOK {
		t.Fatalf("rotating the secret answered %d", status)
	}
	if status, out := put1806(t, c, "s3", name, s3Config1806(idSecret, keySecret)); status != http.StatusOK {
		t.Fatalf("saving the connection again: %d %v", status, out)
	}
	if res, _, err := c.callRaw("s3_list", map[string]any{"connection": name, "purpose": "Acceptance #1806: list with a wrong key."}); err == nil && !res.IsError {
		t.Error("s3_list succeeded with the wrong key the secret now holds")
	}
}

// TestIssue1806_ASaveIsRefusedASecretOrFieldThatDoesNotFit: a secret not
// allowed on the connection, and a reference in a field the kind does not
// fill, are refused naming them; a trino password naming an allowed secret
// saves and reads back as written.
func TestIssue1806_ASaveIsRefusedASecretOrFieldThatDoesNotFit(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1806-trino-%d", time.Now().UnixNano())
	secret := issue2066Secret(t, c, name, "a-trino-password")
	other := issue2066Secret(t, c, "some-other-connection", "x-value")
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/trino/"+name, http.NoBody) })
	base := func() map[string]any {
		return map[string]any{"host": "trino.example.com", "port": 443, "user": "svc", "ssl": true, "catalog": "memory"}
	}

	cfg := base()
	cfg["password"] = "{{secret:" + other + "}}"
	if status, out := put1806(t, c, "trino", name, cfg); status != http.StatusBadRequest || !containsAny(fmt.Sprint(out), other) {
		t.Errorf("a secret allowed elsewhere: %d %v", status, out)
	}
	cfg = base()
	cfg["user"] = "{{secret:" + secret + "}}"
	if status, out := put1806(t, c, "trino", name, cfg); status != http.StatusBadRequest || !containsAny(fmt.Sprint(out), "may be named in password") {
		t.Errorf("a reference in user: %d %v", status, out)
	}
	cfg = base()
	cfg["password"] = "{{secret:" + secret + "}}"
	if status, out := put1806(t, c, "trino", name, cfg); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("a password naming its secret: %d %v", status, out)
	}
	_, got := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/trino/"+name, http.NoBody)
	if saved, _ := got["config"].(map[string]any); saved["password"] != "{{secret:"+secret+"}}" {
		t.Errorf("the read returns %v; want the reference as written", saved["password"])
	}
}

// containsAny reports whether s holds any of subs.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
