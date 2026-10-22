//go:build integration

package acceptance

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
	"time"
)

// Issue #2225: portal permissions per persona. A persona's portal block sets
// one level per portal area; /api/v1/portal/me reports it, and every route of
// an area refuses with 403 what the level does not allow. A persona with no
// block keeps what it reached before.
//
// Wire forms: the admin persona body's portal is a JSON object, sent with
// string levels (knowledge "none" and "read", resources "own") and boolean
// switches (memory, search, automations false), and sent absent. The
// resources upload is a multipart form with the file part last.

// issue2225Me reads the caller's /me portal block.
func issue2225Me(t *testing.T, c *client) map[string]any {
	t.Helper()
	status, me := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /me: %d %v", status, me)
	}
	portal, ok := me["portal"].(map[string]any)
	if !ok {
		t.Fatalf("/me carries no portal block: %v", me)
	}
	return portal
}

// issue2225Upload uploads a small text file into a resource library as c.
func issue2225Upload(t *testing.T, c *client, scope, scopeID, label string) (int, map[string]any) {
	t.Helper()
	name := fmt.Sprintf("acceptance-2225-%s-%d", label, time.Now().UnixNano())
	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		fields := map[string]string{
			"scope": scope, "path": "acceptance-2225", "display_name": name,
			"description": "Acceptance #2225: a file uploaded under a persona's resources level.",
		}
		if scopeID != "" {
			fields["scope_id"] = scopeID
		}
		for k, v := range fields {
			if err := w.WriteField(k, v); err != nil {
				return err
			}
		}
		part, err := filePart1631(w, name+".txt", "text/plain")
		if err != nil {
			return err
		}
		if _, err := part.Write([]byte("acceptance 2225\n")); err != nil {
			return err
		}
		return w.Close()
	})
	if id, _ := body["id"].(string); id != "" {
		admin := connect(t)
		t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	}
	return status, body
}

// issue2225Listed reports whether a resource listing as c holds the id.
func issue2225Listed(t *testing.T, c *client, query, id string) bool {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/resources?"+query, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/resources?%s: %d %v", query, status, out)
	}
	rows, _ := out["resources"].([]any)
	if rows == nil {
		rows, _ = out["data"].([]any)
	}
	for _, r := range rows {
		if row, ok := r.(map[string]any); ok && row["id"] == id {
			return true
		}
	}
	return false
}

// A persona with knowledge none has no Knowledge in its portal, and the
// knowledge-pages list refuses its people with 403 naming the area.
func TestIssue2225_KnowledgeNoneTakesTheSectionAway(t *testing.T) {
	admin := connect(t)
	m := newIssue2241Member(t, admin, "2225-none", map[string]any{"portal": map[string]any{
		"knowledge": "none", "catalog": "none", "memory": false, "search": false,
	}})
	portal := issue2225Me(t, m.client)
	if portal["knowledge"] != "none" || portal["catalog"] != "none" || portal["memory"] != false || portal["search"] != false {
		t.Fatalf("/me portal = %v, want knowledge, catalog, memory and search off (the navigation's Knowledge reads these)", portal)
	}
	status, body := m.client.rest(http.MethodGet, "/api/v1/portal/knowledge-pages", http.NoBody)
	if status != http.StatusForbidden || body["area"] != "knowledge" {
		t.Fatalf("GET knowledge-pages: %d %v, want 403 naming knowledge", status, body)
	}
}

// With knowledge read the list returns and a create is refused.
func TestIssue2225_KnowledgeReadListsAndDoesNotCreate(t *testing.T) {
	admin := connect(t)
	m := newIssue2241Member(t, admin, "2225-read", map[string]any{"portal": map[string]any{"knowledge": "read"}})
	if portal := issue2225Me(t, m.client); portal["knowledge"] != "read" {
		t.Fatalf("/me portal knowledge = %v, want read", portal["knowledge"])
	}
	if status, body := m.client.rest(http.MethodGet, "/api/v1/portal/knowledge-pages", http.NoBody); status != http.StatusOK {
		t.Fatalf("GET knowledge-pages: %d %v, want 200", status, body)
	}
	status, body := m.client.rest(http.MethodPost, "/api/v1/portal/knowledge-pages", jsonBody(t, map[string]any{
		"title": "Acceptance 2225", "body": "# Acceptance 2225\n",
	}))
	if status != http.StatusForbidden {
		t.Fatalf("POST knowledge-pages at read: %d %v, want 403", status, body)
	}
}

