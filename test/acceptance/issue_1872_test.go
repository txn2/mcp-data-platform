//go:build integration

package acceptance

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Issue #1872: the Resources page is a file manager. What it needed from the
// server is exercised here through the routes the page calls: a folder stored
// on its own (created empty, kept after its last file leaves, moved while
// empty, deleted only once empty), the administrator's People folder, and the
// column sorts a paged folder has to be sorted by on the server.
//
// Wire forms. This ticket touches no MCP tool parameter. Its surface is the
// folder routes' JSON body, and each form it admits is sent: scope_id present
// (a person's or a persona's folder) and absent (Global, keyed by no id), a
// valid path, a path the grammar refuses, and a body that is not JSON. The
// listing's sort parameter is sent in every value the page sends.

// folder1872 is a top-level folder no earlier run used.
func folder1872(tag string) string {
	return "a1872-" + tag + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// me1872 is the caller's own library key: the subject their files are filed
// under, as the portal reads it.
func me1872(t *testing.T, c *client) string {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/portal/me: %d %v", status, body)
	}
	id, _ := body["user_id"].(string)
	if id == "" {
		t.Fatalf("/me carried no user_id: %v", body)
	}
	return id
}

// folders1872 is what the facets route reports for one library: each folder
// path and its count.
func folders1872(t *testing.T, c *client, query string) map[string]int {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources/facets?"+query, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("facets: %d %v", status, body)
	}
	out := map[string]int{}
	list, _ := body["folders"].([]any)
	for _, f := range list {
		m, _ := f.(map[string]any)
		path, _ := m["path"].(string)
		n, _ := m["count"].(float64)
		out[path] = int(n)
		if _, ok := m["updated_at"]; !ok {
			t.Errorf("folder %s carries no updated_at", path)
		}
	}
	return out
}

// upload1872 files one small file into a folder of the caller's library.
func upload1872(t *testing.T, c *client, scopeID, path, name string, size int) string {
	t.Helper()
	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		for _, kv := range [][2]string{{"scope", "user"}, {"scope_id", scopeID}, {"path", path}, {"display_name", name}, {"description", "Acceptance #1872"}} {
			if err := w.WriteField(kv[0], kv[1]); err != nil {
				return err
			}
		}
		part, err := w.CreateFormFile("file", name)
		if err != nil {
			return err
		}
		if _, err := part.Write([]byte(strings.Repeat("x", size))); err != nil {
			return err
		}
		return w.Close()
	})
	if status != http.StatusCreated {
		t.Fatalf("upload %s: %d %v", name, status, body)
	}
	id, _ := body["id"].(string)
	return id
}

func deleteResource1872(t *testing.T, c *client, id string) {
	t.Helper()
	if status, body := c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody); status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("delete %s: %d %v", id, status, body)
	}
}

func TestIssue1872_AFolderIsCreatedEmptyAndListed(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	me := me1872(t, c)
	top := folder1872("new")
	body := map[string]any{"scope": "user", "scope_id": me, "path": top + "/inner"}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/resources/folders", jsonBody(t, map[string]any{"scope": "user", "scope_id": me, "path": top}))
	})

	if status, out := c.rest(http.MethodPost, "/api/v1/resources/folders", jsonBody(t, body)); status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	got := folders1872(t, c, "scope=user")
	if n, ok := got[top+"/inner"]; !ok || n != 0 {
		t.Fatalf("the empty folder is not listed with a zero count: %v", got)
	}
	if _, ok := got[top]; !ok {
		t.Fatalf("the folder above it is not listed: %v", got)
	}
	if status, _ := c.rest(http.MethodPost, "/api/v1/resources/folders", jsonBody(t, body)); status != http.StatusConflict {
		t.Fatalf("a second create answered %d, want 409", status)
	}
}

