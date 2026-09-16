//go:build integration

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #1762: the docs said "Managing keys is a signed-in action. A request
// that authenticated with an API key cannot issue, list or revoke keys", and
// the admin routes never enforced it -- a service key carrying the admin
// persona listed, issued and revoked keys, a bound key among them. The
// self-scoped portal routes did enforce it.
//
// The route is what it is: key management is an administrator's action and an
// API key carrying the admin persona is an administrator's credential. What
// changed is the sentence, and these criteria are the corrected one, executed
// against the routes it describes.
//
// What these hold: an API-key request carrying the admin persona lists,
// issues and revokes keys on the admin route, whichever header form carries
// the credential; a key issued that way authenticates; the same request binds
// a key to a person who has signed in, and that key then authenticates as that
// person rather than as a key; and the self-scoped routes, which act on the
// strength of the caller being signed in, refuse an API-key request with the
// 403 sentence -- while the same person's signed-in session is served.
//
// Wire forms: the admin create route decodes strictly into
// authKeyCreateRequest, whose `name`, `email`, `description`, `user_email` and
// `expires_in` are each a string and whose `roles` is a []string, so each
// admits exactly one JSON form; each is sent as literal request bytes. With
// `user_email` set, `roles` is optional, and the two forms that leave it out
// -- the field omitted and `[]` -- are both sent. The credential itself
// reaches the platform in both forms the token extractor accepts, X-API-Key
// and Authorization: Bearer.

const (
	issue1762AdminKeysPath  = "/api/v1/admin/auth/keys"
	issue1762PortalKeysPath = "/api/v1/portal/api-keys"
	issue1762MePath         = "/api/v1/portal/me"

	// issue1762Realm, issue1762Client and issue1762Secret are the dev
	// stack's realm and portal client (dev/keycloak-realm.json,
	// dev/platform.yaml), and issue1762Person the ordinary person a bound
	// key is issued against.
	issue1762Realm      = "mcp-platform"
	issue1762Client     = "mcp-data-platform-portal"
	issue1762Secret     = "portal-dev-secret"
	issue1762Person     = "analyst@example.com"
	issue1762PersonPass = "analyst-password"

	// issue1762KeyRole is a role the deployment's personas carry, so the
	// key a criterion issues reaches a persona and its identity route
	// answers. A key whose roles reach none authenticates and is refused
	// by every surface, which is a different fact from the one under test.
	issue1762KeyRole = "collaborator"
)

// issue1762Keycloak is where the dev stack's identity provider answers. A real
// sign-in is needed because a key bound to somebody is only issuable once the
// platform has seen that person authenticate.
func issue1762Keycloak() string {
	if v := os.Getenv("KEYCLOAK_BASE_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return "http://localhost:9090"
}

// issue1762SignIn returns the id_token the platform accepts as a bearer for
// email, which is also what records their subject and roles.
func issue1762SignIn(t *testing.T, email, password string) string {
	t.Helper()
	form := url.Values{
		"grant_type": {"password"}, "client_id": {issue1762Client},
		"client_secret": {issue1762Secret}, "username": {email},
		"password": {password}, "scope": {"openid profile email"},
	}
	endpoint := issue1762Keycloak() + "/realms/" + issue1762Realm + "/protocol/openid-connect/token"
	res, err := http.Post(endpoint, "application/x-www-form-urlencoded", strings.NewReader(form.Encode())) // #nosec G704 -- the dev stack's identity provider, named by the suite's own environment
	if err != nil {
		t.Fatalf("signing in as %s: %v (is the dev stack up? make dev)", email, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("signing in as %s: reading the body: %v", email, err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("signing in as %s: %d %s", email, res.StatusCode, string(raw))
	}
	var out struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("signing in as %s: decoding the token response: %v", email, err)
	}
	if out.IDToken == "" {
		t.Fatalf("signing in as %s: the token response carried no id_token", email)
	}
	return out.IDToken
}

// issue1762Credential is how one request carries its credential: the header
// name and the value format. Both forms the platform's token extractor accepts
// are exercised, because the ticket's report was made with the first.
type issue1762Credential struct {
	label  string
	header string
	value  func(string) string
}

var issue1762Credentials = []issue1762Credential{
	{label: "X-API-Key", header: "X-API-Key", value: func(v string) string { return v }},
	{label: "Authorization", header: "Authorization", value: func(v string) string { return "Bearer " + v }},
}

// issue1762Do issues one request with the credential given and returns the
// status and the decoded body.
func issue1762Do(
	t *testing.T, cred issue1762Credential, secret, method, path string, body []byte,
) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, baseURL()+path, reader)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	req.Header.Set(cred.header, cred.value(secret))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the body: %v", method, path, err)
	}
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out) //nolint:errcheck // a non-object body leaves out nil
	}
	return res.StatusCode, out
}

