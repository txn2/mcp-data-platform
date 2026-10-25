//go:build integration

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #2290: an external user is an account in the identity provider whose
// role resolves to a persona marked external. Two client personas, A and B,
// each with two people who sign in through the dev stack's Keycloak by role.
// A share to a persona reaches its people and nobody else's; staff see the
// people marked external; an external person opens only what is shared with
// them, comments on it, changes their notification settings, uploads only to
// their own folder and reaches no other route or tool; earlier versions follow
// the persona setting; and External files decides who reaches their uploads.
//
// Wire forms: the persona bodies as JSON objects with external as the boolean
// true and absent, portal as a JSON object with history as the boolean true
// and false and absent, external_files as the strings "view" and "manage" and
// absent, and uploads as the boolean true; the share bodies with
// shared_with_persona and shared_with_email strings and permission "editor";
// the thread body as a JSON object; the notification body's mode as the
// string "daily"; the upload as a multipart form with the file part last;
// the external people's requests as a Keycloak browser session with its CSRF
// token, and their MCP session with the identity provider's id_token as the
// bearer.

const issue2290Password = "acc-2290-password"

// externalSession is a person signed in to the portal through the identity
// provider, as a browser is: the session cookie and the CSRF token /me issued.
type externalSession struct {
	hc      *http.Client
	csrf    string
	email   string
	userID  string
	idToken string
}

// do sends one request as the session.
func (s externalSession) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = jsonBody(t, body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, baseURL()+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.csrf != "" && method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", s.csrf)
	}
	return issue2290Send(t, s.hc, req)
}

func issue2290Send(t *testing.T, hc *http.Client, req *http.Request) (int, map[string]any) {
	t.Helper()
	res, err := hc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// persona2290 is a persona the test created and the Keycloak role that
// resolves to it.
type persona2290 struct {
	name, role string
}

// newPersona2290 creates a persona with extra fields on its body, and the
// realm role at Keycloak its people are given.
func newPersona2290(t *testing.T, admin *client, label string, extra map[string]any) persona2290 {
	t.Helper()
	stamp := time.Now().UnixNano()
	p := persona2290{name: fmt.Sprintf("acc-2290-%s-%d", label, stamp)}
	p.role = "dp_" + strings.ReplaceAll(p.name, "-", "_")
	body := map[string]any{
		"name": p.name, "display_name": "Acceptance " + label, "roles": []any{p.role},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
	}
	for k, v := range extra {
		body[k] = v
	}
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, body)); status != http.StatusCreated {
		t.Fatalf("creating persona %s: %d %v", p.name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+p.name+"?confirm=true", http.NoBody) })
	token := kcAdminToken2061(t)
	if status, out := kcAdmin2061(t, token, http.MethodPost, "/roles", map[string]any{"name": p.role}); status != http.StatusCreated {
		t.Fatalf("creating the Keycloak role %s: %d %s", p.role, status, out)
	}
	t.Cleanup(func() { kcAdmin2061(t, kcAdminToken2061(t), http.MethodDelete, "/roles/"+p.role, nil) })
	return p
}

// kcPerson2290 creates a Keycloak user holding the realm role and returns
// their address; the user is deleted when the test ends.
func kcPerson2290(t *testing.T, role, label string) string {
	t.Helper()
	email := fmt.Sprintf("acc-2290-%s-%d@client.example.org", label, time.Now().UnixNano())
	token := kcAdminToken2061(t)
	if status, out := kcAdmin2061(t, token, http.MethodPost, "/users", map[string]any{
		"username": email, "email": email, "enabled": true, "emailVerified": true,
		"firstName": "Acceptance", "lastName": label,
		"credentials": []any{map[string]any{"type": "password", "value": issue2290Password, "temporary": false}},
	}); status != http.StatusCreated {
		t.Fatalf("creating the Keycloak user %s: %d %s", email, status, out)
	}
	_, out := kcAdmin2061(t, token, http.MethodGet, "/users?exact=true&username="+email, nil)
	var users []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &users); err != nil || len(users) != 1 {
		t.Fatalf("finding the Keycloak user %s: %s", email, out)
	}
	userID := users[0].ID
	t.Cleanup(func() { kcAdmin2061(t, kcAdminToken2061(t), http.MethodDelete, "/users/"+userID, nil) })
	_, roleRaw := kcAdmin2061(t, token, http.MethodGet, "/roles/"+role, nil)
	var rep map[string]any
	if err := json.Unmarshal(roleRaw, &rep); err != nil {
		t.Fatalf("reading the Keycloak role %s: %s", role, roleRaw)
	}
	if status, out := kcAdmin2061(t, token, http.MethodPost, "/users/"+userID+"/role-mappings/realm", []any{rep}); status != http.StatusNoContent {
		t.Fatalf("giving %s the role %s: %d %s", email, role, status, out)
	}
	return email
}

