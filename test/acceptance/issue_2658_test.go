//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Acceptance for #2658: the Sites list names each site's production address
// (a public domain on the environment holding the main address first, then
// another domain there, then its pages address), the same address the site's
// page and manage_site get name; GET /api/v1/portal/sites searches by name,
// description, owner and domain on the server with total and paging, as
// manage_site list does; and each environment's tile is drawn from the
// release it serves, redrawn when that release changes, and answered only to
// someone who may open the environment.
//
// Wire forms: the portal's site routes take JSON objects of strings and
// booleans and the list's q, limit and offset as query parameters;
// manage_site takes command, site and query as strings and limit and offset
// as JSON numbers, sent as literal tools/call params.

// listed2658 is the portal list's answer for query.
func listed2658(t *testing.T, key, query string) map[string]any {
	t.Helper()
	status, got := rest2559(t, key, http.MethodGet, portal2559+"?"+query, nil)
	want2559(t, "listing sites", status, http.StatusOK, got)
	return got
}

// addressOf2658 is the address the list names for site.
func addressOf2658(t *testing.T, list map[string]any, name string) string {
	t.Helper()
	sites, _ := list["sites"].([]any)
	for _, raw := range sites {
		s, _ := raw.(map[string]any)
		if s["name"] == name {
			addr, _ := s["address"].(string)
			return addr
		}
	}
	t.Fatalf("site %s is not listed: %v", name, list["_raw"])
	return ""
}

// hostOf2658 is an address's hostname, whatever port the stack serves on.
func hostOf2658(addr string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return addr
	}
	return u.Hostname()
}

// TestIssue2658_TheProductionAddressPrefersThePublicDomain: with no domain the
// list names prod's pages address; with only a private domain, that domain;
// with a public and a private one, the public one; the site's page and
// manage_site get name the same.
func TestIssue2658_TheProductionAddressPrefersThePublicDomain(t *testing.T) {
	s := newSite2604(t, keyAdmin2604, "acc2658-")
	q := "q=" + url.QueryEscape(s.name)
	if got := addressOf2658(t, listed2658(t, keyAdmin2604, q), s.name); hostOf2658(got) != s.host("") {
		t.Errorf("with no domain the production address is %q, not the pages address %s", got, s.host(""))
	}
	private := "a." + s.name + ".example.test"
	status, got := s.rest(t, keyAdmin2604, http.MethodPost, "/domains", map[string]any{"hostname": private, "environment": "prod"})
	want2559(t, "adding a private domain", status, http.StatusCreated, got)
	if addr := addressOf2658(t, listed2658(t, keyAdmin2604, q), s.name); hostOf2658(addr) != private {
		t.Errorf("with only a private domain the production address is %q", addr)
	}
	public := "www." + s.name + ".example.test"
	status, got = s.rest(t, keyAdmin2604, http.MethodPost, "/domains", map[string]any{"hostname": public, "environment": "prod", "public": true})
	want2559(t, "adding a public domain", status, http.StatusCreated, got)
	list := listed2658(t, keyAdmin2604, q)
	if addr := addressOf2658(t, list, s.name); hostOf2658(addr) != public {
		t.Errorf("with a public and a private domain the production address is %q", addr)
	}
	sites, _ := list["sites"].([]any)
	if first, _ := sites[0].(map[string]any); first["more_domains"] != float64(1) {
		t.Errorf("the other domain on prod is not counted: %v", first["more_domains"])
	}
	status, page := s.rest(t, keyAdmin2604, http.MethodGet, "", nil)
	want2559(t, "reading the site", status, http.StatusOK, page)
	if addr, _ := page["address"].(string); hostOf2658(addr) != public {
		t.Errorf("the site's page names %q", addr)
	}
	tool := connectAs(t, keyAdmin2604).call("manage_site", map[string]any{"command": "get", "site": s.name})
	if addr, _ := tool["production_address"].(string); hostOf2658(addr) != public {
		t.Errorf("manage_site get names %q", addr)
	}
}

