//go:build integration

package acceptance

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Issue #2061: a Google service account is a first-class credential. The key
// file Google issues, or the name of a stored secret holding it, plus the
// scopes, is the whole connection; the platform fills the RFC 7523 jwt_bearer
// grant from it and carries the scopes in the signed assertion.
//
// The token endpoint is the dev stack's Keycloak, which accepts the same
// RFC 7523 grant a Google key signs. Each run makes a key pair, writes a key
// file in Google's format around it, and registers through Keycloak's admin
// API an issuer that trusts its public key, a confidential client allowed to
// exchange its assertions, and a user linked to the issuer, the account the
// token is issued for. Keycloak takes only a URL as an issuer, where Google's
// is the key's client_email, so the connection sets jwt_issuer, which wins
// over the file as every explicit key does; the key file stays Google's. The
// connection's upstream is Keycloak too: its userinfo endpoint answers only a
// token it issued, with the user it was issued for.
//
// Wire forms: google_service_account_json is sent both as a string of JSON
// and as the JSON object the file is, and the two must produce the same
// connection (an object is stored as its text, the form the at-rest
// encryption encrypts). oauth_scope is a space-delimited string; the stored
// secret's allow_connections and allow_personas are lists. api_invoke_endpoint
// is sent with method and path strings and no body. The connection test route
// takes no body.

const (
	purpose2061   = "Acceptance #2061: a Google service account connection."
	realm2061     = "mcp-platform"
	idp2061       = "acc-2061-service-account"
	client2061    = "acc-2061-grant"
	secret2061    = "acc-2061-grant-secret"
	user2061      = "acc-2061-account"
	subject2061   = "acceptance-2061-account"
	email2061     = "acceptance-2061@acceptance-2061.iam.gserviceaccount.com"
	issuer2061    = "https://acceptance-2061.example.com"
	scope2061     = "openid"
	badScope2061  = "acc-2061-no-such-scope"
	project2061   = "acceptance-2061"
	tokenPath2061 = "/realms/" + realm2061 + "/protocol/openid-connect/token"
	userinfo2061  = "/realms/" + realm2061 + "/protocol/openid-connect/userinfo"
)

// keycloak2061 is the dev stack's Keycloak, as issue1759KeycloakBase reads it.
func keycloak2061() string { return issue1759KeycloakBase() }

