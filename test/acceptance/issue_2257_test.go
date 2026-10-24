//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq" // the dev stack's database, for the rows written directly
)

// Issue #2257: the persona editor grouped by surface, and the access a persona
// decides that the page did not show or the server did not enforce. The
// administrator persona is never external, scratch tables and the PDF routes
// follow the portal levels, an approval needs sharing, and
// POST /api/v1/admin/personas/effective-access reports what a persona reaches,
// including the grants other objects hold that name it.
//
// Wire forms: the persona bodies as JSON objects with external as the boolean
// true and false and absent, portal as a JSON object with string levels
// (scratch_tables, resources, knowledge) and boolean switches (download),
// allow_sharing as the boolean false; the effective-access body as the persona
// create body, with allow_tools and allow_connections as JSON arrays of
// strings; the approval body's permission as the string "viewer"; the delete's
// confirm as the query parameter "true" and absent.

// issue2257DB opens the dev stack's database.
func issue2257DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// issue2257Access posts a persona body to effective-access as the administrator.
func issue2257Access(t *testing.T, admin *client, body map[string]any) map[string]any {
	t.Helper()
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas/effective-access", jsonBody(t, body))
	if status != http.StatusOK {
		t.Fatalf("effective-access: %d %v", status, out)
	}
	return out
}

// issue2257Num reads a number at a path of nested objects.
func issue2257Num(t *testing.T, m map[string]any, keys ...string) float64 {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("no %s in %v", strings.Join(keys, "."), m)
		}
		cur = obj[k]
	}
	n, ok := cur.(float64)
	if !ok {
		t.Fatalf("%s is %v, not a number", strings.Join(keys, "."), cur)
	}
	return n
}

// issue2257ExternalAccount creates an external persona with the portal block
// given and an account under it, and signs the person in.
func issue2257ExternalAccount(t *testing.T, admin *client, label string, extra map[string]any) (persona, email string, s issue2092Session) {
	t.Helper()
	stamp := time.Now().UnixNano()
	persona = fmt.Sprintf("acc-%s-%d", label, stamp)
	email = fmt.Sprintf("acc-%s-%d@example.org", label, stamp)
	body := map[string]any{
		"name": persona, "display_name": "Acceptance " + label, "roles": []any{strings.ReplaceAll(persona, "-", "_")},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"}, "external": true,
	}
	for k, v := range extra {
		body[k] = v
	}
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, body)); status != http.StatusCreated {
		t.Fatalf("creating the external persona: %d %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+persona+"?confirm=true", http.NoBody) })
	status, acct := admin.rest(http.MethodPost, "/api/v1/admin/external-users", jsonBody(t, map[string]any{
		"email": email, "name": "Acceptance " + label, "persona": persona,
	}))
	if status != http.StatusCreated {
		t.Fatalf("creating the external user: %d %v", status, acct)
	}
	id, _ := acct["id"].(string)
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/external-users/"+id, http.NoBody) })
	s, location := issue2092Claim(t, issue2092MailLink(t, email, "You have been invited"))
	if s.cookie == nil {
		t.Fatalf("the invitation set no session; it redirected to %q", location)
	}
	return persona, email, s
}

// issue2257Upload uploads a small CSV as c to the library named.
func issue2257Upload(t *testing.T, c *client, scope, scopeID, label string) string {
	t.Helper()
	name := fmt.Sprintf("acceptance-2257-%s-%d", label, time.Now().UnixNano())
	if scope == "user" && scopeID == "" {
		// The caller's own folder is keyed by their own id.
		_, me := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
		scopeID, _ = me["user_id"].(string)
	}
	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		fields := map[string]string{
			"scope": scope, "path": "acceptance-2257", "display_name": name,
			"description": "Acceptance #2257.",
		}
		if scopeID != "" {
			fields["scope_id"] = scopeID
		}
		for k, v := range fields {
			if err := w.WriteField(k, v); err != nil {
				return err
			}
		}
		part, err := filePart1631(w, name+".csv", "text/csv")
		if err != nil {
			return err
		}
		if _, err := part.Write([]byte("region,total\nwest,1\neast,2\n")); err != nil {
			return err
		}
		return w.Close()
	})
	id, _ := body["id"].(string)
	if status != http.StatusCreated || id == "" {
		t.Fatalf("uploading %s: %d %v", name, status, body)
	}
	admin := connect(t)
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return id
}

