//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// embedWait2500 bounds the wait for a just-saved asset to be embedded. A
// nightly runner embeds on CPU, behind the 57 assets this fixture saves and
// whatever the index queue already held (#2516).
const embedWait2500 = 3 * time.Minute

// Issue #2500: search, sort and every filter on the Assets and Collections
// tabs run on the server in every scope, over exactly what the caller can
// open: owned, shared to them by user or persona, or reached through a shared
// collection. Hybrid ranking applies in every scope and pages; an author
// filter and a tag facet are offered; manage_asset search and list take the
// same scope and filters. A caller learns nothing about an asset they cannot
// open: no row, no count, no owner, no tag.
//
// Wire forms: the portal routes' scope, q, tag and content_type as strings and
// owner both repeated (owner=a&owner=b) and comma-separated (owner=a,b);
// manage_asset's action, query and scope as strings, owners and tags as JSON
// arrays of strings, limit and offset as numbers; the share body's
// shared_with_persona and permission as strings; the persona body's roles,
// allow_tools and allow_connections as arrays of strings and priority as a
// number.

// issue2500 is the fixture every criterion reads: a caller who is a member of
// a fresh persona, and what three owners saved around them.
type issue2500 struct {
	stamp      int64
	caller     *client
	descTarget string // shared to the persona, oldest of 56, matched only by its description
	tagTarget  string // the peer's, shared to the persona, carrying a tag only it has
	collTarget string // reached only through a collection shared to the persona
	tag        string
	descToken  string
	// What the caller must never learn about.
	hiddenName  string
	hiddenTag   string
	hiddenOwner string
	peerHidden  string // the peer's own asset, never shared
}

func newIssue2500(t *testing.T) *issue2500 {
	t.Helper()
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	s := &issue2500{stamp: time.Now().UnixNano(), hiddenOwner: "admin@example.com"}
	s.tag = fmt.Sprintf("acc2500tag%d", s.stamp)
	s.descToken = fmt.Sprintf("quillback%d", s.stamp)
	s.hiddenTag = fmt.Sprintf("acc2500hiddentag%d", s.stamp)
	s.hiddenName = fmt.Sprintf("acc2500 hidden %d", s.stamp)

	persona := fmt.Sprintf("acc-2500-%d", s.stamp)
	role := persona + "_member"
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": persona, "display_name": "Acceptance 2500", "roles": []any{role}, "priority": 10,
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
	})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving persona %s: %d %v", persona, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+persona, http.NoBody) })
	s.caller = issue2089Key(t, admin, persona+"-caller", role)

	save := func(c *client, name, description string, tags ...string) string {
		t.Helper()
		args := map[string]any{"name": name, "content_type": "text/html", "content": "<p>" + name + "</p>", "description": description}
		if len(tags) > 0 {
			args["tags"] = tags
		}
		out := c.call("save_asset", args)
		id, _ := out["asset_id"].(string)
		if id == "" {
			t.Fatalf("saving %s: %v", name, out)
		}
		t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
		return id
	}
	share := func(c *client, kind, id string) {
		t.Helper()
		status, out := c.rest(http.MethodPost, "/api/v1/portal/"+kind+"/"+id+"/shares", jsonBody(t, map[string]any{
			"shared_with_persona": persona, "permission": "viewer",
		}))
		if status != http.StatusCreated {
			t.Fatalf("sharing %s %s with the persona: %d %v", kind, id, status, out)
		}
	}

	// The description target is saved first, so 55 newer shared assets put
	// it beyond the first page of 50 in every ordering by time.
	s.descTarget = save(owner, fmt.Sprintf("acc2500 forecast %d", s.stamp),
		"A forecast of rainfall for the coastal orchards, filed under "+s.descToken+".")
	share(owner, "assets", s.descTarget)
	s.tagTarget = save(peer, fmt.Sprintf("acc2500 peer %d", s.stamp), "The peer's shared note.", s.tag)
	share(peer, "assets", s.tagTarget)
	for i := range 55 {
		share(owner, "assets", save(owner, fmt.Sprintf("acc2500 filler %d %d", s.stamp, i), "Filler for acceptance #2500."))
	}
	s.collTarget = save(owner, fmt.Sprintf("acc2500 boxed %d", s.stamp), "Reached only through a collection, "+s.descToken+" boxed.")
	coll := owner.call("manage_asset", map[string]any{
		"action": "create_collection", "name": fmt.Sprintf("acc2500 collection %d", s.stamp),
		"sections": []any{map[string]any{"title": "Only", "items": []any{map[string]any{"asset_id": s.collTarget}}}},
	})
	collID, _ := coll["collection_id"].(string)
	if collID == "" {
		t.Fatalf("create_collection returned no id: %v", coll)
	}
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": collID})
	})
	share(owner, "collections", collID)

	// Never shared with the caller.
	save(admin, s.hiddenName, "Nobody else may know of "+s.descToken+" here.", s.hiddenTag)
	s.peerHidden = save(peer, fmt.Sprintf("acc2500 peer private %d", s.stamp), "The peer's own "+s.descToken+".", s.tag)
	return s
}

