//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Issue #2092: external personas and the accounts the platform creates for
// them. An administrator creates the account; the person signs in with the
// one-time link the invitation email holds, read here from the dev stack's
// Mailpit, and the session that opens is held to the external persona: the
// items shared with them, viewer only, no MCP tool, no admin route, no people
// directory. Expiry ends the session and the links.
//
// Wire forms: the admin persona body's external as the JSON boolean true; the
// external-user body as a JSON object with email, name and persona strings
// and expires_at absent; the sign-in request body {"email": string}; the claim
// link's token as the query parameter the email carries; the
// identity-provider sign-in as the browser's form post to Keycloak.

// issue2092Session is an external person signed in through their emailed
// link: the session cookie and the CSRF token /me issued for it.
type issue2092Session struct {
	cookie *http.Cookie
	csrf   string
}

// do sends one request with the session cookie.
func (s issue2092Session) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = jsonBody(t, body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, baseURL()+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(s.cookie)
	req.Header.Set("Content-Type", "application/json")
	if s.csrf != "" && method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", s.csrf)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

var issue2092Link = regexp.MustCompile(`https?://[^\s"<>]+/portal/auth/email-link/claim\?token=[A-Za-z0-9_\-%]+`)

// issue2092MailLink waits for Mailpit to hold a message to the address whose
// subject holds subject, and returns the sign-in link in it.
func issue2092MailLink(t *testing.T, to, subject string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out := getJSON(t, mailpitBase()+"/api/v1/search?query="+url.QueryEscape("to:"+to), "")
		items, _ := out["messages"].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			if s, _ := m["Subject"].(string); !strings.Contains(s, subject) {
				continue
			}
			id, _ := m["ID"].(string)
			msg := getJSON(t, mailpitBase()+"/api/v1/message/"+id, "")
			text, _ := msg["Text"].(string)
			if link := issue2092Link.FindString(text); link != "" {
				return link
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("no email to %s with %q and a sign-in link within 30s", to, subject)
	return ""
}

// issue2092Claim opens a sign-in link and returns the session it set, or
// the redirect it answered with when it set none.
func issue2092Claim(t *testing.T, link string) (issue2092Session, string) {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	// The link is built on the deployment's public address; the claim is made
	// at the address the suite reaches.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL()+u.RequestURI(), http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("claiming the link: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	for _, c := range res.Cookies() {
		if c.Value != "" {
			s := issue2092Session{cookie: c}
			_, me := s.do(t, http.MethodGet, "/api/v1/portal/me", nil)
			s.csrf, _ = me["csrf_token"].(string)
			return s, res.Header.Get("Location")
		}
	}
	return issue2092Session{}, res.Header.Get("Location")
}

// issue2092Setup creates an external persona, an account under it and four
// assets the administrator owns, three of them shared with the account, and
// signs the person in from the invitation.
type issue2092Setup struct {
	persona, email, accountID string
	shared                    []string
	unshared                  string
	invite                    string
	session                   issue2092Session
}

func newIssue2092(t *testing.T, feedback bool) issue2092Setup {
	t.Helper()
	admin := connect(t)
	stamp := time.Now().UnixNano()
	s := issue2092Setup{
		persona: fmt.Sprintf("acc-2092-%d", stamp),
		email:   fmt.Sprintf("acc-2092-%d@example.org", stamp),
	}
	role := strings.ReplaceAll(s.persona, "-", "_")
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": s.persona, "display_name": "Acceptance 2092", "roles": []any{role},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
		"external": true, "portal": map[string]any{"feedback": feedback},
	})); status != http.StatusCreated {
		t.Fatalf("creating the external persona: %d %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+s.persona, http.NoBody) })

	status, acct := admin.rest(http.MethodPost, "/api/v1/admin/external-users", jsonBody(t, map[string]any{
		"email": s.email, "name": "Acceptance Client", "persona": s.persona,
	}))
	if status != http.StatusCreated {
		t.Fatalf("creating the external user: %d %v", status, acct)
	}
	s.accountID, _ = acct["id"].(string)
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/external-users/"+s.accountID, http.NoBody) })

	for i := range 4 {
		saved := admin.call("save_asset", map[string]any{
			"name": fmt.Sprintf("acc-2092 item %d %d", i, stamp), "content_type": "text/html",
			"content": fmt.Sprintf("<p>acc-2092 item %d</p>", i), "description": "Acceptance #2092.",
		})
		id, _ := saved["asset_id"].(string)
		t.Cleanup(func() { _, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
		if i == 3 {
			s.unshared = id
			continue
		}
		if status, out := admin.rest(http.MethodPost, "/api/v1/portal/assets/"+id+"/shares", jsonBody(t, map[string]any{
			"shared_with_email": s.email, "permission": "editor",
		})); status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("sharing %s: %d %v", id, status, out)
		}
		s.shared = append(s.shared, id)
	}

	s.invite = issue2092MailLink(t, s.email, "You have been invited")
	var location string
	s.session, location = issue2092Claim(t, s.invite)
	if s.session.cookie == nil {
		t.Fatalf("the invitation link set no session; it redirected to %q", location)
	}
	return s
}

// The person signs in from the invitation and opens exactly the three items
// shared with them, as a viewer, and nothing else.
func TestIssue2092_AnExternalUserOpensExactlyWhatIsSharedWithThem(t *testing.T) {
	s := newIssue2092(t, true)

	status, me := s.session.do(t, http.MethodGet, "/api/v1/portal/me", nil)
	portal, _ := me["portal"].(map[string]any)
	if status != http.StatusOK || me["persona"] != s.persona || portal["external"] != true || me["is_admin"] == true {
		t.Fatalf("/me: %d %v, want the external persona", status, me)
	}

	status, list := s.session.do(t, http.MethodGet, "/api/v1/portal/shared-with-me", nil)
	if status != http.StatusOK {
		t.Fatalf("shared-with-me: %d %v", status, list)
	}
	raw, _ := json.Marshal(list)
	for _, id := range s.shared {
		if !strings.Contains(string(raw), id) {
			t.Errorf("shared-with-me does not list %s", id)
		}
		status, asset := s.session.do(t, http.MethodGet, "/api/v1/portal/assets/"+id, nil)
		if status != http.StatusOK {
			t.Errorf("opening shared %s: %d %v", id, status, asset)
		}
		if asset["share_permission"] != "viewer" {
			t.Errorf("shared %s permission = %v, want viewer although the share grants editor", id, asset["share_permission"])
		}
	}
	if strings.Contains(string(raw), s.unshared) {
		t.Errorf("shared-with-me lists %s, which is not shared with them", s.unshared)
	}
	if status, _ := s.session.do(t, http.MethodGet, "/api/v1/portal/assets/"+s.unshared, nil); status == http.StatusOK {
		t.Errorf("an asset not shared with them opened")
	}
	if status, out := s.session.do(t, http.MethodPost, "/api/v1/portal/assets", map[string]any{"name": "x", "content_type": "text/plain", "content": "x"}); status != http.StatusForbidden {
		t.Errorf("creating an asset: %d %v, want 403", status, out)
	}
}

// No MCP tool, admin route, people directory or other portal area answers
// the external session.
func TestIssue2092_NoToolAdminRouteOrDirectoryAnswersAnExternalUser(t *testing.T) {
	s := newIssue2092(t, true)
	for path, want := range map[string]int{
		"/api/v1/portal/users":              http.StatusForbidden,
		"/api/v1/portal/mention-candidates": http.StatusForbidden,
		"/api/v1/portal/api-keys":           http.StatusForbidden,
		"/api/v1/portal/knowledge-pages":    http.StatusForbidden,
		"/api/v1/portal/search?q=acc":       http.StatusForbidden,
		"/api/v1/portal/prompts":            http.StatusForbidden,
		"/api/v1/portal/scripts":            http.StatusForbidden,
		"/api/v1/apis":                      http.StatusForbidden,
	} {
		if status, out := s.session.do(t, http.MethodGet, path, nil); status != want {
			t.Errorf("GET %s: %d %v, want %d", path, status, out, want)
		}
	}
	if status, _ := s.session.do(t, http.MethodGet, "/api/v1/admin/system/info", nil); status == http.StatusOK {
		t.Error("an admin route answered the external session")
	}

	// The MCP endpoint with the session cookie lists no tool and calls none.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL()+"/",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"intent":"x"}}}`))
	req.AddCookie(s.session.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusOK && !strings.Contains(string(body), "error") && !strings.Contains(string(body), "isError") {
		t.Errorf("an MCP tool call answered the external session: %s", body)
	}
}

// After the account expires the session ends, the invitation and a new
// sign-in link no longer open, and no guest link is offered in their place.
func TestIssue2092_ExpiryEndsTheSessionAndTheLinks(t *testing.T) {
	s := newIssue2092(t, true)
	if status, _ := s.session.do(t, http.MethodGet, "/api/v1/portal/me", nil); status != http.StatusOK {
		t.Fatalf("before expiry /me answered %d", status)
	}
	db := issue1904DB(t)
	issue1904Exec(t, db, `UPDATE external_accounts SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, s.accountID)

	if status, _ := s.session.do(t, http.MethodGet, "/api/v1/portal/me", nil); status != http.StatusUnauthorized {
		t.Errorf("after expiry /me answered %d, want 401", status)
	}
	if status, _ := s.session.do(t, http.MethodGet, "/api/v1/portal/assets/"+s.shared[0], nil); status == http.StatusOK {
		t.Error("after expiry a shared asset still opened")
	}
	if sess, location := issue2092Claim(t, s.invite); sess.cookie != nil || !strings.Contains(location, "error=link_invalid") {
		t.Errorf("the used invitation: cookie %v, location %q, want refused", sess.cookie, location)
	}
	admin := connect(t)
	before := len(mailpitMessages(t))
	status, _ := admin.rest(http.MethodPost, "/portal/auth/email-link", jsonBody(t, map[string]any{"email": s.email}))
	if status != http.StatusAccepted {
		t.Errorf("asking for a link answered %d, want 202", status)
	}
	if after := len(mailpitMessages(t)); after != before {
		t.Errorf("an expired account was sent a sign-in link")
	}
}

// A sign-in link asked for on the sign-in screen opens a session, once.
func TestIssue2092_ASignInLinkFromTheSignInScreenWorksOnce(t *testing.T) {
	s := newIssue2092(t, false)
	admin := connect(t)
	if status, _ := admin.rest(http.MethodPost, "/portal/auth/email-link", jsonBody(t, map[string]any{"email": s.email})); status != http.StatusAccepted {
		t.Fatalf("asking for a link answered %d", status)
	}
	link := issue2092MailLink(t, s.email, "your sign-in link")
	sess, _ := issue2092Claim(t, link)
	if sess.cookie == nil {
		t.Fatal("the sign-in link set no session")
	}
	if status, me := sess.do(t, http.MethodGet, "/api/v1/portal/me", nil); status != http.StatusOK || me["email"] != s.email {
		t.Errorf("/me: %d %v", status, me)
	}
	if again, location := issue2092Claim(t, link); again.cookie != nil || !strings.Contains(location, "error=link_invalid") {
		t.Errorf("a second use of the link: cookie %v, location %q, want refused", again.cookie, location)
	}
	// Feedback is off for this persona: no thread is offered.
	if status, _ := sess.do(t, http.MethodGet, "/api/v1/portal/threads", nil); status != http.StatusForbidden {
		t.Errorf("threads with feedback off answered %d, want 403", status)
	}
}

// An address that already signs in through the identity provider gets no
// external account: the person is internal.
func TestIssue2092_AnInternalAddressGetsNoExternalAccount(t *testing.T) {
	admin := connect(t)
	// A Keycloak sign-in records the person in the directory.
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	if status, _ := issue1759Request(t, token, http.MethodGet, issue1759MePath, nil); status != http.StatusOK {
		t.Fatalf("the Keycloak sign-in was not accepted: %d", status)
	}
	stamp := time.Now().UnixNano()
	name := fmt.Sprintf("acc-2092-int-%d", stamp)
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": name, "display_name": "Acceptance 2092", "roles": []any{strings.ReplaceAll(name, "-", "_")},
		"allow_tools": []any{"*"}, "external": true,
	})); status != http.StatusCreated {
		t.Fatalf("creating the external persona: %d %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody) })
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/external-users", jsonBody(t, map[string]any{
		"email": issue1759AnalystEmail, "name": "Analyst", "persona": name,
	}))
	if status != http.StatusConflict {
		if id, _ := out["id"].(string); id != "" {
			admin.rest(http.MethodDelete, "/api/v1/admin/external-users/"+id, http.NoBody)
		}
		t.Fatalf("an external account for an internal address: %d %v, want 409", status, out)
	}
}

// With feedback on, the external user comments on what is shared with them
// and sees only shared threads (#2227 makes the split).
func TestIssue2092_WithFeedbackOnTheExternalUserCommentsAndSeesOnlySharedThreads(t *testing.T) {
	s := newIssue2092(t, true)
	admin := connect(t)
	assetID := s.shared[0]
	_, internal := admin.rest(http.MethodPost, "/api/v1/portal/threads", jsonBody(t, map[string]any{
		"kind": "comment", "target_type": "asset", "asset_id": assetID, "body": "acc-2092 internal",
	}))
	internalID, _ := internal["id"].(string)
	status, mine := s.session.do(t, http.MethodPost, "/api/v1/portal/threads", map[string]any{
		"kind": "comment", "target_type": "asset", "asset_id": assetID, "body": "acc-2092 comment from the client",
	})
	if status != http.StatusCreated {
		t.Fatalf("the external user's comment: %d %v", status, mine)
	}
	_, list := s.session.do(t, http.MethodGet, "/api/v1/portal/threads?asset_id="+assetID, nil)
	raw, _ := json.Marshal(list)
	if !strings.Contains(string(raw), mine["id"].(string)) || strings.Contains(string(raw), internalID) {
		t.Errorf("the external user's threads %s: want their own and not the internal %s", raw, internalID)
	}
}

// issue2092IdPUser creates a person at the dev stack's Keycloak with the
// address and the analyst role, and returns their password; the person is
// deleted when the test ends.
func issue2092IdPUser(t *testing.T, email string) string {
	t.Helper()
	token := kcAdminToken2061(t)
	if status, out := kcAdmin2061(t, token, http.MethodPost, "/users", map[string]any{
		"username": email, "email": email, "emailVerified": true, "enabled": true,
		"firstName": "Acceptance", "lastName": "Client",
	}); status != http.StatusCreated {
		t.Fatalf("creating the Keycloak user: %d %s", status, out)
	}
	_, out := kcAdmin2061(t, token, http.MethodGet, "/users?exact=true&email="+url.QueryEscape(email), nil)
	var users []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &users); err != nil || len(users) != 1 {
		t.Fatalf("reading the Keycloak user back: %s", out)
	}
	id := users[0].ID
	t.Cleanup(func() { kcAdmin2061(t, kcAdminToken2061(t), http.MethodDelete, "/users/"+id, nil) })
	password := fmt.Sprintf("acc-2092-%d", time.Now().UnixNano())
	if status, out := kcAdmin2061(t, token, http.MethodPut, "/users/"+id+"/reset-password", map[string]any{
		"type": "password", "value": password, "temporary": false,
	}); status != http.StatusNoContent {
		t.Fatalf("setting the Keycloak user's password: %d %s", status, out)
	}
	_, role := kcAdmin2061(t, token, http.MethodGet, "/roles/dp_analyst", nil)
	var r map[string]any
	if err := json.Unmarshal(role, &r); err != nil {
		t.Fatalf("reading the dp_analyst role: %s", role)
	}
	if status, out := kcAdmin2061(t, token, http.MethodPost, "/users/"+id+"/role-mappings/realm", []any{r}); status != http.StatusNoContent {
		t.Fatalf("granting dp_analyst: %d %s", status, out)
	}
	return password
}

// The person's first sign-in through the identity provider converts the
// account: it reads converted, its session and links stop working, and what
// was shared with the address opens for the person, now internal.
func TestIssue2092_AnIdentityProviderSignInConvertsTheAccount(t *testing.T) {
	s := newIssue2092(t, false)
	admin := connect(t)
	password := issue2092IdPUser(t, s.email)

	browser := browserSignIn(t, baseURL(), s.email, password)

	_, list := admin.rest(http.MethodGet, "/api/v1/admin/external-users", http.NoBody)
	raw, _ := json.Marshal(list)
	var accounts struct {
		Users []map[string]any `json:"users"`
	}
	_ = json.Unmarshal(raw, &accounts)
	var account map[string]any
	for _, u := range accounts.Users {
		if u["id"] == s.accountID {
			account = u
		}
	}
	if account["status"] != "converted" || account["converted_at"] == nil {
		t.Fatalf("the account after an identity-provider sign-in: %v, want converted with its date", account)
	}

	if status, _ := s.session.do(t, http.MethodGet, "/api/v1/portal/assets/"+s.shared[0], nil); status == http.StatusOK {
		t.Error("the external session still opens a shared item after the conversion")
	}
	if session, location := issue2092Claim(t, s.invite); session.cookie != nil {
		t.Errorf("the invitation link signed in again after the conversion (redirect %q)", location)
	}

	res, err := browser.Get(baseURL() + "/api/v1/portal/assets/" + s.shared[0]) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("the converted person opening an item shared with their address: %d", res.StatusCode)
	}
	res, err = browser.Get(baseURL() + "/api/v1/portal/me") // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	var me map[string]any
	_ = json.NewDecoder(res.Body).Decode(&me)
	_ = res.Body.Close()
	if portal, _ := me["portal"].(map[string]any); portal["external"] == true {
		t.Errorf("the converted person is still external: %v", portal)
	}
}