// The administrator persona is never external: the admin API refuses saving
// it so, and a configuration file that marks it so is refused at startup.
func TestIssue2257_TheAdministratorPersonaIsNeverExternal(t *testing.T) {
	admin := connect(t)
	status, out := admin.rest(http.MethodPut, "/api/v1/admin/personas/admin", jsonBody(t, map[string]any{
		"display_name": "Administrator", "roles": []any{"admin", "dp_admin"},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"}, "external": true,
	}))
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["detail"]), "cannot be external") {
		t.Fatalf("saving the admin persona external: %d %v, want 400", status, out)
	}

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "platform.yaml")
	if err := os.WriteFile(cfg, []byte(`server:
  name: acceptance-2257
  transport: stdio
admin:
  persona: admin
personas:
  admin:
    display_name: Administrator
    roles: [admin]
    external: true
    tools:
      allow: ["*"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "mcp-data-platform")
	build := exec.Command("go", "build", "-o", bin, "./cmd/mcp-data-platform") //nolint:gosec // the test builds the product it starts
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the platform: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	run := exec.CommandContext(ctx, bin, "--config", cfg) //nolint:gosec // the binary just built, with a fixture config
	run.Stdin = strings.NewReader("")
	out2, err := run.CombinedOutput()
	if err == nil || !strings.Contains(string(out2), "the administrator persona cannot be external") {
		t.Fatalf("starting with an external administrator persona: err %v, output:\n%s", err, out2)
	}
}

// An administrator persona marked external in the store directly administers
// nothing: an external account under it is refused every admin route, and a
// key whose roles resolve to it is refused the admin API and has no secret
// filled.
func TestIssue2257_AnExternalAdministratorInTheStoreAdministersNothing(t *testing.T) {
	admin := connect(t)
	db := issue2257DB(t)
	secret, _ := secret2051(t, admin, []string{"acc-2257-nobody"}, fixture2051)
	keyName := fmt.Sprintf("acc-2257-admin-%d", time.Now().UnixNano())
	status, created := admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": keyName, "roles": []any{"admin"}, "description": "Acceptance #2257",
	}))
	key, _ := created["key"].(string)
	if (status != http.StatusCreated && status != http.StatusOK) || key == "" {
		t.Fatalf("creating the key: %d %v", status, created)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+keyName, http.NoBody) })
	// The session is opened while the key still resolves to the
	// administrator: opening one calls platform_info.
	holder := connectAs(t, key)
	email := fmt.Sprintf("acc-2257-ext-%d@example.org", time.Now().UnixNano())

	// The row is written as a store would hold it, then announced the way a
	// peer replica announces a persona change; both replicas re-read it.
	var existed bool
	if err := db.QueryRowContext(t.Context(), `SELECT EXISTS (SELECT 1 FROM persona_definitions WHERE name = 'admin')`).Scan(&existed); err != nil {
		t.Fatal(err)
	}
	announce := func() {
		if _, err := db.ExecContext(context.Background(), `SELECT pg_notify('mcp_notifications_reload',
			'{"method":"platform/reload/persona","params":{"origin":"acceptance-2257"}}')`); err != nil {
			t.Errorf("announcing the persona change: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = db.ExecContext(ctx, `DELETE FROM external_accounts WHERE email = $1`, email)
		if existed {
			_, _ = db.ExecContext(ctx, `UPDATE persona_definitions SET external = FALSE WHERE name = 'admin'`)
		} else {
			_, _ = db.ExecContext(ctx, `DELETE FROM persona_definitions WHERE name = 'admin'`)
		}
		announce()
		// The next criterion in this group runs as the administrator, so the
		// cleanup ends only once the key reads the admin API again, four times
		// running so that both replicas are seen to have re-read the persona.
		deadline := time.Now().Add(30 * time.Second)
		for ok := 0; ok < 4; {
			if status, _ := holder.rest(http.MethodGet, "/api/v1/admin/personas", http.NoBody); status == http.StatusOK {
				ok++
				continue
			}
			ok = 0
			if time.Now().After(deadline) {
				t.Errorf("the administrator persona was not restored on every replica within 30s")
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	})
	if _, err := db.ExecContext(t.Context(), `INSERT INTO persona_definitions
		(name, display_name, roles, tools_allow, connections_allow, priority, external)
		VALUES ('admin', 'Administrator', '["admin","dp_admin"]', '["*"]', '["*"]', 100, TRUE)
		ON CONFLICT (name) DO UPDATE SET external = TRUE`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO external_accounts (id, email, name, persona)
		VALUES ($1, $2, 'Acceptance 2257', 'admin')`, "acc2257-"+unique1579(), email); err != nil {
		t.Fatal(err)
	}
	announce()

	deadline := time.Now().Add(20 * time.Second)
	for {
		status, _ := holder.rest(http.MethodGet, "/api/v1/admin/personas", http.NoBody)
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a key resolving to the external administrator persona still reads the admin API (%d)", status)
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Every replica answers the same: a few reads land on both.
	for range 4 {
		if status, _ := holder.rest(http.MethodGet, "/api/v1/admin/system/info", http.NoBody); status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Errorf("the admin API answered the key %d", status)
		}
	}
	res, text, err := holder.callRaw("api_invoke_endpoint", map[string]any{
		"connection": fixture2051, "method": http.MethodPost, "path": "/v1/echo",
		"body":    map[string]any{"text": "{{secret:" + secret + "}}"},
		"purpose": "Acceptance #2257: an external administrator persona fills no secret.",
	})
	if err == nil && (res == nil || !res.IsError || strings.Contains(text, "[REDACTED:"+secret+"]")) {
		t.Errorf("the call was answered, filling the secret: %s", text)
	}

	if status, _ := holder.rest(http.MethodPost, "/portal/auth/email-link", jsonBody(t, map[string]any{"email": email})); status != http.StatusAccepted {
		t.Fatalf("asking for a sign-in link answered %d", status)
	}
	sess, location := issue2092Claim(t, issue2092MailLink(t, email, "your sign-in link"))
	if sess.cookie == nil {
		t.Fatalf("the external account under the administrator persona got no session; redirected to %q", location)
	}
	for _, path := range []string{"/api/v1/admin/personas", "/api/v1/admin/system/info", "/api/v1/admin/secrets", "/api/v1/admin/external-users"} {
		if status, _ := sess.do(t, http.MethodGet, path, nil); status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Errorf("GET %s as the external account answered %d", path, status)
		}
	}
}