// kcAdmin2061 calls Keycloak's admin API with the dev admin's token,
// returning the status and the body.
func kcAdmin2061(t *testing.T, token, method, path string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, keycloak2061()+"/admin/realms/"+realm2061+path, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Keycloak admin %s %s: %v (the dev stack's Keycloak must answer at %s)", method, path, err, keycloak2061())
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// kcAdminToken2061 signs the dev admin in to the master realm.
func kcAdminToken2061(t *testing.T) string {
	t.Helper()
	resp, err := http.PostForm(keycloak2061()+"/realms/master/protocol/openid-connect/token", url.Values{
		"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {"admin"}, "password": {"admin"},
	})
	if err != nil {
		t.Fatalf("signing in to Keycloak at %s: %v", keycloak2061(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.AccessToken == "" {
		t.Fatalf("Keycloak's admin sign-in answered %d with no token", resp.StatusCode)
	}
	return out.AccessToken
}

// kcUpsert2061 creates the object at collection, or replaces the one at item
// when it exists.
func kcUpsert2061(t *testing.T, token, collection, item string, body any) {
	t.Helper()
	if status, _ := kcAdmin2061(t, token, http.MethodGet, item, nil); status == http.StatusOK {
		if status, out := kcAdmin2061(t, token, http.MethodPut, item, body); status != http.StatusNoContent {
			t.Fatalf("Keycloak PUT %s answered %d: %s", item, status, out)
		}
		return
	}
	if status, out := kcAdmin2061(t, token, http.MethodPost, collection, body); status != http.StatusCreated {
		t.Fatalf("Keycloak POST %s answered %d: %s", collection, status, out)
	}
}

// kcClientID2061 is the internal id of the confidential client.
func kcClientID2061(t *testing.T, token string) string {
	t.Helper()
	_, out := kcAdmin2061(t, token, http.MethodGet, "/clients?clientId="+client2061, nil)
	var clients []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &clients); err != nil || len(clients) != 1 {
		t.Fatalf("the client %s is not in the realm: %s", client2061, out)
	}
	return clients[0].ID
}

// kcUserID2061 is the internal id of the linked user, created when absent.
func kcUserID2061(t *testing.T, token string) string {
	t.Helper()
	find := func() string {
		_, out := kcAdmin2061(t, token, http.MethodGet, "/users?exact=true&username="+user2061, nil)
		var users []struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(out, &users)
		if len(users) == 1 {
			return users[0].ID
		}
		return ""
	}
	if id := find(); id != "" {
		return id
	}
	if status, out := kcAdmin2061(t, token, http.MethodPost, "/users", map[string]any{
		"username": user2061, "enabled": true, "email": user2061 + "@example.com", "emailVerified": true,
		"firstName": "Acceptance", "lastName": "Account",
	}); status != http.StatusCreated {
		t.Fatalf("creating the Keycloak user answered %d: %s", status, out)
	}
	return find()
}

// keyFile2061 makes a key pair, has Keycloak trust its public key for the
// service account's issuer, and returns the key file Google would issue for
// that account, with the token endpoint at Keycloak.
func keyFile2061(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	keyID := fmt.Sprintf("acc2061%d", time.Now().UnixNano())

	token := kcAdminToken2061(t)
	kcUpsert2061(t, token, "/identity-provider/instances", "/identity-provider/instances/"+idp2061, map[string]any{
		"alias": idp2061, "providerId": "jwt-authorization-grant", "enabled": true,
		"config": map[string]string{
			"issuer":                          issuer2061,
			"useJwksUrl":                      "false",
			"publicKeySignatureVerifier":      string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub})),
			"publicKeySignatureVerifierKeyId": keyID,
			"jwtAuthorizationGrantEnabled":    "true",
			// Google accepts an assertion living an hour, and the
			// platform signs one that long for a Google key.
			"jwtAuthorizationGrantMaxAllowedAssertionExpiration": "3600",
			"jwtAuthorizationGrantAllowedClockSkew":              "10",
		},
	})
	client := map[string]any{
		"clientId": client2061, "enabled": true, "publicClient": false, "secret": secret2061,
		"standardFlowEnabled": false, "directAccessGrantsEnabled": false, "serviceAccountsEnabled": false,
		"attributes": map[string]string{
			"oauth2.jwt.authorization.grant.enabled": "true",
			"oauth2.jwt.authorization.grant.idp":     idp2061,
		},
	}
	if _, out := kcAdmin2061(t, token, http.MethodGet, "/clients?clientId="+client2061, nil); !bytes.Contains(out, []byte(client2061)) {
		if status, out := kcAdmin2061(t, token, http.MethodPost, "/clients", client); status != http.StatusCreated {
			t.Fatalf("creating the Keycloak client answered %d: %s", status, out)
		}
	}
	client["id"] = kcClientID2061(t, token)
	if status, out := kcAdmin2061(t, token, http.MethodPut, "/clients/"+client["id"].(string), client); status != http.StatusNoContent {
		t.Fatalf("updating the Keycloak client answered %d: %s", status, out)
	}
	user := kcUserID2061(t, token)
	link := "/users/" + user + "/federated-identity/" + idp2061
	kcAdmin2061(t, token, http.MethodDelete, link, nil)
	if status, out := kcAdmin2061(t, token, http.MethodPost, link, map[string]any{
		"identityProvider": idp2061, "userId": subject2061, "userName": subject2061,
	}); status != http.StatusNoContent && status != http.StatusCreated {
		t.Fatalf("linking the user to the issuer answered %d: %s", status, out)
	}

	file, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     project2061,
		"private_key_id": keyID,
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   email2061,
		"client_id":      "100000000000000002061",
		"token_uri":      keycloak2061() + tokenPath2061,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(file)
}

// grant2061 is what the connection adds to a Google key for Keycloak: an
// issuer Keycloak accepts, the linked user the assertion names as its
// subject (Google's domain-wide delegation field), the confidential client
// that may exchange it, and the scope as a form parameter beside the claim,
// since Keycloak reads it there and userinfo needs openid.
func grant2061(config map[string]any) map[string]any {
	config["jwt_issuer"] = issuer2061
	if _, ok := config["oauth_scope_placement"]; !ok {
		config["oauth_scope_placement"] = "both"
	}
	config["jwt_subject"] = subject2061
	config["oauth_client_id"] = client2061
	config["oauth_client_secret"] = secret2061
	if _, ok := config["base_url"]; !ok {
		config["base_url"] = keycloak2061()
	}
	if _, ok := config["oauth_scope"]; !ok {
		config["oauth_scope"] = scope2061
	}
	return config
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

// issuedTo2061 calls Keycloak's userinfo through the connection, which
// answers only for a token Keycloak issued and still holds valid, and fails
// unless it names the linked user.
func issuedTo2061(t *testing.T, admin *client, name string) {
	t.Helper()
	out := admin.call("api_invoke_endpoint", map[string]any{
		"connection": name, "method": http.MethodGet, "path": userinfo2061, "purpose": purpose2061,
	})
	if status, _ := out["status"].(float64); status != http.StatusOK {
		t.Fatalf("userinfo answered %v: %v", out["status"], out)
	}
	if body, _ := out["body"].(map[string]any); body["preferred_username"] != user2061 {
		t.Fatalf("userinfo names %v, want %s: %v", body["preferred_username"], user2061, body)
	}
}

func TestIssue2061_TheKeyFileAndTheScopesAreTheWholeConnection(t *testing.T) {
	admin := connect(t)
	text := keyFile2061(t)
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
			if status := connection2061(t, admin, name, grant2061(map[string]any{
				"google_service_account_json": form.value,
			})); status != http.StatusOK && status != http.StatusCreated {
				t.Fatalf("saving the connection answered %d", status)
			}

			_, read := admin.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
			cfg, _ := read["config"].(map[string]any)
			if cfg["google_service_account_json"] != "[REDACTED]" {
				t.Fatalf("the key file read back as %T, not redacted", cfg["google_service_account_json"])
			}
			identity, _ := cfg["google_service_account_identity"].(map[string]any)
			if identity["client_email"] != email2061 || identity["private_key_id"] == "" || identity["project_id"] != project2061 {
				t.Fatalf("the read names no account: %v", identity)
			}

			res := probe2061(t, admin, name)
			detail, _ := res["detail"].(string)
			if res["ok"] != true || !strings.Contains(detail, "issued a token for "+subject2061+" with scopes ") || !strings.Contains(detail, scope2061) {
				t.Fatalf("the connection test = %v", res)
			}
			issuedTo2061(t, admin, name)
		})
	}
}