// signIn2290 signs the person in through the portal's identity-provider
// sign-in, as a browser does, and through the token endpoint for the id_token
// an MCP client presents.
func signIn2290(t *testing.T, email string) externalSession {
	t.Helper()
	s := externalSession{hc: browserSignIn(t, baseURL(), email, issue2290Password), email: email}
	_, me := s.do(t, http.MethodGet, "/api/v1/portal/me", nil)
	s.csrf, _ = me["csrf_token"].(string)
	s.userID, _ = me["user_id"].(string)
	s.idToken = issue1759SignIn(t, email, issue2290Password)
	return s
}

// newPerson2290 is a person holding the persona's role, signed in, once the
// directory records the roles the sign-in stated.
func newPerson2290(t *testing.T, admin *client, p persona2290, label string) externalSession {
	t.Helper()
	email := kcPerson2290(t, p.role, label)
	s := signIn2290(t, email)
	if _, me := s.do(t, http.MethodGet, "/api/v1/portal/me", nil); me["persona"] != p.name {
		t.Fatalf("%s signed in as %v, want the persona %s", email, me["persona"], p.name)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/users/"+email, http.NoBody) })
	issue2290Recorded(t, admin, email, p.name)
	return s
}

// issue2290Recorded waits for the directory to hold the person with the
// persona their recorded roles resolve to. A sign-in records the person in
// the background, so the row arrives a moment after the session does.
func issue2290Recorded(t *testing.T, admin *client, email, persona string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, u := admin.rest(http.MethodGet, "/api/v1/admin/users/"+email, http.NoBody)
		if u["persona"] == persona {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the directory never recorded %s under %s: %v", email, persona, u)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// issue2290Clients is the two client personas and their people.
type issue2290Clients struct {
	a, b           persona2290
	a1, a2, b1, b2 externalSession
}

func newIssue2290Clients(t *testing.T, admin *client, portal map[string]any) issue2290Clients {
	t.Helper()
	extra := map[string]any{"external": true}
	if portal != nil {
		extra["portal"] = portal
	}
	c := issue2290Clients{a: newPersona2290(t, admin, "client-a", extra), b: newPersona2290(t, admin, "client-b", extra)}
	c.a1, c.a2 = newPerson2290(t, admin, c.a, "a1"), newPerson2290(t, admin, c.a, "a2")
	c.b1, c.b2 = newPerson2290(t, admin, c.b, "b1"), newPerson2290(t, admin, c.b, "b2")
	return c
}

// issue2290Asset saves an asset as the administrator and returns its id.
func issue2290Asset(t *testing.T, admin *client, label string) string {
	t.Helper()
	saved := admin.call("save_asset", map[string]any{
		"name": fmt.Sprintf("acc-2290 %s %d", label, time.Now().UnixNano()), "content_type": "text/html",
		"content": "<p>acc-2290 " + label + "</p>", "description": "Acceptance #2290.",
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("saving the asset: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id
}

func issue2290Share(t *testing.T, admin *client, assetID string, body map[string]any) {
	t.Helper()
	if status, out := admin.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares", jsonBody(t, body)); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("sharing %s with %v: %d %v", assetID, body, status, out)
	}
}

// An asset shared with persona A opens for both of A's people and neither of
// B's; an asset shared with one of A's people opens for that person alone.
func TestIssue2290_APersonaShareReachesItsPeopleOnly(t *testing.T) {
	admin := connect(t)
	c := newIssue2290Clients(t, admin, nil)
	toA := issue2290Asset(t, admin, "to persona A")
	issue2290Share(t, admin, toA, map[string]any{"shared_with_persona": c.a.name, "permission": "editor"})
	toA1 := issue2290Asset(t, admin, "to A1")
	issue2290Share(t, admin, toA1, map[string]any{"shared_with_email": c.a1.email, "permission": "editor"})

	for _, tc := range []struct {
		who       string
		s         externalSession
		toA, toA1 bool
	}{
		{"A1", c.a1, true, true}, {"A2", c.a2, true, false}, {"B1", c.b1, false, false}, {"B2", c.b2, false, false},
	} {
		for id, want := range map[string]bool{toA: tc.toA, toA1: tc.toA1} {
			status, asset := tc.s.do(t, http.MethodGet, "/api/v1/portal/assets/"+id, nil)
			if (status == http.StatusOK) != want {
				t.Errorf("%s opening %s: %d, want opened=%v", tc.who, id, status, want)
			}
			if want && asset["share_permission"] != "viewer" {
				t.Errorf("%s holds %v on %s, want viewer although the share grants editor", tc.who, asset["share_permission"], id)
			}
		}
	}
}

// An internal person sharing an asset sees A's and B's people marked
// external in the person picker, and the personas marked in the persona list.
func TestIssue2290_AnInternalPersonSeesExternalPeopleMarked(t *testing.T) {
	admin := connect(t)
	c := newIssue2290Clients(t, admin, nil)
	staff := newIssue2241Member(t, admin, "2290-staff", nil)
	for _, s := range []externalSession{c.a1, c.a2, c.b1, c.b2} {
		status, out := staff.client.rest(http.MethodGet, "/api/v1/portal/users?q="+s.email, http.NoBody)
		users, _ := out["users"].([]any)
		if status != http.StatusOK || len(users) != 1 {
			t.Fatalf("the picker finding %s: %d %v", s.email, status, out)
		}
		if u, _ := users[0].(map[string]any); u["external"] != true {
			t.Errorf("the picker shows %s unmarked: %v", s.email, u)
		}
	}
	_, out := staff.client.rest(http.MethodGet, "/api/v1/portal/users?q="+devOwnerEmail, http.NoBody)
	if users, _ := out["users"].([]any); len(users) > 0 {
		if u, _ := users[0].(map[string]any); u["external"] == true {
			t.Errorf("an internal person is marked external: %v", u)
		}
	}
	_, personas := staff.client.rest(http.MethodGet, "/api/v1/portal/share-personas", http.NoBody)
	marked := map[string]bool{}
	list, _ := personas["personas"].([]any)
	for _, p := range list {
		m, _ := p.(map[string]any)
		name, _ := m["name"].(string)
		marked[name] = m["external"] == true
	}
	if !marked[c.a.name] || !marked[c.b.name] || marked[staff.persona] {
		t.Errorf("the persona list marks %v, want %s and %s external and %s not", marked, c.a.name, c.b.name, staff.persona)
	}

	// The staff member shares with persona A, which is not their own.
	asset := staff.client.call("save_asset", map[string]any{
		"name": fmt.Sprintf("acc-2290 staff %d", time.Now().UnixNano()), "content_type": "text/html",
		"content": "<p>staff</p>", "description": "Acceptance #2290.",
	})
	assetID, _ := asset["asset_id"].(string)
	t.Cleanup(func() {
		_, _, _ = staff.client.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})
	if status, out := staff.client.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares", jsonBody(t, map[string]any{
		"shared_with_persona": c.a.name,
	})); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("an internal person sharing with persona A: %d %v", status, out)
	}
	if status, _ := c.a2.do(t, http.MethodGet, "/api/v1/portal/assets/"+assetID, nil); status != http.StatusOK {
		t.Errorf("A2 opening what staff shared with persona A: %d", status)
	}
}

// Admin > Users marks the person as external with their persona.
func TestIssue2290_AdminUsersMarksExternalPeople(t *testing.T) {
	admin := connect(t)
	a := newPersona2290(t, admin, "client-users", map[string]any{"external": true})
	s := newPerson2290(t, admin, a, "u1")
	status, out := admin.rest(http.MethodGet, "/api/v1/admin/users?q="+s.email, http.NoBody)
	users, _ := out["users"].([]any)
	if status != http.StatusOK || len(users) != 1 {
		t.Fatalf("Admin > Users finding %s: %d %v", s.email, status, out)
	}
	if u, _ := users[0].(map[string]any); u["external"] != true || u["persona"] != a.name {
		t.Errorf("Admin > Users shows %v, want persona %s and external", u, a.name)
	}
}

// An external user opens exactly the assets shared with them or their
// persona, comments on one, and changes their notification preferences.
func TestIssue2290_AnExternalUserOpensWhatIsSharedCommentsAndSetsPrefs(t *testing.T) {
	admin := connect(t)
	a := newPersona2290(t, admin, "client-open", map[string]any{"external": true, "portal": map[string]any{"feedback": true}})
	s := newPerson2290(t, admin, a, "o1")
	mine := issue2290Asset(t, admin, "to the person")
	issue2290Share(t, admin, mine, map[string]any{"shared_with_email": s.email})
	theirs := issue2290Asset(t, admin, "to the persona")
	issue2290Share(t, admin, theirs, map[string]any{"shared_with_persona": a.name})
	unshared := issue2290Asset(t, admin, "unshared")

	_, list := s.do(t, http.MethodGet, "/api/v1/portal/shared-with-me", nil)
	text := stringOf(list)
	if !strings.Contains(text, mine) || !strings.Contains(text, theirs) || strings.Contains(text, unshared) {
		t.Errorf("shared-with-me holds person %v, persona %v, unshared %v: %s",
			strings.Contains(text, mine), strings.Contains(text, theirs), strings.Contains(text, unshared), text)
	}
	if status, _ := s.do(t, http.MethodGet, "/api/v1/portal/assets/"+unshared, nil); status == http.StatusOK {
		t.Error("an asset shared with nobody opened")
	}

	status, thread := s.do(t, http.MethodPost, "/api/v1/portal/threads", map[string]any{
		"kind": "comment", "target_type": "asset", "asset_id": theirs, "body": "acc-2290 comment from the client",
	})
	if status != http.StatusCreated {
		t.Fatalf("the external user's comment: %d %v", status, thread)
	}
	if thread["visibility"] != "shared" {
		t.Errorf("the comment's thread is %v, want shared", thread["visibility"])
	}

	if status, out := s.do(t, http.MethodPut, "/api/v1/portal/notification-prefs", map[string]any{"mode": "daily"}); status != http.StatusOK {
		t.Fatalf("changing the notification preferences: %d %v", status, out)
	}
	if _, prefs := s.do(t, http.MethodGet, "/api/v1/portal/notification-prefs", nil); prefs["mode"] != "daily" {
		t.Errorf("the preferences read back %v, want mode daily", prefs)
	}
}

// Every other portal route and every MCP tool refuses an external user.
func TestIssue2290_EverythingElseRefusesAnExternalUser(t *testing.T) {
	admin := connect(t)
	a := newPersona2290(t, admin, "client-refuse", map[string]any{"external": true})
	s := newPerson2290(t, admin, a, "r1")
	for _, path := range []string{
		"/api/v1/portal/users", "/api/v1/portal/mention-candidates?target_type=asset&target_id=x",
		"/api/v1/portal/api-keys", "/api/v1/portal/knowledge-pages", "/api/v1/portal/search?q=acc",
		"/api/v1/portal/prompts", "/api/v1/portal/scripts", "/api/v1/portal/calls", "/api/v1/portal/sessions",
		"/api/v1/portal/activity/overview", "/api/v1/portal/access-requests", "/api/v1/portal/share-personas",
		"/api/v1/portal/channels", "/api/v1/portal/memory/records", "/api/v1/portal/catalog/datasets",
		"/api/v1/apis", "/api/v1/tables",
	} {
		if status, out := s.do(t, http.MethodGet, path, nil); status != http.StatusForbidden {
			t.Errorf("GET %s: %d %v, want 403", path, status, out)
		}
	}
	if status, _ := s.do(t, http.MethodGet, "/api/v1/admin/system/info", nil); status == http.StatusOK {
		t.Error("an admin route answered the external user")
	}

	// The MCP endpoint with the identity provider's token lists no tool and
	// calls none.
	ctx, cancel := context.WithTimeout(t.Context(), sessionTimeout)
	defer cancel()
	hc := &http.Client{Transport: authRoundTripper{key: s.idToken, base: http.DefaultTransport}}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "acceptance-2290", Version: "1.0.0"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: baseURL(), HTTPClient: hc}, nil)
	if err != nil {
		t.Fatalf("opening an MCP session as the external user: %v", err)
	}
	defer func() { _ = session.Close() }()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools.Tools) != 0 {
		names := make([]string, 0, len(tools.Tools))
		for _, tl := range tools.Tools {
			names = append(names, tl.Name)
		}
		t.Errorf("tools/list gave the external user %v, want none", names)
	}
	for _, name := range []string{"platform_info", "search", "fetch"} {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{"intent": "x", "ref": "x"}})
		if err == nil && !res.IsError {
			t.Errorf("tools/call %s answered the external user", name)
		}
	}
}