// TestIssue2658_TheListSearchesAndPagesOnTheServer: q naming a domain returns
// that site and no other, total counting every match; limit and offset page
// the result; manage_site list takes the same query.
func TestIssue2658_TheListSearchesAndPagesOnTheServer(t *testing.T) {
	s := newSite2604(t, keyAdmin2604, "acc2658-q-")
	newSite2604(t, keyAdmin2604, "acc2658-q-")
	domain := s.name + ".example.test"
	status, got := s.rest(t, keyAdmin2604, http.MethodPost, "/domains", map[string]any{"hostname": domain, "environment": "prod"})
	want2559(t, "adding a domain", status, http.StatusCreated, got)

	found := listed2658(t, keyAdmin2604, "q="+url.QueryEscape(domain))
	if found["total"] != float64(1) || addressOf2658(t, found, s.name) == "" {
		t.Errorf("q=%s: total %v, %v", domain, found["total"], found["_raw"])
	}
	all := listed2658(t, keyAdmin2604, "q=acc2658-q-")
	total, _ := all["total"].(float64)
	if total < 2 {
		t.Fatalf("both sites are not found by name: %v", all["total"])
	}
	page := listed2658(t, keyAdmin2604, "q=acc2658-q-&limit=1&offset=1")
	if page["total"] != total {
		t.Errorf("total moved with the page: %v vs %v", page["total"], total)
	}
	if sites, _ := page["sites"].([]any); len(sites) != 1 {
		t.Errorf("limit=1 answered %d sites", len(sites))
	}
	tool := connectAs(t, keyAdmin2604).call("manage_site", map[string]any{"command": "list", "query": domain, "limit": 10, "offset": 0})
	if tool["total"] != float64(1) {
		t.Errorf("manage_site list query=%s: %v", domain, tool["total"])
	}
}

// tile2658 waits up to two minutes for env's tile and returns its status.
func tile2658(t *testing.T, s *site2604, key, env string, want int) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		status, _ := s.rest(t, key, http.MethodGet, "/environments/"+env+"/tile", nil)
		if status == want || time.Now().After(deadline) {
			return status
		}
		time.Sleep(time.Second)
	}
}

// TestIssue2658_EachEnvironmentHasItsOwnTile: after publishing to dev and
// promoting to qa and prod, every environment answers a tile; a new release
// on prod is drawn anew; a person who may not open dev gets none for it.
func TestIssue2658_EachEnvironmentHasItsOwnTile(t *testing.T) {
	s := newSite2604(t, keyAdmin2604, "acc2658-tile-")
	s.setEnv(t, keyAdmin2604, "qa", map[string]any{})
	s.publish(t, keyAdmin2604, "one")
	agentsMayPromote2643(t, s.name, "qa")
	agentsMayPromote2643(t, s.name, "prod")
	s.promote(t, "dev", "qa")
	s.promote(t, "qa", "prod")
	for _, env := range []string{"dev", "qa", "prod"} {
		if status := tile2658(t, s, keyAdmin2604, env, http.StatusOK); status != http.StatusOK {
			t.Errorf("%s has no tile within the bound: %d", env, status)
		}
	}

	s.publish(t, keyAdmin2604, "two")
	s.promote(t, "dev", "qa")
	s.promote(t, "qa", "prod")
	if status := tile2658(t, s, keyAdmin2604, "prod", http.StatusOK); status != http.StatusOK {
		t.Errorf("prod's new release was not drawn: %d", status)
	}

	s.share(t, keyAdmin2604, map[string]any{"email": emailPeer2604, "level": "view", "environments": []string{"prod"}})
	viewer := keyPeer2604
	if status, _ := s.rest(t, viewer, http.MethodGet, "/environments/dev/tile", nil); status != http.StatusNotFound {
		t.Errorf("a person who may not open dev got its tile: %d", status)
	}
	if status := tile2658(t, s, viewer, "prod", http.StatusOK); status != http.StatusOK {
		t.Errorf("a person who may open prod got no tile for it: %d", status)
	}
}