// issue1762Bearer issues a request carrying a bearer token, which is how a
// signed-in person's session reaches the platform.
func issue1762Bearer(t *testing.T, token, method, path string, body []byte) (int, map[string]any) {
	t.Helper()
	return issue1762Do(t, issue1762Credentials[1], token, method, path, body)
}

// issue1762Revoke removes a key by name, whatever the criterion did with it.
// It carries a context of its own because it runs as a cleanup, after the
// test's context is already cancelled, and it reports nothing: the criterion
// has had its verdict by then and a key left behind is not one.
func issue1762Revoke(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL()+issue1762AdminKeysPath+"/"+name, nil)
	if err != nil {
		return
	}
	req.Header.Set("X-API-Key", devAPIKey())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	_ = res.Body.Close() //nolint:errcheck // best-effort cleanup
}

// TestIssue1762_AnAPIKeyManagesKeysOnTheAdminRoute is the corrected sentence:
// the admin route requires the admin persona and accepts any credential that
// carries it, in either header form.
func TestIssue1762_AnAPIKeyManagesKeysOnTheAdminRoute(t *testing.T) {
	for _, cred := range issue1762Credentials {
		t.Run(cred.label, func(t *testing.T) {
			status, listed := issue1762Do(t, cred, devAPIKey(), http.MethodGet, issue1762AdminKeysPath, nil)
			if status != http.StatusOK {
				t.Fatalf("listing keys: HTTP %d: %v", status, listed)
			}
			if _, ok := listed["keys"].([]any); !ok {
				t.Fatalf("the listing carried no keys array: %v", listed)
			}

			name := fmt.Sprintf("acc-1762-%s-%d", strings.ToLower(cred.header), time.Now().UnixNano())
			t.Cleanup(func() { issue1762Revoke(name) })
			body, err := json.Marshal(map[string]any{
				"name": name, "description": "Acceptance 1762.", "roles": []string{issue1762KeyRole},
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			status, created := issue1762Do(t, cred, devAPIKey(), http.MethodPost, issue1762AdminKeysPath, body)
			if status != http.StatusCreated {
				t.Fatalf("creating a key: HTTP %d: %v", status, created)
			}
			issued, _ := created["key"].(string)
			if issued == "" {
				t.Fatalf("the create carried no key value: %v", created)
			}

			// The key exists because it authenticates, not because a
			// response said so.
			status, me := issue1762Do(t, issue1762Credentials[0], issued, http.MethodGet, issue1762MePath, nil)
			if status != http.StatusOK {
				t.Fatalf("the issued key does not authenticate: HTTP %d: %v", status, me)
			}

			status, _ = issue1762Do(t, cred, devAPIKey(), http.MethodDelete, issue1762AdminKeysPath+"/"+name, nil)
			if status != http.StatusOK {
				t.Fatalf("revoking the key: HTTP %d", status)
			}
			if status, _ = issue1762Do(t, issue1762Credentials[0], issued, http.MethodGet, issue1762MePath, nil); status == http.StatusOK {
				t.Fatal("the revoked key still authenticates")
			}
		})
	}
}

// TestIssue1762_AnAPIKeyBindsAKeyToAPerson is the consequence the corrected
// paragraph states plainly: a credential carrying the admin persona issues a
// key that authenticates as somebody else, and that is what an administrator's
// credential does.
func TestIssue1762_AnAPIKeyBindsAKeyToAPerson(t *testing.T) {
	// The person must have signed in for the platform to know what they
	// authenticate as; signing in here is what records it.
	token := issue1762SignIn(t, issue1762Person, issue1762PersonPass)
	status, mine := issue1762Bearer(t, token, http.MethodGet, issue1762MePath, nil)
	if status != http.StatusOK {
		t.Fatalf("the signed-in session was refused: HTTP %d: %v", status, mine)
	}
	theirUserID, _ := mine["user_id"].(string)
	if theirUserID == "" {
		t.Fatalf("the signed-in session reported no user id: %v", mine)
	}

	// Both forms that leave the roles out: the field omitted, and an empty
	// array. Each issues a key carrying the person's own roles.
	for _, form := range []struct {
		label string
		body  map[string]any
	}{
		{label: "roles omitted", body: map[string]any{}},
		{label: "roles empty", body: map[string]any{"roles": []string{}}},
	} {
		t.Run(form.label, func(t *testing.T) {
			name := fmt.Sprintf("acc-1762-bound-%d", time.Now().UnixNano())
			t.Cleanup(func() { issue1762Revoke(name) })
			payload := map[string]any{"name": name, "user_email": issue1762Person}
			for k, v := range form.body {
				payload[k] = v
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			status, created := issue1762Do(t, issue1762Credentials[0], devAPIKey(),
				http.MethodPost, issue1762AdminKeysPath, body)
			if status != http.StatusCreated {
				t.Fatalf("binding a key to %s: HTTP %d: %v", issue1762Person, status, created)
			}
			issued, _ := created["key"].(string)
			if issued == "" {
				t.Fatalf("the create carried no key value: %v", created)
			}

			status, as := issue1762Do(t, issue1762Credentials[0], issued, http.MethodGet, issue1762MePath, nil)
			if status != http.StatusOK {
				t.Fatalf("the bound key does not authenticate: HTTP %d: %v", status, as)
			}
			if got, _ := as["user_id"].(string); got != theirUserID {
				t.Fatalf("the bound key authenticates as %q; the person signs in as %q", got, theirUserID)
			}
			if got, _ := as["email"].(string); !strings.EqualFold(got, issue1762Person) {
				t.Errorf("the bound key's address is %q, not the person's", got)
			}
		})
	}
}

// TestIssue1762_TheSelfScopedRoutesRefuseAKey is the half of the old sentence
// that was always true, and is where the docs now point: this page acts on the
// strength of the caller being signed in.
func TestIssue1762_TheSelfScopedRoutesRefuseAKey(t *testing.T) {
	body, err := json.Marshal(map[string]any{"name": "acc-1762-refused"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, call := range []struct {
		method string
		body   []byte
	}{
		{method: http.MethodGet},
		{method: http.MethodPost, body: body},
	} {
		t.Run(call.method, func(t *testing.T) {
			status, out := issue1762Do(t, issue1762Credentials[0], devAPIKey(),
				call.method, issue1762PortalKeysPath, call.body)
			if status != http.StatusForbidden {
				t.Fatalf("%s %s with an API key: HTTP %d: %v",
					call.method, issue1762PortalKeysPath, status, out)
			}
			text := fmt.Sprint(out)
			if !strings.Contains(text, "signed-in session") {
				t.Errorf("the refusal does not say what it wants instead: %v", out)
			}
		})
	}
}

// TestIssue1762_ThePersonsOwnSessionIsServedThere closes it: the refusal above
// is about the credential, not about the route being shut.
func TestIssue1762_ThePersonsOwnSessionIsServedThere(t *testing.T) {
	token := issue1762SignIn(t, issue1762Person, issue1762PersonPass)
	status, out := issue1762Bearer(t, token, http.MethodGet, issue1762PortalKeysPath, nil)
	if status != http.StatusOK {
		t.Fatalf("the person's own session was refused their own keys: HTTP %d: %v", status, out)
	}
	if _, ok := out["keys"]; !ok {
		t.Fatalf("the listing carried no keys field: %v", out)
	}
}