func TestIssue1872_EveryBodyFormIsAnsweredOnItsMerits(t *testing.T) {
	admin := connect(t)
	global := folder1872("global")
	// Global is keyed by no id: the body leaves scope_id out.
	status, out := admin.rest(http.MethodPost, "/api/v1/resources/folders", jsonBody(t, map[string]any{"scope": "global", "path": global}))
	if status != http.StatusCreated {
		t.Fatalf("global create: %d %v", status, out)
	}
	if _, ok := folders1872(t, admin, "scope=global")[global]; !ok {
		t.Fatal("the global folder is not listed")
	}
	if status, _ := admin.rest(http.MethodDelete, "/api/v1/resources/folders", jsonBody(t, map[string]any{"scope": "global", "path": global})); status != http.StatusNoContent {
		t.Fatalf("global delete answered %d", status)
	}
	// A persona's folder names its persona.
	persona := folder1872("persona")
	status, out = admin.rest(http.MethodPost, "/api/v1/resources/folders", jsonBody(t, map[string]any{"scope": "persona", "scope_id": "data-person", "path": persona}))
	if status != http.StatusCreated {
		t.Fatalf("persona create: %d %v", status, out)
	}
	admin.rest(http.MethodDelete, "/api/v1/resources/folders", jsonBody(t, map[string]any{"scope": "persona", "scope_id": "data-person", "path": persona}))

	if status, _ := admin.rest(http.MethodPost, "/api/v1/resources/folders", jsonBody(t, map[string]any{"scope": "global", "path": "/Bad Path/"})); status != http.StatusBadRequest {
		t.Fatalf("a path the grammar refuses answered %d, want 400", status)
	}
	if status, _ := admin.rest(http.MethodPost, "/api/v1/resources/folders", strings.NewReader("{")); status != http.StatusBadRequest {
		t.Fatalf("a body that is not JSON answered %d, want 400", status)
	}
}

func TestIssue1872_SomebodyElseCannotAddAFolderToYourResources(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	me := me1872(t, owner)
	status, _ := peer.rest(http.MethodPost, "/api/v1/resources/folders",
		jsonBody(t, map[string]any{"scope": "user", "scope_id": me, "path": folder1872("intrude")}))
	if status != http.StatusForbidden {
		t.Fatalf("a peer creating a folder in the owner's resources answered %d, want 403", status)
	}
}

func TestIssue1872_AFolderOutlivesItsLastFileMovesEmptyAndIsDeletedOnlyWhenEmpty(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	me := me1872(t, c)
	top := folder1872("life")
	from, to, moved := top+"/weekly", top+"/archive", top+"/kept"
	lib := func(path string) map[string]any { return map[string]any{"scope": "user", "scope_id": me, "path": path} }

	id := upload1872(t, c, me, from, "orders.csv", 10)
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
		c.rest(http.MethodDelete, "/api/v1/resources/folders", jsonBody(t, lib(top)))
	})

	// The file moves out; the folder it left stays, empty.
	if status, out := c.rest(http.MethodPatch, "/api/v1/resources/"+id, jsonBody(t, map[string]any{"path": to})); status != http.StatusOK {
		t.Fatalf("moving the file: %d %v", status, out)
	}
	got := folders1872(t, c, "scope=user")
	if n, ok := got[from]; !ok || n != 0 {
		t.Fatalf("the emptied folder %s is gone or still counts a file: %v", from, got)
	}
	if got[to] != 1 {
		t.Fatalf("the destination does not count the file: %v", got)
	}

	// An empty folder moves.
	status, out := c.rest(http.MethodPost, "/api/v1/resources/folders/move",
		jsonBody(t, map[string]any{"scope": "user", "scope_id": me, "from": from, "to": moved}))
	if status != http.StatusOK {
		t.Fatalf("moving the empty folder: %d %v", status, out)
	}
	got = folders1872(t, c, "scope=user")
	if _, ok := got[from]; ok {
		t.Fatalf("the moved folder is still at %s: %v", from, got)
	}
	if _, ok := got[moved]; !ok {
		t.Fatalf("the moved folder is not at %s: %v", moved, got)
	}

	// A folder holding a file is refused; emptied, it goes.
	if status, _ := c.rest(http.MethodDelete, "/api/v1/resources/folders", jsonBody(t, lib(to))); status != http.StatusConflict {
		t.Fatalf("deleting a folder that holds a file answered %d, want 409", status)
	}
	deleteResource1872(t, c, id)
	if status, _ := c.rest(http.MethodDelete, "/api/v1/resources/folders", jsonBody(t, lib(to))); status != http.StatusNoContent {
		t.Fatalf("deleting the emptied folder answered %d, want 204", status)
	}
	if _, ok := folders1872(t, c, "scope=user")[to]; ok {
		t.Fatal("the deleted folder is still listed")
	}
	if status, _ := c.rest(http.MethodDelete, "/api/v1/resources/folders", jsonBody(t, lib(top+"/nothing"))); status != http.StatusNotFound {
		t.Fatalf("deleting a folder that does not exist answered %d, want 404", status)
	}
}