// With resources own a person uploads to their own folder, an administrator
// sees the upload, and the persona folder is not opened to them.
func TestIssue2225_OwnResourcesGoToTheirFolderAndAnAdministratorSeesThem(t *testing.T) {
	admin := connect(t)
	m := newIssue2241Member(t, admin, "2225-own", map[string]any{"portal": map[string]any{"resources": "own"}})

	status, body := issue2225Upload(t, m.client, "user", m.userID, "own")
	id, _ := body["id"].(string)
	if status != http.StatusCreated || id == "" {
		t.Fatalf("upload to their own folder: %d %v, want 201", status, body)
	}
	if !issue2225Listed(t, admin, "", id) {
		t.Fatalf("an administrator's listing does not hold %s", id)
	}

	pStatus, pBody := issue2225Upload(t, admin, "persona", m.persona, "persona-folder")
	inPersona, _ := pBody["id"].(string)
	if pStatus != http.StatusCreated || inPersona == "" {
		t.Fatalf("an administrator's upload to the persona folder: %d %v", pStatus, pBody)
	}
	if issue2225Listed(t, m.client, "scope=persona&scope_id="+m.persona, inPersona) {
		t.Fatalf("the persona folder opened to a person at the own level")
	}
	if status, _ := issue2225Upload(t, m.client, "persona", m.persona, "refused"); status == http.StatusCreated {
		t.Fatalf("a person at the own level uploaded into the persona folder")
	}
}

// A persona saved with no portal block reports every area as it was, and
// the routes of each area answer it as before.
func TestIssue2225_APersonaWithNoBlockKeepsWhatItHad(t *testing.T) {
	for _, c := range []struct {
		name   string
		client func(t *testing.T) *client
	}{
		{"a persona created without the block", func(t *testing.T) *client {
			return newIssue2241Member(t, connect(t), "2225-unset", nil).client
		}},
		{"the dev stack's collaborator, from the file", func(t *testing.T) *client { return connectAs(t, devOwnerAPIKey) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			cl := c.client(t)
			_, me := cl.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
			tools := map[string]bool{}
			if list, ok := me["tools"].([]any); ok {
				for _, x := range list {
					tools[fmt.Sprint(x)] = true
				}
			}
			portal := issue2225Me(t, cl)
			wantKnowledge := "read"
			if tools["apply_knowledge"] {
				wantKnowledge = "write"
			}
			wantCatalog := "none"
			switch {
			case tools["apply_knowledge"] && tools["fetch"]:
				wantCatalog = "write"
			case tools["fetch"]:
				wantCatalog = "read"
			}
			want := map[string]any{
				"assets": "own", "resources": "persona", "apis": "browse",
				"knowledge": wantKnowledge, "catalog": wantCatalog,
				"feedback": true, "prompts": true, "automations": true, "search": true, "memory": true, "download": true,
			}
			for k, v := range want {
				if portal[k] != v {
					t.Errorf("/me portal %s = %v, want %v", k, portal[k], v)
				}
			}
			for _, path := range []string{
				"/api/v1/portal/knowledge-pages", "/api/v1/portal/prompts", "/api/v1/portal/scripts",
				"/api/v1/apis", "/api/v1/portal/threads", "/api/v1/portal/memory/records",
			} {
				if status, body := cl.rest(http.MethodGet, path, http.NoBody); status == http.StatusForbidden {
					t.Errorf("GET %s refused a persona with no block: %v", path, body)
				}
			}
			if status, body := cl.rest(http.MethodGet, "/api/v1/resources", http.NoBody); status != http.StatusOK {
				t.Errorf("GET /api/v1/resources: %d %v", status, body)
			}
		})
	}
}

// Switches off refuse their areas' routes, and an administrator is never
// refused, whatever their persona says.
func TestIssue2225_ASwitchOffRefusesItsRoutes(t *testing.T) {
	admin := connect(t)
	m := newIssue2241Member(t, admin, "2225-off", map[string]any{"portal": map[string]any{
		"automations": false, "prompts": false, "feedback": false, "apis": "none", "download": false,
	}})
	for path, area := range map[string]string{
		"/api/v1/portal/scripts": "automations", "/api/v1/portal/prompts": "prompts",
		"/api/v1/portal/threads": "feedback", "/api/v1/apis": "apis",
	} {
		status, body := m.client.rest(http.MethodGet, path, http.NoBody)
		if status != http.StatusForbidden || body["area"] != area {
			t.Errorf("GET %s: %d %v, want 403 naming %s", path, status, body, area)
		}
		if status, _ := admin.rest(http.MethodGet, path, http.NoBody); status == http.StatusForbidden {
			t.Errorf("GET %s refused an administrator", path)
		}
	}
	if portal := issue2225Me(t, admin); portal["knowledge"] != "write" || portal["resources"] != "persona" || portal["automations"] != true {
		t.Errorf("an administrator's /me portal = %v, want every area at its highest", portal)
	}
}
