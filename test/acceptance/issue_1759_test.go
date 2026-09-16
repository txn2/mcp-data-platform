//go:build integration

package acceptance

import (
	"bytes"
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

// Issue #1759: an API key was its own identity. A key session stamped
// "apikey:<name>" where the same human's OAuth session stamped their subject,
// so a person connecting through a client that speaks only bearer tokens was
// a different user from themselves signed in to the portal, with the key's
// roles rather than their own.
//
// What these hold, through the routes the portal's pages call and the identity
// route every session reads (GET /api/v1/portal/me): a key a person issues for
// themselves resolves to that person -- same user id, same email, same roles,
// same persona as their own signed-in session; a person cannot widen their own
// key; a person's key list holds only their own keys and a revoked key stops
// authenticating at once; an administrator can issue a key against a person's
// account, with that person's recorded roles by default and a narrower set on
// request; an administrator cannot bind a key to somebody the platform has
// never seen sign in; an administrator lists and revokes a key a person issued
// for themselves; and a key bound to nobody is the standalone service identity
// it has always been.
//
// Wire forms: the self-service create route decodes strictly into
// userKeyCreateRequest, whose `name`, `description` and `expires_in` are each
// a string, so each admits exactly one JSON form. The admin create route's
// `user_email` is a string and its `roles` is a []string -- and with
// `user_email` set, `roles` is optional, so the "no override" case has three
// forms the schema admits: the field omitted, `[]`, and `null`. Every one is
// sent as literal request bytes below and asserted to produce one result.

// issue1759KeycloakBase is where the dev stack's identity provider answers.
// The suite needs a real person session -- not an API key -- because the whole
// criterion is that the two resolve to one identity.
func issue1759KeycloakBase() string {
	if v := os.Getenv("KEYCLOAK_BASE_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return "http://localhost:9090"
}

const (
	// issue1759Realm, issue1759Client and issue1759Secret are the dev stack's
	// realm and portal client (dev/keycloak-realm.json, dev/platform.yaml).
	issue1759Realm  = "mcp-platform"
	issue1759Client = "mcp-data-platform-portal"
	issue1759Secret = "portal-dev-secret"

	// issue1759AnalystEmail is the dev realm person these criteria act as, and
	// issue1759AnalystPass their password.
	issue1759AnalystEmail = "analyst@example.com"
	issue1759AnalystPass  = "analyst-password"

	issue1759PortalKeysPath = "/api/v1/portal/api-keys"
	issue1759AdminKeysPath  = "/api/v1/admin/auth/keys"
	issue1759MePath         = "/api/v1/portal/me"
)

// issue1759SignIn is a real sign-in as email: it returns the id_token the
// platform accepts as a bearer, whose audience is the portal client. Signing
// in is also what records the person's subject and roles, which is what a key
// bound to them resolves through.
func issue1759SignIn(t *testing.T, email, password string) string {
	t.Helper()
	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {issue1759Client},
		"client_secret": {issue1759Secret},
		"username":      {email},
		"password":      {password},
		"scope":         {"openid profile email"},
	}
	endpoint := issue1759KeycloakBase() + "/realms/" + issue1759Realm + "/protocol/openid-connect/token"
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

// issue1759Request issues one authenticated request as the bearer given, and
// returns the status and the decoded body. The bearer is an id_token for a
// person's own session and an API key for a key session, which is the whole
// point: the same route, read two ways, must answer with one identity.
func issue1759Request(t *testing.T, bearer, method, path string, body []byte) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, baseURL()+path, reader) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
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

// issue1759Identity is what a session reports itself to be.
type issue1759Identity struct {
	UserID  string
	Email   string
	Roles   []string
	Persona string
}

// issue1759Me reads the identity route as bearer.
func issue1759Me(t *testing.T, bearer string) issue1759Identity {
	t.Helper()
	status, body := issue1759Request(t, bearer, http.MethodGet, issue1759MePath, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d: %v", issue1759MePath, status, body)
	}
	return issue1759Identity{
		UserID:  issue1759String(body, "user_id"),
		Email:   issue1759String(body, "email"),
		Roles:   issue1759Strings(body, "roles"),
		Persona: issue1759String(body, "persona"),
	}
}

// issue1759String reads a string field, answering "" when it is absent.
func issue1759String(body map[string]any, key string) string {
	s, _ := body[key].(string)
	return s
}

// issue1759Strings reads a string-array field as a sorted, comparable value.
func issue1759Strings(body map[string]any, key string) []string {
	raw, _ := body[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// issue1759Name is a key name unique to this run, so a key a failed earlier
// run left behind cannot answer for this one.
func issue1759Name(label string) string {
	return fmt.Sprintf("issue-1759-%s-%d", label, time.Now().UnixNano())
}

// issue1759CreateOwn issues a key for the signed-in person themselves and
// returns the key value.
func issue1759CreateOwn(t *testing.T, bearer, name string) string {
	t.Helper()
	status, body := issue1759Request(t, bearer, http.MethodPost, issue1759PortalKeysPath,
		[]byte(`{"name":"`+name+`","description":"issue 1759 acceptance"}`))
	if status != http.StatusCreated {
		t.Fatalf("POST %s: want 201, got %d: %v", issue1759PortalKeysPath, status, body)
	}
	key := issue1759String(body, "key")
	if key == "" {
		t.Fatalf("POST %s: the response carried no key: %v", issue1759PortalKeysPath, body)
	}
	return key
}

// issue1759DeleteOwn revokes one of the caller's own keys.
func issue1759DeleteOwn(t *testing.T, bearer, name string) int {
	t.Helper()
	status, _ := issue1759Request(t, bearer, http.MethodDelete, issue1759PortalKeysPath+"/"+url.PathEscape(name), nil)
	return status
}

// issue1759SameIdentity fails unless two sessions report one identity.
func issue1759SameIdentity(t *testing.T, want, got issue1759Identity, how string) {
	t.Helper()
	if got.UserID != want.UserID {
		t.Fatalf("%s: user_id is %q, but the person's own session is %q -- the platform holds two users for one human", how, got.UserID, want.UserID)
	}
	if got.Email != want.Email {
		t.Fatalf("%s: email is %q, want %q", how, got.Email, want.Email)
	}
	if strings.Join(got.Roles, ",") != strings.Join(want.Roles, ",") {
		t.Fatalf("%s: roles are %v, want %v", how, got.Roles, want.Roles)
	}
	if got.Persona != want.Persona {
		t.Fatalf("%s: persona is %q, want %q", how, got.Persona, want.Persona)
	}
}

// TestIssue1759_OwnKeyIsTheSamePerson holds the ticket's first criterion: a key
// a person issues for themselves resolves to that person, not to the key.
func TestIssue1759_OwnKeyIsTheSamePerson(t *testing.T) {
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	mine := issue1759Me(t, token)
	if mine.UserID == "" {
		t.Fatalf("the signed-in session reported no user_id")
	}
	if strings.HasPrefix(mine.UserID, "apikey:") {
		t.Fatalf("the signed-in session reported %q, which is a key identity", mine.UserID)
	}

	name := issue1759Name("own")
	key := issue1759CreateOwn(t, token, name)
	t.Cleanup(func() { issue1759DeleteOwn(t, token, name) })

	issue1759SameIdentity(t, mine, issue1759Me(t, key), "a key the person issued for themselves")
}

// TestIssue1759_OwnKeyCannotWiden holds that a person cannot give their own key
// access they do not have: the self-service route takes no roles at all.
func TestIssue1759_OwnKeyCannotWiden(t *testing.T) {
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	name := issue1759Name("widen")
	status, body := issue1759Request(t, token, http.MethodPost, issue1759PortalKeysPath,
		[]byte(`{"name":"`+name+`","roles":["admin"]}`))
	if status == http.StatusCreated {
		issue1759DeleteOwn(t, token, name)
		t.Fatalf("POST %s with roles was accepted: a person can widen their own key", issue1759PortalKeysPath)
	}
	if status != http.StatusBadRequest {
		t.Fatalf("POST %s with roles: want 400, got %d: %v", issue1759PortalKeysPath, status, body)
	}
}

// TestIssue1759_OwnKeysAreListedAndRevoked holds that a person's key list is
// their own and that revoking a key stops it authenticating at once.
func TestIssue1759_OwnKeysAreListedAndRevoked(t *testing.T) {
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	name := issue1759Name("revoke")
	key := issue1759CreateOwn(t, token, name)

	status, body := issue1759Request(t, token, http.MethodGet, issue1759PortalKeysPath, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d: %v", issue1759PortalKeysPath, status, body)
	}
	if !issue1759ListsKey(body, name) {
		t.Fatalf("GET %s: the person's own list does not hold %q: %v", issue1759PortalKeysPath, name, body)
	}

	// The key authenticates while it exists.
	if got := issue1759Me(t, key); got.Email != issue1759AnalystEmail {
		t.Fatalf("the key authenticated as %q, want %q", got.Email, issue1759AnalystEmail)
	}

	if got := issue1759DeleteOwn(t, token, name); got != http.StatusOK {
		t.Fatalf("DELETE %s/%s: want 200, got %d", issue1759PortalKeysPath, name, got)
	}
	if status, _ := issue1759Request(t, key, http.MethodGet, issue1759MePath, nil); status == http.StatusOK {
		t.Fatalf("the revoked key still authenticates")
	}
	if got := issue1759DeleteOwn(t, token, name); got != http.StatusNotFound {
		t.Fatalf("DELETE of an already revoked key: want 404, got %d", got)
	}
}

// issue1759ListsKey reports whether a key listing holds a key named name.
func issue1759ListsKey(body map[string]any, name string) bool {
	keys, _ := body["keys"].([]any)
	for _, raw := range keys {
		entry, ok := raw.(map[string]any)
		if ok && issue1759String(entry, "name") == name {
			return true
		}
	}
	return false
}

// TestIssue1759_AdminIssuesAKeyForAPerson holds the ticket's second criterion:
// an administrator issues a key against a person's account and it resolves to
// that person, carrying their recorded roles. The "no override" case is sent
// in all three forms the schema admits, and every one must answer alike.
func TestIssue1759_AdminIssuesAKeyForAPerson(t *testing.T) {
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	mine := issue1759Me(t, token)
	admin := connect(t)

	for _, form := range []struct {
		label string
		roles string
	}{
		{"roles omitted", ""},
		{"roles empty", `,"roles":[]`},
		{"roles null", `,"roles":null`},
	} {
		name := issue1759Name("bound")
		status, body := admin.rest(http.MethodPost, issue1759AdminKeysPath, bytes.NewReader(
			[]byte(`{"name":"`+name+`","user_email":"`+issue1759AnalystEmail+`"`+form.roles+`}`)))
		if status != http.StatusCreated {
			t.Fatalf("%s: POST %s: want 201, got %d: %v", form.label, issue1759AdminKeysPath, status, body)
		}
		key := issue1759String(body, "key")
		issue1759SameIdentity(t, mine, issue1759Me(t, key), "a key an administrator issued for the person ("+form.label+")")
		admin.rest(http.MethodDelete, issue1759AdminKeysPath+"/"+url.PathEscape(name), nil)
	}
}

// TestIssue1759_AdminMayNarrowABoundKey holds that an administrator may hand a
// bound key a narrower role set: it is still that person, with those roles.
func TestIssue1759_AdminMayNarrowABoundKey(t *testing.T) {
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	mine := issue1759Me(t, token)
	admin := connect(t)

	name := issue1759Name("narrowed")
	status, body := admin.rest(http.MethodPost, issue1759AdminKeysPath, bytes.NewReader(
		[]byte(`{"name":"`+name+`","user_email":"`+issue1759AnalystEmail+`","roles":["collaborator"]}`)))
	if status != http.StatusCreated {
		t.Fatalf("POST %s: want 201, got %d: %v", issue1759AdminKeysPath, status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, issue1759AdminKeysPath+"/"+url.PathEscape(name), nil) })

	got := issue1759Me(t, issue1759String(body, "key"))
	if got.UserID != mine.UserID {
		t.Fatalf("a narrowed key reported user_id %q, want the person's %q", got.UserID, mine.UserID)
	}
	if strings.Join(got.Roles, ",") != "collaborator" {
		t.Fatalf("a narrowed key carried roles %v, want [collaborator]", got.Roles)
	}
}

// TestIssue1759_AdminCannotBindToSomebodyUnseen holds that an administrator
// cannot bind a key to a directory entry the platform has never seen sign in:
// there is no subject to resolve and no roles to carry, so the key would
// authenticate as nobody and list nothing.
func TestIssue1759_AdminCannotBindToSomebodyUnseen(t *testing.T) {
	admin := connect(t)
	invited := fmt.Sprintf("issue-1759-invited-%d@example.com", time.Now().UnixNano())
	status, body := admin.rest(http.MethodPost, "/api/v1/admin/users", bytes.NewReader(
		[]byte(`{"email":"`+invited+`","first_name":"Issue","last_name":"Invited"}`)))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/admin/users: want 201, got %d: %v", status, body)
	}
	t.Cleanup(func() {
		admin.rest(http.MethodDelete, "/api/v1/admin/users/"+url.PathEscape(invited), nil)
	})

	name := issue1759Name("unseen")
	status, body = admin.rest(http.MethodPost, issue1759AdminKeysPath, bytes.NewReader(
		[]byte(`{"name":"`+name+`","user_email":"`+invited+`"}`)))
	if status == http.StatusCreated {
		admin.rest(http.MethodDelete, issue1759AdminKeysPath+"/"+url.PathEscape(name), nil)
		t.Fatalf("a key was bound to somebody the platform has never seen sign in")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("POST %s for an unseen person: want 400, got %d: %v", issue1759AdminKeysPath, status, body)
	}
}

// TestIssue1759_ServiceKeyIsUnaffected holds the ticket's third criterion: a
// key bound to nobody is the standalone identity it has always been.
func TestIssue1759_ServiceKeyIsUnaffected(t *testing.T) {
	admin := connect(t)
	name := issue1759Name("service")
	status, body := admin.rest(http.MethodPost, issue1759AdminKeysPath, bytes.NewReader(
		[]byte(`{"name":"`+name+`","roles":["admin"]}`)))
	if status != http.StatusCreated {
		t.Fatalf("POST %s: want 201, got %d: %v", issue1759AdminKeysPath, status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, issue1759AdminKeysPath+"/"+url.PathEscape(name), nil) })

	got := issue1759Me(t, issue1759String(body, "key"))
	if got.UserID != "apikey:"+name {
		t.Fatalf("a service key reported user_id %q, want %q", got.UserID, "apikey:"+name)
	}
	if strings.Join(got.Roles, ",") != "admin" {
		t.Fatalf("a service key carried roles %v, want [admin]", got.Roles)
	}
}