// An external persona whose rules allow every tool and connection calls no
// tool, no operation and no scratch table.
func TestIssue2257_AnExternalPersonaReachesNothingThroughItsRules(t *testing.T) {
	admin := connect(t)
	out := issue2257Access(t, admin, map[string]any{
		"name": "acc-2257-preview", "display_name": "Preview", "roles": []any{"acc_2257_preview"},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"}, "external": true,
	})
	if n := issue2257Num(t, out, "tools", "callable"); n != 0 {
		t.Errorf("tools callable = %v, want 0", n)
	}
	if n := issue2257Num(t, out, "tools", "total"); n == 0 {
		t.Errorf("the deployment reported no tools: %v", out["tools"])
	}
	if n := issue2257Num(t, out, "api_operations", "callable"); n != 0 {
		t.Errorf("operations callable = %v, want 0", n)
	}
	if scratch, ok := out["scratch"].([]any); !ok || len(scratch) != 0 {
		t.Errorf("scratch = %v, want []", out["scratch"])
	}
	if out["account_type"] != "external" {
		t.Errorf("account_type = %v", out["account_type"])
	}
}

// A person of an external persona whose connections allow the scratch
// connection is refused every scratch-table route, including registering over
// a file scoped to their own address.
func TestIssue2257_ScratchTablesRefuseAnExternalPerson(t *testing.T) {
	admin := connect(t)
	_, email, s := issue2257ExternalAccount(t, admin, "2257-ext", nil)
	fileID := issue2257Upload(t, admin, "user", email, "ext")
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/tables"},
		{http.MethodGet, "/api/v1/table-connections"},
		{http.MethodPost, "/api/v1/resources/" + fileID + "/tables"},
	} {
		var body any
		if req.method == http.MethodPost {
			body = map[string]any{"connection": "acme-scratch-resources"}
		}
		if status, out := s.do(t, req.method, req.path, body); status != http.StatusForbidden {
			t.Errorf("%s %s as the external person: %d %v, want 403", req.method, req.path, status, out)
		}
	}
}

