//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Issue #2061: a Google service account is a first-class credential. The key
// file Google issues, or the name of a stored secret holding it, plus the
// scopes, is the whole connection; the platform carries the scopes in the
// signed assertion, which is where Google reads them.
//
// These criteria run against Google: oauth2.googleapis.com issues the token
// and the Display & Video 360 API answers the call, with a service account key
// whose account has been granted DV360 access. The key is read from the file
// GOOGLE_SA_KEY_FILE names, sent only through the admin API, and never logged:
// every read of the connection afterwards returns it redacted.
//
// Wire forms: google_service_account_json is sent both as a string of JSON
// and as the JSON object the file is, and the two must produce the same
// connection (an object is stored as its text, the form the at-rest
// encryption encrypts). oauth_scope is a space-delimited string; the stored
// secret's allow_connections and allow_personas are lists. api_invoke_endpoint
// is sent with method and path strings and no body. The connection test route
// takes no body.

const (
	dv360Base2061  = "https://displayvideo.googleapis.com"
	dv360Scope2061 = "https://www.googleapis.com/auth/display-video"
	purpose2061    = "Acceptance #2061: a Google service account connection."
)

// keyFile2061 is the key file's text and the account it names.
func keyFile2061(t *testing.T) (text, email string) {
	t.Helper()
	path := os.Getenv("GOOGLE_SA_KEY_FILE")
	if path == "" {
		t.Fatal("GOOGLE_SA_KEY_FILE names no key file; these criteria run against Google with a real service account key")
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- a path the operator running the suite names
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}
	var file struct {
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.ClientEmail == "" {
		t.Fatal("GOOGLE_SA_KEY_FILE is not a service account key file")
	}
	return string(raw), file.ClientEmail
}

// connection2061 saves an api connection and removes it after the test,
// returning the status the save answered.
func connection2061(t *testing.T, admin *client, name string, config map[string]any) int {
	t.Helper()
	status := admin.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": config, "description": purpose2061,
	})
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
	return status
}

// probe2061 runs the connection test. A failed test answers 503 with the
// same body a passing one answers 200 with.
func probe2061(t *testing.T, admin *client, name string) map[string]any {
	t.Helper()
	status, body := admin.rest(http.MethodPost, "/api/v1/admin/connection-instances/api/"+name+"/test", http.NoBody)
	if want := map[bool]int{true: http.StatusOK, false: http.StatusServiceUnavailable}[body["ok"] == true]; status != want {
		t.Fatalf("the connection test answered %d %v", status, body)
	}
	return body
}

// partners2061 calls DV360's partners listing through the connection.
func partners2061(t *testing.T, admin *client, name string) map[string]any {
	t.Helper()
	return admin.call("api_invoke_endpoint", map[string]any{
		"connection": name, "method": http.MethodGet, "path": "/v4/partners", "purpose": purpose2061,
	})
}

func TestIssue2061_TheKeyFileAndTheScopesAreTheWholeConnection(t *testing.T) {
	admin := connect(t)
	text, email := keyFile2061(t)
	var object map[string]any
	if err := json.Unmarshal([]byte(text), &object); err != nil {
		t.Fatal(err)
	}
	for _, form := range []struct {
		label string
		value any
	}{
		{"string of JSON", text},
		{"object", object},
	} {
		t.Run(form.label, func(t *testing.T) {
			name := "acc-2061-" + strings.ReplaceAll(form.label, " ", "-") + "-" + unique1579()
			if status := connection2061(t, admin, name, map[string]any{
				"base_url": dv360Base2061, "google_service_account_json": form.value, "oauth_scope": dv360Scope2061,
			}); status != http.StatusOK && status != http.StatusCreated {
				t.Fatalf("saving the connection answered %d", status)
			}

			_, read := admin.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
			cfg, _ := read["config"].(map[string]any)
			if cfg["google_service_account_json"] != "[REDACTED]" {
				t.Fatalf("the key file read back as %T, not redacted", cfg["google_service_account_json"])
			}
			identity, _ := cfg["google_service_account_identity"].(map[string]any)
			if identity["client_email"] != email || identity["private_key_id"] == "" || identity["project_id"] == "" {
				t.Fatalf("the read names no account: %v", identity)
			}

			res := probe2061(t, admin, name)
			detail, _ := res["detail"].(string)
			if res["ok"] != true || !strings.Contains(detail, "issued a token for "+email+" with scopes "+dv360Scope2061) {
				t.Fatalf("the connection test = %v", res)
			}

			out := partners2061(t, admin, name)
			if status, _ := out["status"].(float64); status != http.StatusOK {
				t.Fatalf("DV360 answered the token %v: %v", out["status"], out)
			}
		})
	}
}

