//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1553: the Resources library reads like the Assets gallery. The half of
// that ticket which is executable through the platform's own surface is the
// listing predicate the library picker is built on: an administrator may write
// and open any library by id, and until this change could not LIST one they
// were not a member of. So an administrator uploaded into a persona and the
// file was then absent from every listing they could ask for.
//
// The other half -- the picker, the thumbnails, the recently-updated strip, the
// folder picker on upload -- is browser behavior with no wire surface of its
// own; it is covered by the page's own suite and by the Playwright run.
//
// Wire forms: every parameter here is typed in its schema and admits exactly
// one JSON form. manage_resource's action, filename, display_name, path,
// description, scope, scope_id, content and content_type are strings and tags
// is an array of strings, each sent below as a literal tools/call parameter of
// that form. The listing is a REST GET whose scope and scope_id are query-string
// parameters, which have no second form; both the narrowed and the unnarrowed
// spelling are issued.

// The two personas this file turns on, both read off dev/platform.yaml.
//
// unreachedPersona1553 is one the administrator and the ordinary person are
// BOTH outside of, which is what makes a single library prove both halves of
// the rule: the administrator reaches it on authority, and the ordinary person
// does not reach it at all.
//
// ownPersona1553 is the one the ordinary person belongs to, so their own
// unnarrowed listing has something in it besides their own files.
const (
	unreachedPersona1553 = "inventory-analyst"
	ownPersona1553       = "collaborator"
)

// unique1553 names one run of this file so a re-run does not collide with what
// the last one left behind.
func unique1553() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// createResource1553 files a resource as the calling person and returns its id,
// removing it when the test ends.
//
// An empty scope files it into the caller's own library, keyed on whatever
// identifier their claims carry, which is how a test says "their own" without
// having to know the shape of that identifier.
func createResource1553(t *testing.T, c *client, admin *client, scope, scopeID, name string) string {
	t.Helper()
	args := map[string]any{
		"action":       "create",
		"filename":     name + ".md",
		"display_name": name,
		"path":         "references",
		"description":  "Acceptance #1553: a file filed into one named library.",
		"content":      "# " + name + "\n",
		"content_type": "text/markdown",
		"tags":         []any{"acceptance-1553"},
	}
	if scope != "" {
		args["scope"] = scope
		args["scope_id"] = scopeID
	}
	out := c.call("manage_resource", args)
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() {
		_, _ = admin.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
	})
	return id
}

// listedIDs1553 returns the ids the listing at the given query answers with.
func listedIDs1553(t *testing.T, c *client, query string) map[string]bool {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources"+query, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/resources%s: status %d: %v", query, status, body)
	}
	ids := map[string]bool{}
	list, _ := body["resources"].([]any)
	for _, item := range list {
		r, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := r["id"].(string); id != "" {
			ids[id] = true
		}
	}
	return ids
}

// scopesOf1553 returns the distinct (scope, scope_id) pairs a listing answered
// with, which is what says whose libraries a caller actually reached.
func scopesOf1553(t *testing.T, c *client, query string) map[string]bool {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources"+query, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/resources%s: status %d: %v", query, status, body)
	}
	pairs := map[string]bool{}
	list, _ := body["resources"].([]any)
	for _, item := range list {
		r, ok := item.(map[string]any)
		if !ok {
			continue
		}
		scope, _ := r["scope"].(string)
		scopeID, _ := r["scope_id"].(string)
		pairs[scope+":"+scopeID] = true
	}
	return pairs
}

// TestIssue1553_AnAdministratorListsAPersonaLibraryTheyAreNotIn walks the
// criterion in the order a person meets it: the administrator files something
// into a persona, then goes looking for it both ways the picker asks.
func TestIssue1553_AnAdministratorListsAPersonaLibraryTheyAreNotIn(t *testing.T) {
	admin := connect(t)
	id := unique1553()
	name := "acceptance-1553-" + id

	created := createResource1553(
		t, admin, admin, "persona", unreachedPersona1553, name,
	)

	// Narrowed, which is what the picker sends when one library is chosen.
	narrowed := listedIDs1553(t, admin, "?scope=persona&scope_id="+unreachedPersona1553+"&limit=200")
	if !narrowed[created] {
		t.Errorf("the administrator's listing of %s does not hold the file they just filed there",
			unreachedPersona1553)
	}

	// Unnarrowed, which is what the picker sends on All.
	all := listedIDs1553(t, admin, "?limit=200")
	if !all[created] {
		t.Errorf("the administrator's unnarrowed listing does not hold the file they just filed into %s",
			unreachedPersona1553)
	}
}

// TestIssue1553_AnOrdinaryCallerReachesNoLibraryTheyAreNotIn is the other half:
// widening the administrator's listing widened nobody else's.
func TestIssue1553_AnOrdinaryCallerReachesNoLibraryTheyAreNotIn(t *testing.T) {
	admin := connect(t)
	person := connectAs(t, devOwnerAPIKey)
	id := unique1553()

	hidden := createResource1553(
		t, admin, admin, "persona", unreachedPersona1553,
		"acceptance-1553-hidden-"+id,
	)

	// Naming the library explicitly reaches nothing: this person holds no
	// authority over it and is not a member.
	named := listedIDs1553(t, person,
		"?scope=persona&scope_id="+unreachedPersona1553+"&limit=200")
	if named[hidden] {
		t.Errorf("an ordinary caller listed %s, a persona they are not in",
			unreachedPersona1553)
	}

	// And neither does the unnarrowed listing, which is where they start: every
	// library it reached is the global one, the persona they belong to, or a
	// user library -- which can only be their own, since a user library is keyed
	// by the identifiers of the person whose it is.
	for pair := range scopesOf1553(t, person, "?limit=200") {
		if pair == "global:" || pair == "persona:"+ownPersona1553 || strings.HasPrefix(pair, "user:") {
			continue
		}
		t.Errorf("an ordinary caller's unnarrowed listing reached %s, "+
			"which is neither theirs, their persona's, nor global", pair)
	}
}

// TestIssue1553_AnOrdinaryCallerStillReachesTheirOwnLibraries proves the
// unnarrowed listing every page now opens on is not empty for a reader: it is
// their own library, their persona's, and the global one.
func TestIssue1553_AnOrdinaryCallerStillReachesTheirOwnLibraries(t *testing.T) {
	admin := connect(t)
	person := connectAs(t, devOwnerAPIKey)
	id := unique1553()

	own := createResource1553(t, person, admin, "", "", "acceptance-1553-own-"+id)
	shared := createResource1553(t, admin, admin, "persona", ownPersona1553,
		"acceptance-1553-shared-"+id)
	global := createResource1553(t, admin, admin, "global", "",
		"acceptance-1553-global-"+id)

	all := listedIDs1553(t, person, "?limit=200")
	for _, want := range []struct {
		id, what string
	}{
		{own, "their own library"},
		{shared, "the persona library they belong to"},
		{global, "the global library"},
	} {
		if !all[want.id] {
			t.Errorf("an ordinary caller's unnarrowed listing is missing the file in %s", want.what)
		}
	}
}