// A persona that can run trino_execute on a scratch connection is reported
// able to drop any registered table through SQL.
func TestIssue2257_TrinoExecuteOnAScratchConnectionCanDropAnyTable(t *testing.T) {
	admin := connect(t)
	out := issue2257Access(t, admin, map[string]any{
		"name": "acc-2257-sql", "display_name": "SQL", "roles": []any{"acc_2257_sql"},
		"allow_tools": []any{"trino_execute", "trino_query"}, "allow_connections": []any{"acme-scratch-resources"},
	})
	scratch, _ := out["scratch"].([]any)
	found := false
	for _, s := range scratch {
		row, _ := s.(map[string]any)
		if row["connection"] == "acme-scratch-resources" {
			found = true
			if row["drop_via_sql"] != true {
				t.Errorf("drop_via_sql = %v on %v", row["drop_via_sql"], row)
			}
		}
	}
	if !found {
		t.Fatalf("no scratch entry for acme-scratch-resources: %v", out["scratch"])
	}
}

// A persona with Resources none is refused registering a table over a file it
// uploaded.
func TestIssue2257_ResourcesNoneRefusesRegisteringOverOwnFile(t *testing.T) {
	admin := connect(t)
	m := newIssue2241Member(t, admin, "2257-res", nil)
	fileID := issue2257Upload(t, m.client, "user", "", "own")
	if status, out := admin.rest(http.MethodPut, "/api/v1/admin/personas/"+m.persona, jsonBody(t, map[string]any{
		"display_name": "Acceptance 2257-res", "roles": []any{strings.ReplaceAll(m.persona, "-", "_")},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"}, "portal": map[string]any{"resources": "none"},
	})); status != http.StatusOK {
		t.Fatalf("setting resources none: %d %v", status, out)
	}
	status, out := m.client.rest(http.MethodPost, "/api/v1/resources/"+fileID+"/tables",
		jsonBody(t, map[string]any{"connection": "acme-scratch-resources"}))
	if status != http.StatusForbidden || out["area"] != "resources" {
		t.Fatalf("registering over its own file at resources none: %d %v, want 403 naming resources", status, out)
	}
}

