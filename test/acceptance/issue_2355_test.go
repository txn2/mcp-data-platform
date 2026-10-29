//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #2355: platform.decrypt opens only what the form it names
// sealed. A run whose persona the form admits passes the ciphertext of a
// stored secret, read from gateway_secrets, as if it were one of the form's
// sensitive fields, and is refused without any part of the secret; the
// form's own submission still decrypts (#2179's criteria).
//
// Wire forms: manage_script run_draft takes literal tools/call params; the
// script passes platform.decrypt a dict payload.

func TestIssue2355_DecryptOpensNothingTheFormDidNotSeal(t *testing.T) {
	admin := connect(t)
	db := issue1870DB(t)
	form, _ := bindForm(t, admin, "acc-2355", issue2179Fields, map[string]any{"auth_mode": "public", "decrypt_personas": []string{"admin"}})
	name, value := secret2051(t, admin, []string{}, fixture2051)

	var sealed string
	if err := db.QueryRow(`SELECT value FROM gateway_secrets WHERE name = $1`, name).Scan(&sealed); err != nil {
		t.Fatalf("reading the stored secret's ciphertext: %v", err)
	}
	if !strings.HasPrefix(sealed, "enc:") {
		t.Fatalf("the stored secret is not held as ciphertext: %q", sealed)
	}
	src := `def main():
    """Passes a stored secret's ciphertext off as a sensitive field."""
    platform.result(platform.decrypt("` + form + `", {"values": {"ssn": "` + sealed + `"}, "sensitive": ["ssn"]}))
`
	res, text, err := admin.callRaw("manage_script", map[string]any{"command": "run_draft", "name": endpointName("acc-2355-draft"), "source": src})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, value) || strings.Contains(text, value[:4]) {
		t.Fatalf("the draft's answer carries the stored secret (error=%v): %s", res.IsError, text)
	}
	if !strings.Contains(text, "was not sealed by this form") {
		t.Fatalf("the draft was not refused for a value the form did not seal (error=%v): %s", res.IsError, text)
	}
}