func TestIssue1872_PeopleIsForAdministratorsAndNamesEachPerson(t *testing.T) {
	person := connectAs(t, devPeerAPIKey)
	me := me1872(t, person)
	top := folder1872("people")
	id := upload1872(t, person, me, top, "notes.md", 4)
	t.Cleanup(func() {
		person.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
		person.rest(http.MethodDelete, "/api/v1/resources/folders", jsonBody(t, map[string]any{"scope": "user", "scope_id": me, "path": top}))
	})

	if status, _ := person.rest(http.MethodGet, "/api/v1/resources/people", http.NoBody); status != http.StatusForbidden {
		t.Fatalf("a non-administrator listing people answered %d, want 403", status)
	}
	admin := connect(t)
	status, body := admin.rest(http.MethodGet, "/api/v1/resources/people", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("people: %d %v", status, body)
	}
	people, _ := body["people"].([]any)
	var found map[string]any
	for _, p := range people {
		if m, _ := p.(map[string]any); m["scope_id"] == me {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("the person's folder is not under People: %v", people)
	}
	if found["email"] != "asset.peer@example.com" {
		t.Errorf("the person's folder is named %v", found["email"])
	}
	if n, _ := found["count"].(float64); n < 1 {
		t.Errorf("the person's folder counts %v files", found["count"])
	}
	// The administrator opens that person's folder by its key.
	got := folders1872(t, admin, "scope=user&scope_id="+url.QueryEscape(me))
	if got[top] != 1 {
		t.Fatalf("the administrator cannot read the person's folder: %v", got)
	}
}

func TestIssue1872_AFolderIsSortedOnTheServer(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	me := me1872(t, c)
	top := folder1872("sort")
	var ids []string
	for _, f := range []struct {
		name string
		size int
	}{{"b.csv", 30}, {"a.csv", 10}, {"c.csv", 20}} {
		ids = append(ids, upload1872(t, c, me, top, f.name, f.size))
	}
	t.Cleanup(func() {
		for _, id := range ids {
			c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
		}
		c.rest(http.MethodDelete, "/api/v1/resources/folders", jsonBody(t, map[string]any{"scope": "user", "scope_id": me, "path": top}))
	})

	for sort, want := range map[string]string{
		"name": "a.csv,b.csv,c.csv", "name_desc": "c.csv,b.csv,a.csv",
		"size": "a.csv,c.csv,b.csv", "size_desc": "b.csv,c.csv,a.csv",
		"updated_asc": "b.csv,a.csv,c.csv", "updated": "c.csv,a.csv,b.csv",
	} {
		q := fmt.Sprintf("/api/v1/resources?scope=user&path=%s&direct=true&sort=%s", url.QueryEscape(top), sort)
		status, body := c.rest(http.MethodGet, q, http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("list sort=%s: %d %v", sort, status, body)
		}
		var names []string
		rows, _ := body["resources"].([]any)
		for _, r := range rows {
			m, _ := r.(map[string]any)
			n, _ := m["display_name"].(string)
			names = append(names, n)
		}
		if got := strings.Join(names, ","); got != want {
			t.Errorf("sort=%s listed %s, want %s", sort, got, want)
		}
	}
}