// A persona with PDF export off is refused both PDF routes on items it reads.
func TestIssue2257_PDFExportOffRefusesThePDFRoutes(t *testing.T) {
	admin := connect(t)
	m := newIssue2241Member(t, admin, "2257-pdf", map[string]any{"portal": map[string]any{"download": false}})
	saved := m.client.call("save_asset", map[string]any{
		"name": "acc-2257 pdf " + unique1579(), "content_type": "text/html",
		"content": "<p>acc-2257</p>", "description": "Acceptance #2257.",
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("saving the asset: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})
	fileID := issue2257Upload(t, m.client, "user", "", "pdf")
	if status, _ := m.client.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody); status != http.StatusOK {
		t.Fatalf("the persona cannot read its own asset (%d)", status)
	}
	for _, path := range []string{"/api/v1/portal/assets/" + assetID + "/pdf", "/api/v1/resources/" + fileID + "/pdf"} {
		status, body := rawGet(t, baseURL(), path, m.key)
		if status != http.StatusForbidden || !strings.Contains(string(body), "PDF export") {
			t.Errorf("GET %s with PDF export off: %d %s, want 403", path, status, body)
		}
	}
}

// An owner whose persona withholds sharing is refused approving a request,
// and no share is written.
func TestIssue2257_ApprovingARequestNeedsSharing(t *testing.T) {
	admin := connect(t)
	owner := newIssue2241Member(t, admin, "2257-own", map[string]any{"allow_sharing": false})
	requester := newIssue2241Member(t, admin, "2257-ask", nil)
	saved := owner.client.call("save_asset", map[string]any{
		"name": "acc-2257 approve " + unique1579(), "content_type": "text/html",
		"content": "<p>acc-2257</p>", "description": "Acceptance #2257.",
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("saving the asset: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})
	status, req := requester.client.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/access-requests", jsonBody(t, map[string]any{}))
	reqID, _ := req["id"].(string)
	if status != http.StatusCreated || reqID == "" {
		t.Fatalf("requesting access: %d %v", status, req)
	}
	status, out := owner.client.rest(http.MethodPost, "/api/v1/portal/access-requests/"+reqID+"/approve",
		jsonBody(t, map[string]any{"permission": "viewer"}))
	if status != http.StatusForbidden {
		t.Fatalf("approving with sharing off: %d %v, want 403", status, out)
	}
	var n int
	if err := issue2257DB(t).QueryRowContext(t.Context(),
		`SELECT count(*) FROM portal_shares WHERE asset_id = $1 AND origin = 'access_request'`, assetID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d share rows were written", n)
	}
}

// A tool allowed by a pattern whose every connection is denied is not
// callable.
func TestIssue2257_AToolWhoseConnectionsAreDeniedIsNotCallable(t *testing.T) {
	admin := connect(t)
	out := issue2257Access(t, admin, map[string]any{
		"name": "acc-2257-tool", "display_name": "Tool", "roles": []any{"acc_2257_tool"},
		"allow_tools": []any{"trino_*"}, "allow_connections": []any{},
	})
	items, _ := out["tools"].(map[string]any)["items"].([]any)
	for _, it := range items {
		row, _ := it.(map[string]any)
		if row["name"] != "trino_query" {
			continue
		}
		if row["decision"] != "allow" || row["callable"] != false {
			t.Fatalf("trino_query = %v, want allowed by pattern and not callable", row)
		}
		return
	}
	t.Fatalf("trino_query is not among the tools: %v", items)
}

// An external persona saved with Knowledge Edit, then made internal, has
// Knowledge Edit again.
func TestIssue2257_ExternalKeepsTheStoredPortal(t *testing.T) {
	admin := connect(t)
	name := fmt.Sprintf("acc-2257-keep-%d", time.Now().UnixNano())
	body := map[string]any{
		"name": name, "display_name": "Keep", "roles": []any{strings.ReplaceAll(name, "-", "_")},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
		"portal": map[string]any{"knowledge": "write"}, "external": true,
	}
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, body)); status != http.StatusCreated {
		t.Fatalf("creating: %d %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name+"?confirm=true", http.NoBody) })
	_, detail := admin.rest(http.MethodGet, "/api/v1/admin/personas/"+name, http.NoBody)
	stored, _ := detail["portal"].(map[string]any)
	effective, _ := detail["effective_portal"].(map[string]any)
	if stored["knowledge"] != "write" || effective["knowledge"] != "none" {
		t.Fatalf("external: portal %v, effective %v", stored, effective)
	}
	// The editor saves the stored block back, External off.
	delete(body, "name")
	body["portal"], body["external"] = stored, false
	if status, out := admin.rest(http.MethodPut, "/api/v1/admin/personas/"+name, jsonBody(t, body)); status != http.StatusOK {
		t.Fatalf("making it internal: %d %v", status, out)
	}
	_, detail = admin.rest(http.MethodGet, "/api/v1/admin/personas/"+name, http.NoBody)
	if effective, _ := detail["effective_portal"].(map[string]any); effective["knowledge"] != "write" {
		t.Fatalf("internal again: effective %v, want knowledge write", effective)
	}
}

// A person of an external persona at Resources read lists no Global file.
func TestIssue2257_AnExternalPersonReadsNoGlobal(t *testing.T) {
	admin := connect(t)
	globalID := issue2257Upload(t, admin, "global", "", "global")
	_, _, s := issue2257ExternalAccount(t, admin, "2257-glob", map[string]any{"portal": map[string]any{"resources": "shared"}})
	status, out := s.do(t, http.MethodGet, "/api/v1/resources?scope=global", nil)
	if status != http.StatusOK {
		t.Fatalf("listing: %d %v", status, out)
	}
	if strings.Contains(stringOf(out), globalID) {
		t.Errorf("the external person listed a Global file: %v", out)
	}
	if status, _ := s.do(t, http.MethodGet, "/api/v1/resources/"+globalID, nil); status != http.StatusNotFound {
		t.Errorf("reading the Global file answered %d, want 404", status)
	}
}

// effective-access reports the grants other objects hold that name the
// persona, each matching the store, and the role role_mapping assigns.
func TestIssue2257_EffectiveAccessReportsWhatNamesThePersona(t *testing.T) {
	admin := connect(t)
	db := issue2257DB(t)
	name := fmt.Sprintf("acc-2257-refs-%d", time.Now().UnixNano())
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": name, "display_name": "Refs", "roles": []any{strings.ReplaceAll(name, "-", "_")},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"}, "external": true,
	})); status != http.StatusCreated {
		t.Fatalf("creating: %d %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name+"?confirm=true", http.NoBody) })
	secret, _ := secret2051(t, admin, []string{name}, fixture2051)
	stamp := unique1579()
	scriptID := "00000000-0000-4000-8000-" + fmt.Sprintf("%012s", stamp)
	promptID := "00000000-0000-4000-9000-" + fmt.Sprintf("%012s", stamp)
	source := "acc-2257-" + stamp
	email := "acc-2257-" + stamp + "@example.org"
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO scripts (id, name) VALUES ($1, $2)`, []any{scriptID, "acc-2257-" + stamp}},
		{`INSERT INTO script_grants (script_id, principal_kind, principal) VALUES ($1, 'persona', $2)`, []any{scriptID, name}},
		{`INSERT INTO prompts (id, name, scope, personas) VALUES ($1, $2, 'persona', ARRAY[$3])`, []any{promptID, "acc-2257-" + stamp, name}},
		{`INSERT INTO webhook_sources (name, connection_name, config) VALUES ($1, 'acme-scratch', jsonb_build_object('persona', $2::text))`, []any{source, name}},
		{`INSERT INTO external_accounts (id, email, persona) VALUES ($1, $2, $3)`, []any{"acc2257r-" + stamp, email, name}},
	} {
		if _, err := db.ExecContext(t.Context(), stmt.q, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.q, err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = db.ExecContext(ctx, `DELETE FROM scripts WHERE id = $1`, scriptID)
		_, _ = db.ExecContext(ctx, `DELETE FROM prompts WHERE id = $1`, promptID)
		_, _ = db.ExecContext(ctx, `DELETE FROM webhook_sources WHERE name = $1`, source)
		_, _ = db.ExecContext(ctx, `DELETE FROM external_accounts WHERE email = $1`, email)
	})
	out := issue2257Access(t, admin, map[string]any{
		"name": name, "display_name": "Refs", "roles": []any{strings.ReplaceAll(name, "-", "_")},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"}, "external": true,
	})
	refs, _ := out["references"].(map[string]any)
	secrets, _ := refs["secrets"].(map[string]any)
	if !strings.Contains(stringOf(secrets["named"]), secret) {
		t.Errorf("secrets named = %v, want %s", secrets["named"], secret)
	}
	for key, want := range map[string]float64{"script_grants": 1, "prompts": 1, "external_accounts": 1} {
		if got := issue2257Num(t, refs, key); got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	if !strings.Contains(stringOf(refs["webhook_sources"]), source) {
		t.Errorf("webhook_sources = %v, want %s", refs["webhook_sources"], source)
	}

	mapped := issue2257Access(t, admin, map[string]any{
		"name": "data-engineer", "display_name": "Data Engineer", "roles": []any{"data_engineer"},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
	})
	assignment, _ := mapped["assignment"].(map[string]any)
	if !strings.Contains(stringOf(assignment["role_mapping"]), "dp_data_engineer") {
		t.Errorf("role_mapping = %v, want dp_data_engineer", assignment["role_mapping"])
	}
}

// Deleting a persona a secret names answers 409 listing it, and deletes once
// confirmed.
func TestIssue2257_DeletingANamedPersonaListsItsReferences(t *testing.T) {
	admin := connect(t)
	name := fmt.Sprintf("acc-2257-del-%d", time.Now().UnixNano())
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": name, "display_name": "Delete", "roles": []any{strings.ReplaceAll(name, "-", "_")},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
	})); status != http.StatusCreated {
		t.Fatalf("creating: %d %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name+"?confirm=true", http.NoBody) })
	secret, _ := secret2051(t, admin, []string{name}, fixture2051)
	status, out := admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody)
	refs, _ := out["references"].(map[string]any)
	if status != http.StatusConflict || !strings.Contains(stringOf(refs), secret) {
		t.Fatalf("deleting a named persona: %d %v, want 409 naming %s", status, out, secret)
	}
	if status, out := admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name+"?confirm=true", http.NoBody); status != http.StatusOK {
		t.Fatalf("deleting confirmed: %d %v", status, out)
	}
}