// TestIssue1759_AdminListsAndRevokesAPersonsKey holds that an administrator
// sees every key, a person's own included, and can revoke any of them.
func TestIssue1759_AdminListsAndRevokesAPersonsKey(t *testing.T) {
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	name := issue1759Name("adminrevoke")
	key := issue1759CreateOwn(t, token, name)
	t.Cleanup(func() { issue1759DeleteOwn(t, token, name) })

	admin := connect(t)
	status, body := admin.rest(http.MethodGet, issue1759AdminKeysPath, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d: %v", issue1759AdminKeysPath, status, body)
	}
	stored := issue1759BoundKeyName(body, issue1759AnalystEmail, name)
	if stored == "" {
		t.Fatalf("the administrator's key list does not hold the key %q the person issued, owned by %s: %v",
			name, issue1759AnalystEmail, body)
	}

	if status, _ := admin.rest(http.MethodDelete, issue1759AdminKeysPath+"/"+url.PathEscape(stored), nil); status != http.StatusOK {
		t.Fatalf("DELETE %s/%s: want 200, got %d", issue1759AdminKeysPath, stored, status)
	}
	if status, _ := issue1759Request(t, key, http.MethodGet, issue1759MePath, nil); status == http.StatusOK {
		t.Fatalf("a key the administrator revoked still authenticates")
	}
}