func TestIssue2061_TheKeyFromAStoredSecretNeverEntersTheConnection(t *testing.T) {
	admin := connect(t)
	text, email := keyFile2061(t)
	name := "acc-2061-secret-" + unique1579()
	other := "acc-2061-other-" + unique1579()
	secret := "acc2061_key_" + unique1579()
	if status := admin.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+secret, map[string]any{
		"description": purpose2061, "value": text, "allow_connections": []string{name}, "allow_personas": []string{},
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("storing the key answered %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+secret, http.NoBody) })

	for _, conn := range []string{name, other} {
		if status := connection2061(t, admin, conn, map[string]any{
			"base_url": dv360Base2061, "google_service_account_secret": secret, "oauth_scope": dv360Scope2061,
		}); status != http.StatusOK && status != http.StatusCreated {
			t.Fatalf("saving %s answered %d", conn, status)
		}
	}

	res := probe2061(t, admin, name)
	if detail, _ := res["detail"].(string); res["ok"] != true || !strings.Contains(detail, "issued a token for "+email) {
		t.Fatalf("the connection test = %v", res)
	}
	if out := partners2061(t, admin, name); out["status"] != float64(http.StatusOK) {
		t.Fatalf("DV360 answered %v: %v", out["status"], out)
	}

	refused := probe2061(t, admin, other)
	if errText, _ := refused["error"].(string); refused["ok"] == true || !strings.Contains(errText, `may not be used by connection "`+other+`"`) {
		t.Fatalf("a connection the secret does not allow = %v", refused)
	}
}

func TestIssue2061_ABadScopeSaysWhatToCheck(t *testing.T) {
	admin := connect(t)
	text, _ := keyFile2061(t)
	name := "acc-2061-scope-" + unique1579()
	if status := connection2061(t, admin, name, map[string]any{
		"base_url": dv360Base2061, "google_service_account_json": text,
		"oauth_scope": "https://www.googleapis.com/auth/no-such-scope",
	}); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d", status)
	}
	res := probe2061(t, admin, name)
	errText, _ := res["error"].(string)
	if res["ok"] == true || !strings.Contains(errText, "check oauth_scope") {
		t.Fatalf("the connection test = %v", res)
	}
}

func TestIssue2061_AFileThatIsNotAServiceAccountKeyIsRefusedByField(t *testing.T) {
	admin := connect(t)
	text, email := keyFile2061(t)
	var file map[string]any
	if err := json.Unmarshal([]byte(text), &file); err != nil {
		t.Fatal(err)
	}
	delete(file, "private_key")
	noKey, _ := json.Marshal(file)
	file["type"] = "authorized_user"
	oauthClient, _ := json.Marshal(file)
	for _, tc := range []struct {
		label, value, want string
	}{
		{"no private key", string(noKey), "has no private_key"},
		{"an OAuth client's file", string(oauthClient), `not "service_account"`},
	} {
		t.Run(tc.label, func(t *testing.T) {
			name := "acc-2061-refused-" + unique1579()
			status, body := admin.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, jsonBody(t, map[string]any{
				"config": map[string]any{"base_url": dv360Base2061, "google_service_account_json": tc.value, "oauth_scope": dv360Scope2061},
			}))
			t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
			detail, _ := body["detail"].(string)
			if status != http.StatusBadRequest || !strings.Contains(detail, tc.want) {
				t.Fatalf("saving it answered %d %q; want 400 naming %q", status, detail, tc.want)
			}
			if strings.Contains(detail, email) {
				t.Fatal("the refusal repeats a value from the file")
			}
		})
	}
}
