//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Acceptance for #2611: a two-character site name puts "--" in the 3rd and
// 4th characters of every <site>--<env> address, a label DNS reserves and
// certificate authorities refuse. Creating such a site is refused, through the
// portal and through manage_site, with the reason. A site that already has
// such a name (one created before the rule) reports no pages address for its
// environments other than the main one, says why, and its unusable addresses
// are not listed for the ingress.
//
// Wire forms: POST /api/v1/portal/sites takes a JSON object whose name and
// description are strings; GET /api/v1/portal/sites/{site} and GET
// /api/v1/admin/sites/domains take no body. manage_site takes command, name
// and site as strings.

func TestIssue2611_ATwoCharacterSiteIsRefused(t *testing.T) {
	status, got := rest2559(t, keyAdmin2604, http.MethodPost, portal2559, map[string]any{"name": "qz", "description": "Acceptance 2611"})
	if status == http.StatusCreated {
		_, _ = rest2559(t, keyAdmin2604, http.MethodDelete, portal2559+"/qz", nil)
	}
	want2559(t, "a two-character site from the portal", status, http.StatusUnprocessableEntity, got)
	if raw := got["_raw"].(string); !strings.Contains(raw, "cannot be two characters") || !strings.Contains(raw, "qz--dev") {
		t.Errorf("the portal's refusal does not say why: %s", raw)
	}

	res, text, err := connectAs(t, keyAdmin2604).callRaw("manage_site", map[string]any{"command": "create", "name": "qz"})
	if err != nil {
		t.Fatalf("manage_site create: %v", err)
	}
	if !res.IsError || !strings.Contains(text, "cannot be two characters") {
		t.Errorf("manage_site created a two-character site, or did not say why: %s", text)
	}
}

// existingTwoCharacterSite2611 writes a two-character site straight into the
// platform's database, as one created before #2611 would be, with dev on the
// main address and a prod environment, and removes it afterwards.
func existingTwoCharacterSite2611(t *testing.T) string {
	t.Helper()
	db := platformDB2539(t)
	ctx := context.Background()
	name := ""
	for _, c := range "abcdefghijklmnopqrstuvwxyz0123456789" {
		candidate := "y" + string(c)
		var taken bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sites WHERE name = $1)`, candidate).Scan(&taken); err != nil {
			t.Fatalf("reading the sites: %v", err)
		}
		if !taken {
			name = candidate
			break
		}
	}
	if name == "" {
		t.Fatal("every y? site name is taken")
	}
	id := "site_acc2611_" + unique1579()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", firstRunes(q, 60), err)
		}
	}
	exec(`INSERT INTO sites (id, name, owner_email, created_by, main_environment) VALUES ($1, $2, 'admin@example.com', 'admin@example.com', 'dev')`, id, name)
	exec(`INSERT INTO site_environments (site_id, name, updated_by) VALUES ($1, 'dev', 'admin@example.com'), ($1, 'prod', 'admin@example.com')`, id)
	t.Cleanup(func() {
		_, _ = rest2559(t, keyAdmin2604, http.MethodDelete, portal2559+"/"+name, nil)
		cleanup, err := sql.Open("postgres", platformDSN2611())
		if err == nil {
			_, _ = cleanup.ExecContext(context.Background(), `DELETE FROM sites WHERE id = $1`, id)
			_ = cleanup.Close()
		}
	})
	return name
}

// platformDSN2611 is the dev stack's platform database, as platformDB2539
// opens it; a cleanup runs after the test's own handle is closed.
func platformDSN2611() string {
	port := os.Getenv("DEV_PG_PORT")
	if port == "" {
		port = "5432"
	}
	return "postgres://platform:platform_secret@localhost:" + port + "/mcp_platform?sslmode=disable"
}

func TestIssue2611_AnExistingTwoCharacterSiteSaysWhyItHasNoPagesAddress(t *testing.T) {
	name := existingTwoCharacterSite2611(t)

	status, view := rest2559(t, keyAdmin2604, http.MethodGet, portal2559+"/"+name, nil)
	want2559(t, "reading the two-character site", status, http.StatusOK, view)
	pages, _ := view["pages_addresses"].(map[string]any)
	if p, _ := pages["prod"].(string); p != "" {
		t.Errorf("prod's pages address is %q: %s--prod is a label certificate authorities refuse", p, name)
	}
	if dev, _ := pages["dev"].(string); !strings.Contains(dev, "://"+name+"."+pagesDomain2517) {
		t.Errorf("the main address %s.%s is a working address and is not reported: %q", name, pagesDomain2517, dev)
	}
	note, _ := view["address_note"].(string)
	if !strings.Contains(note, "an address like "+name+"--prod."+pagesDomain2517) || !strings.Contains(note, "certificate authorities refuse it") {
		t.Errorf("the site view does not say why: %q", note)
	}

	tool := connectAs(t, keyAdmin2604).call("manage_site", map[string]any{"command": "get", "site": name})
	if tool["address_note"] != note {
		t.Errorf("manage_site get says %q, the portal %q", tool["address_note"], note)
	}

	status, list := rest2559(t, keyAdmin2604, http.MethodGet, "/api/v1/admin/sites/domains", nil)
	want2559(t, "listing the addresses for the ingress", status, http.StatusOK, list)
	raw, _ := list["_raw"].(string)
	if strings.Contains(raw, name+"--") {
		t.Errorf("the ingress list offers %s--<env>, which no certificate can be issued for", name)
	}
	if !strings.Contains(raw, `"`+name+"."+pagesDomain2517+`"`) {
		t.Errorf("the ingress list leaves out the main address %s.%s", name, pagesDomain2517)
	}
}
