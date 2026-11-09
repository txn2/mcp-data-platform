//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2578: the site access routes held an email in the path, and the API
// gateway refuses a path holding "@", so they could not be called through it.
// The email is now in the PUT body and the DELETE query string.
//
// Wire forms: api_invoke_endpoint's body is sent as a JSON object and as a
// string of JSON; path_params and query_params are objects of strings. The
// portal's browser client sends the PUT body as JSON and the DELETE email
// URL-encoded.

const (
	email2578   = "gateway.2578@example.com"
	opGrant2578 = "PUT /portal/sites/{site}/access"
	opDrop2578  = "DELETE /portal/sites/{site}/access"
	admin2578   = "platform-admin"
	why2578     = "Acceptance #2578: site access through the API gateway."
)

func hasGrant2578(t *testing.T, s *site2559, email string) bool {
	t.Helper()
	status, got := rest2559(t, keyAdmin2559, http.MethodGet, s.path("/access"), nil)
	want2559(t, "reading access", status, http.StatusOK, got)
	grants, _ := got["grants"].([]any)
	for _, g := range grants {
		if row, _ := g.(map[string]any); row["email"] == email {
			return true
		}
	}
	return false
}

func TestIssue2578_TheGatewayGrantsAndRevokesARealAddress(t *testing.T) {
	asObject := map[string]any{"email": email2578, "level": "view"}
	asJSON, err := json.Marshal(asObject)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]any{"body as an object": asObject, "body as a string of JSON": string(asJSON)} {
		t.Run(name, func(t *testing.T) {
			s := newSite2559(t, keyAdmin2559)
			c := connectAs(t, keyAdmin2559)
			c.call("api_invoke_endpoint", map[string]any{
				"connection": admin2578, "operation_id": opGrant2578, "path_params": map[string]any{"site": s.name},
				"body": body, "purpose": why2578,
			})
			if !hasGrant2578(t, s, email2578) {
				t.Fatalf("the grant through the gateway did not land")
			}
			c.call("api_invoke_endpoint", map[string]any{
				"connection": admin2578, "operation_id": opDrop2578, "path_params": map[string]any{"site": s.name},
				"query_params": map[string]any{"email": email2578}, "purpose": why2578,
			})
			if hasGrant2578(t, s, email2578) {
				t.Fatalf("the revoke through the gateway did not land")
			}
		})
	}
}

func TestIssue2578_ThePathGuardStillRefusesAnAt(t *testing.T) {
	c := connectAs(t, keyAdmin2559)
	res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": admin2578, "method": "GET", "path": "/portal/sites/a@b.example/access", "purpose": why2578,
	})
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	if !res.IsError || !strings.Contains(text, "disallowed character") {
		t.Fatalf("a path holding @ reached the upstream: isError=%v %s", res.IsError, text)
	}
}

func TestIssue2578_ARevokeNamingNobodyIsRefused(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.grant(t, keyEngineer2559, "kept@example.com", "view")
	status, got := rest2559(t, keyEngineer2559, http.MethodDelete, s.path("/access"), nil)
	want2559(t, "a revoke with no email", status, http.StatusBadRequest, got)
	if !hasGrant2578(t, s, "kept@example.com") {
		t.Errorf("a revoke naming nobody removed a grant")
	}
}

func TestIssue2578_AGrantNamingNobodyIsRefused(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	status, got := rest2559(t, keyEngineer2559, http.MethodPut, s.path("/access"), map[string]any{"level": "view"})
	want2559(t, "a grant with no email", status, http.StatusUnprocessableEntity, got)
	if hasGrant2578(t, s, "") {
		t.Errorf("a grant naming nobody was stored")
	}
}

func TestIssue2578_ThePortalGrantsAndRevokesThroughTheNewRoute(t *testing.T) {
	s := newSite2559(t, keyAdmin2559)
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/site-signin/playwright.config.ts", "--grep", "access panel")
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(),
		"SITE_SIGNIN_BASE_URL="+baseURL(),
		"SITE_ACCESS_SITE="+s.name,
		"SITE_ACCESS_API_KEY="+keyAdmin2559,
		"SITE_ACCESS_EMAIL="+email2578,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright against %s:\n%s", baseURL(), out)
	if err != nil {
		t.Fatalf("the access panel case failed: %v", err)
	}
}