// With earlier versions off, the asset shows no history and the version
// routes answer 403; with it on, the person sees the history.
func TestIssue2290_EarlierVersionsFollowThePersonaSetting(t *testing.T) {
	admin := connect(t)
	a := newPersona2290(t, admin, "client-history", map[string]any{"external": true})
	s := newPerson2290(t, admin, a, "h1")
	id := issue2290Asset(t, admin, "versioned")
	if res, _, err := admin.callRaw("manage_asset", map[string]any{"action": "update", "asset_id": id, "content": "<p>acc-2290 second</p>"}); err != nil || res.IsError {
		t.Fatalf("writing a second version: %v %v", err, res)
	}
	issue2290Share(t, admin, id, map[string]any{"shared_with_persona": a.name, "permission": "editor"})

	paths := []string{"/api/v1/portal/assets/" + id + "/versions", "/api/v1/portal/assets/" + id + "/versions/1/content"}
	for _, p := range paths {
		if status, _ := s.do(t, http.MethodGet, p, nil); status != http.StatusForbidden {
			t.Errorf("with earlier versions unset, GET %s: %d, want 403", p, status)
		}
	}
	setHistory := func(on bool) {
		body := map[string]any{
			"display_name": "Acceptance client-history", "roles": []any{a.role},
			"allow_tools": []any{"*"}, "allow_connections": []any{"*"}, "external": true,
			"portal": map[string]any{"history": on},
		}
		if status, out := admin.rest(http.MethodPut, "/api/v1/admin/personas/"+a.name, jsonBody(t, body)); status != http.StatusOK {
			t.Fatalf("setting history %v: %d %v", on, status, out)
		}
	}
	setHistory(true)
	status, versions := s.do(t, http.MethodGet, paths[0], nil)
	if status != http.StatusOK || !strings.Contains(stringOf(versions), "version") {
		t.Errorf("with earlier versions on, the history: %d %v", status, versions)
	}
	if status, _ := s.do(t, http.MethodGet, paths[1], nil); status != http.StatusOK {
		t.Errorf("with earlier versions on, version 1's content: %d", status)
	}
	setHistory(false)
	if status, _ := s.do(t, http.MethodGet, paths[0], nil); status != http.StatusForbidden {
		t.Errorf("with earlier versions off, the history: %d, want 403", status)
	}
}