// portalGet reads one portal route as the caller and returns its raw body.
func (s *issue2500) portalGet(t *testing.T, path string, q url.Values) (int, string) {
	t.Helper()
	return s.caller.restText(path + "?" + q.Encode())
}

func ids2500(t *testing.T, body string, scored bool) []string {
	t.Helper()
	var page struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("not a page: %.300s", body)
	}
	out := make([]string, 0, len(page.Data))
	for _, raw := range page.Data {
		var row struct {
			ID    string `json:"id"`
			Asset struct {
				ID string `json:"id"`
			} `json:"asset"`
		}
		_ = json.Unmarshal(raw, &row)
		if scored {
			out = append(out, row.Asset.ID)
		} else {
			out = append(out, row.ID)
		}
	}
	return out
}

func contains2500(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

func (s *issue2500) assertNothingHidden(t *testing.T, what, body string) {
	t.Helper()
	for _, secret := range []string{s.hiddenName, s.hiddenTag, s.hiddenOwner, s.peerHidden} {
		if strings.Contains(body, secret) {
			t.Errorf("%s names %q, which the caller cannot open: %.400s", what, secret, body)
		}
	}
}

func TestIssue2500_SearchInEveryScopeCoversWhatTheCallerCanOpen(t *testing.T) {
	s := newIssue2500(t)

	// The description target is on no first page of the listing.
	_, listing := s.portalGet(t, "/api/v1/portal/assets", url.Values{"scope": {"shared"}, "limit": {"50"}})
	if contains2500(ids2500(t, listing, false), s.descTarget) {
		t.Fatalf("the description target is on the first page; the fixture does not test paging")
	}

	// Shared: the match is in its description only, and it is found.
	status, body := s.portalGet(t, "/api/v1/portal/assets/search", url.Values{"q": {s.descToken}, "scope": {"shared"}})
	if status != http.StatusOK {
		t.Fatalf("searching shared: %d %.300s", status, body)
	}
	got := ids2500(t, body, true)
	if !contains2500(got, s.descTarget) {
		t.Errorf("a search in Shared for a word in the description did not find it: %v", got)
	}
	if !contains2500(got, s.collTarget) {
		t.Errorf("a search in Shared did not find the asset reached through a shared collection: %v", got)
	}
	s.assertNothingHidden(t, "the Shared search", body)

	// The same asset found by meaning, with no word of the query in it. Only
	// its embedding can match, and the platform embeds in the background
	// behind whatever the index queue already holds, so the search is asked
	// again until the asset is embedded.
	meaning := url.Values{"q": {"weather prediction for fruit farms by the sea"}, "scope": {"all"}, "limit": {"100"}}
	deadline := time.Now().Add(embedWait2500)
	for {
		status, body = s.portalGet(t, "/api/v1/portal/assets/search", meaning)
		if status == http.StatusOK && contains2500(ids2500(t, body, true), s.descTarget) {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("a search by meaning in All did not find the forecast within %s: %d %.400s", embedWait2500, status, body)
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Mine holds none of it: the caller owns nothing.
	_, body = s.portalGet(t, "/api/v1/portal/assets/search", url.Values{"q": {s.descToken}, "scope": {"mine"}})
	if got := ids2500(t, body, true); len(got) != 0 {
		t.Errorf("a search in Mine returned assets the caller does not own: %v", got)
	}

	// The agent's search: all by default, and the same in Shared.
	for _, args := range []map[string]any{
		{"action": "search", "query": s.descToken},
		{"action": "search", "query": s.descToken, "scope": "shared", "limit": 20, "offset": 0},
	} {
		_, text, err := s.caller.callRaw("manage_asset", args)
		if err != nil || !strings.Contains(text, s.descTarget) || !strings.Contains(text, s.collTarget) {
			t.Errorf("manage_asset %v did not find both shared assets: %v %.400s", args, err, text)
		}
		s.assertNothingHidden(t, "manage_asset search", text)
	}
}

func TestIssue2500_FiltersRunOnTheServerInEveryScope(t *testing.T) {
	s := newIssue2500(t)

	// A tag on an asset shared with the caller, beyond the first page.
	status, body := s.portalGet(t, "/api/v1/portal/assets", url.Values{"scope": {"all"}, "tag": {s.tag}})
	got := ids2500(t, body, false)
	if status != http.StatusOK || len(got) != 1 || got[0] != s.tagTarget {
		t.Errorf("filtering All by the tag: %d %v; want only %s (the peer's private asset carrying it is not the caller's)", status, got, s.tagTarget)
	}

	// The author filter, repeated and comma-separated, returns only that
	// owner's assets the caller can open.
	for _, q := range []url.Values{
		{"scope": {"all"}, "owner": {devPeerEmailAddr}},
		{"scope": {"all"}, "owner": {devPeerEmailAddr, "nobody@example.com"}},
		{"scope": {"all"}, "owner": {devPeerEmailAddr + ",nobody@example.com"}},
	} {
		status, body := s.portalGet(t, "/api/v1/portal/assets", q)
		got := ids2500(t, body, false)
		if status != http.StatusOK || len(got) != 1 || got[0] != s.tagTarget {
			t.Errorf("the author filter %v: %d %v; want only %s", q, status, got, s.tagTarget)
		}
		s.assertNothingHidden(t, "the author-filtered listing", body)
	}

	// The agent's list, with the same filters as JSON arrays.
	_, text, err := s.caller.callRaw("manage_asset", map[string]any{
		"action": "list", "scope": "all", "owners": []any{devPeerEmailAddr}, "tags": []any{s.tag},
	})
	if err != nil || !strings.Contains(text, s.tagTarget) || strings.Contains(text, s.peerHidden) {
		t.Errorf("manage_asset list with owners and tags: %v %.400s", err, text)
	}
	_, text, err = s.caller.callRaw("manage_asset", map[string]any{"action": "list"})
	if err != nil || strings.Contains(text, s.tagTarget) {
		t.Errorf("manage_asset list defaults to the caller's own assets: %v %.300s", err, text)
	}

	// Totals count only what the caller can open.
	_, body = s.portalGet(t, "/api/v1/portal/assets", url.Values{"scope": {"all"}, "limit": {"1"}})
	var page struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal([]byte(body), &page)
	if page.Total != 58 {
		t.Errorf("the All total is %d; want 58 (56 shared, the peer's, the boxed one)", page.Total)
	}
}

func TestIssue2500_FacetsListOnlyWhatTheCallerCanOpen(t *testing.T) {
	s := newIssue2500(t)

	status, body := s.portalGet(t, "/api/v1/portal/assets/facets", url.Values{"scope": {"all"}})
	if status != http.StatusOK {
		t.Fatalf("the facets: %d %.300s", status, body)
	}
	var facets struct {
		Tags []struct {
			Value string
			Count int
		} `json:"tags"`
		Authors []struct {
			Value string
			Count int
		} `json:"authors"`
	}
	if err := json.Unmarshal([]byte(body), &facets); err != nil {
		t.Fatalf("the facets body: %.300s", body)
	}
	tags := map[string]int{}
	for _, f := range facets.Tags {
		tags[f.Value] = f.Count
	}
	if tags[s.tag] != 1 {
		t.Errorf("the tag facet counts %d of %s; want 1 (the peer's private asset carrying it is not counted)", tags[s.tag], s.tag)
	}
	authors := map[string]int{}
	for _, f := range facets.Authors {
		authors[f.Value] = f.Count
	}
	if authors[devOwnerEmail] != 57 || authors[devPeerEmailAddr] != 1 {
		t.Errorf("the author facet is %v; want %s 57 and %s 1", authors, devOwnerEmail, devPeerEmailAddr)
	}
	s.assertNothingHidden(t, "the facets", body)

	status, body = s.portalGet(t, "/api/v1/portal/collections/facets", url.Values{"scope": {"shared"}})
	if status != http.StatusOK || !strings.Contains(body, devOwnerEmail) {
		t.Errorf("the collection author facet: %d %.300s", status, body)
	}

	// The collection shared with the caller is in their Shared collections,
	// and found by search there.
	_, body = s.portalGet(t, "/api/v1/portal/collections", url.Values{"scope": {"shared"}})
	if !strings.Contains(body, fmt.Sprintf("acc2500 collection %d", s.stamp)) {
		t.Errorf("the Shared collections do not list the shared collection: %.300s", body)
	}
	_, body = s.portalGet(t, "/api/v1/portal/collections/search", url.Values{"q": {fmt.Sprintf("acc2500 collection %d", s.stamp)}, "scope": {"shared"}})
	if !strings.Contains(body, fmt.Sprintf("acc2500 collection %d", s.stamp)) {
		t.Errorf("a search in Shared collections did not find it: %.300s", body)
	}
}

// Deleting a collection leaves its share rows in place. A recipient who
// reached an asset only through it opens it, finds it and counts it until the
// collection is deleted, and not after.
func TestIssue2500_ADeletedCollectionGrantsNothing(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	stamp := time.Now().UnixNano()
	persona := fmt.Sprintf("acc-2500-del-%d", stamp)
	role := persona + "_member"
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": persona, "display_name": "Acceptance 2500", "roles": []any{role}, "priority": 10,
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
	})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving persona %s: %d %v", persona, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+persona, http.NoBody) })
	caller := issue2089Key(t, admin, persona+"-caller", role)

	token := fmt.Sprintf("marlinspike%d", stamp)
	saved := owner.call("save_asset", map[string]any{
		"name": "acc2500 boxed " + token, "content_type": "text/html", "content": "<p>boxed</p>", "description": "Boxed " + token + ".",
	})
	assetID, _ := saved["asset_id"].(string)
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})
	coll := owner.call("manage_asset", map[string]any{
		"action": "create_collection", "name": "acc2500 deleted " + token,
		"sections": []any{map[string]any{"title": "Only", "items": []any{map[string]any{"asset_id": assetID}}}},
	})
	collID, _ := coll["collection_id"].(string)
	if status, out := owner.rest(http.MethodPost, "/api/v1/portal/collections/"+collID+"/shares", jsonBody(t, map[string]any{
		"shared_with_persona": persona, "permission": "viewer",
	})); status != http.StatusCreated {
		t.Fatalf("sharing the collection: %d %v", status, out)
	}

	found := func() bool {
		_, body := caller.restText("/api/v1/portal/assets/search?" + url.Values{"q": {token}, "scope": {"shared"}}.Encode())
		return strings.Contains(body, assetID)
	}
	if status, _ := caller.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody); status != http.StatusOK || !found() {
		t.Fatalf("before the delete the caller should open and find the asset: open %d, found %v", status, found())
	}

	if _, text, err := owner.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": collID}); err != nil {
		t.Fatalf("deleting the collection: %v %s", err, text)
	}
	if status, _ := caller.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody); status == http.StatusOK {
		t.Error("the caller still opens an asset reached only through a deleted collection")
	}
	if found() {
		t.Error("the caller's search still finds an asset reached only through a deleted collection")
	}
	if _, text, _ := caller.callRaw("manage_asset", map[string]any{"action": "search", "query": token}); strings.Contains(text, assetID) {
		t.Errorf("manage_asset search still finds it: %.300s", text)
	}
}