// issue1759BoundKeyName returns the stored name of the key owned by email
// whose user-facing name is label, or "" when the listing holds no such key.
func issue1759BoundKeyName(body map[string]any, email, label string) string {
	keys, _ := body["keys"].([]any)
	for _, raw := range keys {
		entry, ok := raw.(map[string]any)
		if !ok || issue1759String(entry, "user_email") != email {
			continue
		}
		if name := issue1759String(entry, "name"); strings.HasSuffix(name, label) {
			return name
		}
	}
	return ""
}

// TestIssue1759_AKeyMayNotManageKeys holds the refusal that keeps a key from
// widening itself. An administrator issues a key against a person's account
// with a narrower role set; that key authenticates as the person, so without
// this rule it could ask the self-service route for a second key for the same
// account -- one carrying that person's full roles and no expiry -- and the
// administrator's limits would be gone.
//
// The same rule is what stops a service key configured with somebody's address
// from listing, issuing or revoking that person's keys.
func TestIssue1759_AKeyMayNotManageKeys(t *testing.T) {
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	mine := issue1759Me(t, token)
	admin := connect(t)

	name := issue1759Name("narrowkey")
	status, body := admin.rest(http.MethodPost, issue1759AdminKeysPath, bytes.NewReader(
		[]byte(`{"name":"`+name+`","user_email":"`+issue1759AnalystEmail+`","roles":["collaborator"]}`)))
	if status != http.StatusCreated {
		t.Fatalf("POST %s: want 201, got %d: %v", issue1759AdminKeysPath, status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, issue1759AdminKeysPath+"/"+url.PathEscape(name), nil) })
	key := issue1759String(body, "key")

	// The key really is the person, with the narrower roles.
	if got := issue1759Me(t, key); got.UserID != mine.UserID || strings.Join(got.Roles, ",") != "collaborator" {
		t.Fatalf("the narrowed key reported %+v, want %s with [collaborator]", got, mine.UserID)
	}

	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, issue1759PortalKeysPath, ""},
		{http.MethodPost, issue1759PortalKeysPath, `{"name":"` + issue1759Name("escalated") + `"}`},
		{http.MethodDelete, issue1759PortalKeysPath + "/anything", ""},
	} {
		var payload []byte
		if call.body != "" {
			payload = []byte(call.body)
		}
		status, body := issue1759Request(t, key, call.method, call.path, payload)
		if status != http.StatusForbidden {
			t.Errorf("%s %s with a key: want 403, got %d: %v", call.method, call.path, status, body)
		}
	}

	// Nothing was issued: the person still holds only what they held.
	status, body = issue1759Request(t, token, http.MethodGet, issue1759PortalKeysPath, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d", issue1759PortalKeysPath, status)
	}
	keys, _ := body["keys"].([]any)
	for _, raw := range keys {
		entry, _ := raw.(map[string]any)
		if n := issue1759String(entry, "name"); strings.Contains(n, "escalated") {
			t.Fatalf("a key authenticating with a key issued itself %q", n)
		}
	}
}