// issue2290Upload uploads a CSV as the session to the library named.
func issue2290Upload(t *testing.T, s externalSession, scope, scopeID string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	name := fmt.Sprintf("acceptance-2290-%d", time.Now().UnixNano())
	for k, v := range map[string]string{
		"scope": scope, "scope_id": scopeID, "path": "deliveries", "display_name": name, "description": "Acceptance #2290: a delivery.",
	} {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := filePart1631(w, name+".csv", "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("sku,units\nA1,3\nB2,5\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL()+"/api/v1/resources", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-CSRF-Token", s.csrf)
	return issue2290Send(t, s.hc, req)
}

// An external user's upload lands in their own folder and an upload to any
// other folder is refused. A person whose persona has External files at view
// lists and opens it and cannot change it; at manage they delete it; at none
// they do not see it.
func TestIssue2290_UploadsAndExternalFiles(t *testing.T) {
	admin := connect(t)
	a := newPersona2290(t, admin, "client-upload", map[string]any{"external": true, "portal": map[string]any{"uploads": true}})
	s := newPerson2290(t, admin, a, "up1")

	for _, target := range [][2]string{{"persona", a.name}, {"global", ""}} {
		if status, out := issue2290Upload(t, s, target[0], target[1]); status != http.StatusForbidden {
			t.Errorf("an upload to %s %q: %d %v, want 403", target[0], target[1], status, out)
		}
	}
	status, up := issue2290Upload(t, s, "user", s.userID)
	id, _ := up["id"].(string)
	if status != http.StatusCreated || id == "" {
		t.Fatalf("the upload to their own folder: %d %v", status, up)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	if up["scope"] != "user" || up["scope_id"] != s.userID {
		t.Errorf("the upload landed in %v %v, want their own folder %s", up["scope"], up["scope_id"], s.userID)
	}
	if _, shares := admin.rest(http.MethodGet, "/api/v1/resources/"+id+"/shares", http.NoBody); strings.Contains(stringOf(shares), "shared_with") {
		t.Errorf("the upload was shared on arrival: %v", shares)
	}

	viewer := newIssue2241Member(t, admin, "2290-view", map[string]any{"portal": map[string]any{"external_files": "view"}})
	manager := newIssue2241Member(t, admin, "2290-manage", map[string]any{"portal": map[string]any{"external_files": "manage"}})
	nobody := newIssue2241Member(t, admin, "2290-none", nil)

	status, folders := viewer.client.rest(http.MethodGet, "/api/v1/resources/external-folders", http.NoBody)
	if status != http.StatusOK || !strings.Contains(stringOf(folders), s.email) || !strings.Contains(stringOf(folders), s.userID) {
		t.Fatalf("View lists the external folders: %d %v, want %s's", status, folders, s.email)
	}
	status, listing := viewer.client.rest(http.MethodGet, "/api/v1/resources?scope=user&scope_id="+s.userID, http.NoBody)
	if status != http.StatusOK || !strings.Contains(stringOf(listing), id) {
		t.Errorf("View lists the folder's files: %d %v", status, listing)
	}
	if status, _ := rawGet(t, baseURL(), "/api/v1/resources/"+id+"/content", viewer.key); status != http.StatusOK {
		t.Errorf("View opens the file: %d", status)
	}
	if status, out := viewer.client.rest(http.MethodPatch, "/api/v1/resources/"+id, jsonBody(t, map[string]any{"display_name": "changed"})); status != http.StatusForbidden {
		t.Errorf("View renaming the file: %d %v, want 403", status, out)
	}
	if status, _ := viewer.client.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody); status != http.StatusForbidden {
		t.Errorf("View deleting the file: %d, want 403", status)
	}

	if status, _ := nobody.client.rest(http.MethodGet, "/api/v1/resources/external-folders", http.NoBody); status != http.StatusForbidden {
		t.Errorf("None listing the external folders: %d, want 403", status)
	}
	if status, _ := rawGet(t, baseURL(), "/api/v1/resources/"+id, nobody.key); status != http.StatusNotFound {
		t.Errorf("None opening the file: %d, want 404", status)
	}
	if _, listing := nobody.client.rest(http.MethodGet, "/api/v1/resources?scope=user&scope_id="+s.userID, http.NoBody); strings.Contains(stringOf(listing), id) {
		t.Errorf("None listing the folder sees the file: %v", listing)
	}

	if status, out := manager.client.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("Manage deleting the file: %d %v", status, out)
	}
	if status, _ := rawGet(t, baseURL(), "/api/v1/resources/"+id, viewer.key); status != http.StatusNotFound {
		t.Errorf("the file Manage deleted still opens: %d", status)
	}
}
