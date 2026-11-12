//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2607: the portal's site view addresses an environment that
// has a domain by its domain, reports the composed <site>--<env> address apart
// under pages_addresses, and answers exactly what manage_site get answers for
// the same site.
//
// Wire forms: GET /api/v1/portal/sites/{site} takes no body. PUT
// .../environments/{name} takes a JSON object (here empty); POST .../domains
// takes a JSON object whose hostname and environment are strings and public a
// boolean. manage_site takes command and site as strings.

func TestIssue2607_ThePortalAddressesAnEnvironmentByItsDomain(t *testing.T) {
	s := newSite2604(t, keyAdmin2604, "acc2607-")
	domain := s.name + ".example.test"
	s.setEnv(t, keyAdmin2604, "prod", map[string]any{})
	status, got := s.rest(t, keyAdmin2604, http.MethodPost, "/domains", map[string]any{"hostname": domain, "environment": "prod", "public": true})
	want2559(t, "adding prod's domain", status, http.StatusCreated, got)

	status, view := s.rest(t, keyAdmin2604, http.MethodGet, "", nil)
	want2559(t, "reading the site", status, http.StatusOK, view)
	addresses, _ := view["addresses"].(map[string]any)
	pages, _ := view["pages_addresses"].(map[string]any)
	prod, _ := addresses["prod"].(string)
	if !strings.Contains(prod, "://"+domain) || strings.Contains(prod, "--prod") {
		t.Errorf("prod's address is %q, not its domain %s", prod, domain)
	}
	// prod holds the main address, so that is its pages address; dev answers
	// at its own (#2617).
	if p, _ := pages["prod"].(string); hostname2617(p) != s.host("") {
		t.Errorf("prod's pages address is %q, not the main address %s", p, s.host(""))
	}
	if dev, _ := addresses["dev"].(string); hostname2617(dev) != s.host("dev") {
		t.Errorf("dev, with no domain, is addressed %q, not at its own address %s", dev, s.host("dev"))
	}
	if _, has := view["address_note"]; has {
		t.Errorf("a site whose addresses answer carries an address note: %v", view["address_note"])
	}

	tool := connectAs(t, keyAdmin2604).call("manage_site", map[string]any{"command": "get", "site": s.name})
	if asJSON(tool["addresses"]) != asJSON(view["addresses"]) {
		t.Errorf("manage_site get and the portal disagree on addresses:\n  tool   %s\n  portal %s", asJSON(tool["addresses"]), asJSON(view["addresses"]))
	}
	if asJSON(tool["pages_addresses"]) != asJSON(view["pages_addresses"]) {
		t.Errorf("manage_site get and the portal disagree on pages addresses:\n  tool   %s\n  portal %s", asJSON(tool["pages_addresses"]), asJSON(view["pages_addresses"]))
	}
}