func TestIssue2061_TheKeyFromAStoredSecretNeverEntersTheConnection(t *testing.T) {
	admin := connect(t)
	text := keyFile2061(t)
	name := "acc-2061-secret-" + unique1579()
	other := "acc-2061-other-" + unique1579()
	secret := "acc2061_key_" + unique1579()
	if status := admin.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+secret, map[string]any{
		"description": purpose2061, "value": text, "allow_connections": []string{name}, "allow_personas": []string{},
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("storing the key answered %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+secret, http.NoBody) })

	// The token endpoint defaults to Google's when the key is in a secret,
	// so the connection names Keycloak's.
	for _, conn := range []string{name, other} {
		if status := connection2061(t, admin, conn, grant2061(map[string]any{
			"google_service_account_secret": secret, "oauth_token_url": keycloak2061() + tokenPath2061,
		})); status != http.StatusOK && status != http.StatusCreated {
			t.Fatalf("saving %s answered %d", conn, status)
		}
	}

	res := probe2061(t, admin, name)
	if detail, _ := res["detail"].(string); res["ok"] != true || !strings.Contains(detail, "issued a token for "+subject2061) {
		t.Fatalf("the connection test = %v", res)
	}
	issuedTo2061(t, admin, name)

	refused := probe2061(t, admin, other)
	if errText, _ := refused["error"].(string); refused["ok"] == true || !strings.Contains(errText, `may not be used by connection "`+other+`"`) {
		t.Fatalf("a connection the secret does not allow = %v", refused)
	}
}

// The token endpoint refuses a scope it does not know with invalid_scope,
// and the refusal says to check oauth_scope.
func TestIssue2061_ABadScopeSaysWhatToCheck(t *testing.T) {
	admin := connect(t)
	text := keyFile2061(t)
	name := "acc-2061-scope-" + unique1579()
	if status := connection2061(t, admin, name, grant2061(map[string]any{
		"google_service_account_json": text, "oauth_scope": badScope2061,
	})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d", status)
	}
	res := probe2061(t, admin, name)
	errText, _ := res["error"].(string)
	if res["ok"] == true || !strings.Contains(errText, "invalid_scope") || !strings.Contains(errText, "check oauth_scope") {
		t.Fatalf("the connection test = %v", res)
	}
}

func TestIssue2061_AFileThatIsNotAServiceAccountKeyIsRefusedByField(t *testing.T) {
	admin := connect(t)
	var file map[string]any
	if err := json.Unmarshal([]byte(keyFile2061(t)), &file); err != nil {
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
				"config": grant2061(map[string]any{"google_service_account_json": tc.value}),
			}))
			t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
			detail, _ := body["detail"].(string)
			if status != http.StatusBadRequest || !strings.Contains(detail, tc.want) {
				t.Fatalf("saving it answered %d %q; want 400 naming %q", status, detail, tc.want)
			}
			if strings.Contains(detail, email2061) {
				t.Fatal("the refusal repeats a value from the file")
			}
		})
	}
}