// TestIssue1759_AServiceKeyCannotTakeOverAnAddress holds that a key bound to
// nobody, carrying a person's address, does not become the subject that address
// authenticates as -- which would make that person's own bound key present the
// service key's identity instead of theirs.
func TestIssue1759_AServiceKeyCannotTakeOverAnAddress(t *testing.T) {
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	mine := issue1759Me(t, token)
	admin := connect(t)

	// A service key configured with the analyst's address, as an operator may
	// well write it, and used once.
	service := issue1759Name("service-as-person")
	status, body := admin.rest(http.MethodPost, issue1759AdminKeysPath, bytes.NewReader(
		[]byte(`{"name":"`+service+`","email":"`+issue1759AnalystEmail+`","roles":["admin"]}`)))
	if status != http.StatusCreated {
		t.Fatalf("POST %s: want 201, got %d: %v", issue1759AdminKeysPath, status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, issue1759AdminKeysPath+"/"+url.PathEscape(service), nil) })
	if got := issue1759Me(t, issue1759String(body, "key")); got.UserID != "apikey:"+service {
		t.Fatalf("the service key reported %q, want apikey:%s", got.UserID, service)
	}

	// A key issued against the account after that still resolves to the person.
	bound := issue1759Name("after-service")
	status, body = admin.rest(http.MethodPost, issue1759AdminKeysPath, bytes.NewReader(
		[]byte(`{"name":"`+bound+`","user_email":"`+issue1759AnalystEmail+`"}`)))
	if status != http.StatusCreated {
		t.Fatalf("POST %s: want 201, got %d: %v", issue1759AdminKeysPath, status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, issue1759AdminKeysPath+"/"+url.PathEscape(bound), nil) })

	if got := issue1759Me(t, issue1759String(body, "key")); got.UserID != mine.UserID {
		t.Fatalf("a bound key reported %q after a service key used the same address, want the person's %q",
			got.UserID, mine.UserID)
	}
}
