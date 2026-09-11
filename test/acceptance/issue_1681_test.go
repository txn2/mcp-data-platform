//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1681: a canonical auth_mode="oauth" API connection rendered in the
// portal with no auth configuration at all. The editor still spoke the
// pre-#408 oauth2_* vocabulary while migration 000050 had rewritten every
// api-kind OAuth row onto auth_mode "oauth" plus oauth_grant, so the mode
// select matched nothing and the whole OAuth field block was skipped.
//
// The portal half of that is executed in the browser, against the assembled
// app: ui/e2e/interactive/connections-oauth.spec.ts opens the canonical
// connection, clicks Edit, and asserts every stored value in its field. What
// runs here is the half a server answers: that the canonical shape the ticket
// reports is accepted and read back in the same vocabulary the editor now
// writes, that the OAuth status the portal's card reads agrees with it, and
// that a refusal names the mode and grant the connection actually carries
// rather than the client_credentials mode it hard-coded.
//
// Wire forms: these criteria are admin REST routes, whose bodies are typed
// structs (setConnectionInstanceRequest), so each field admits one JSON form.
// The config map's values are the operator's own strings; oauth_scope is sent
// as the space-delimited string the canonical schema states, and the legacy
// array form is covered by #1682's criteria, which send it and assert what is
// persisted.

const issue1681Purpose = "Acceptance for #1681: a canonical OAuth connection round-trips in the vocabulary the portal editor speaks."

// issue1681Canonical is the connection from the ticket, in the shape migration
// 000050 produces and the admin API accepts.
func issue1681Canonical(name string) map[string]any {
	return map[string]any{
		"base_url":                  "https://analyticsdata.googleapis.com",
		"auth_mode":                 "oauth",
		"oauth_grant":               "authorization_code",
		"oauth_authorization_url":   "https://accounts.google.com/o/oauth2/v2/auth",
		"oauth_token_url":           "https://oauth2.googleapis.com/token",
		"oauth_client_id":           "986495125425.apps.googleusercontent.com",
		"oauth_client_secret":       "acceptance-1681-secret",
		"oauth_scope":               "https://www.googleapis.com/auth/analytics.readonly",
		"oauth_endpoint_auth_style": "params",
		"oauth_prompt":              "consent",
		"connection_name":           name,
		"connect_timeout":           "5s",
		"call_timeout":              "10s",
		"trust_level":               "untrusted",
	}
}

// issue1681Register saves one api connection and removes it when the test ends.
// The status reported by the save is returned so a criterion about a refusal
// reads it rather than failing here.
func issue1681Register(t *testing.T, c *client, name string, cfg map[string]any) (int, map[string]any) {
	t.Helper()
	path := "/api/v1/admin/connection-instances/api/" + name
	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config":      cfg,
		"description": issue1681Purpose,
	}))
	t.Cleanup(func() { c.rest(http.MethodDelete, path, http.NoBody) })
	return status, body
}

func issue1681Name(label string) string {
	return fmt.Sprintf("acc-1681-%s-%d", label, time.Now().UnixNano())
}

// TestIssue1681_CanonicalConnectionRoundTripsInOneVocabulary is the criterion
// the portal editor depends on: what it writes is what comes back, so opening a
// saved connection fills the same fields that saved it.
func TestIssue1681_CanonicalConnectionRoundTripsInOneVocabulary(t *testing.T) {
	c := connect(t)
	name := issue1681Name("canonical")
	status, _ := issue1681Register(t, c, name, issue1681Canonical(name))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the canonical connection: HTTP %d", status)
	}

	got, body := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	if got != http.StatusOK {
		t.Fatalf("reading the connection back: HTTP %d", got)
	}
	cfg, ok := body["config"].(map[string]any)
	if !ok {
		t.Fatalf("the connection carries no config: %v", body)
	}
	for key, want := range map[string]string{
		"auth_mode":                 "oauth",
		"oauth_grant":               "authorization_code",
		"oauth_authorization_url":   "https://accounts.google.com/o/oauth2/v2/auth",
		"oauth_token_url":           "https://oauth2.googleapis.com/token",
		"oauth_client_id":           "986495125425.apps.googleusercontent.com",
		"oauth_scope":               "https://www.googleapis.com/auth/analytics.readonly",
		"oauth_endpoint_auth_style": "params",
		"oauth_prompt":              "consent",
	} {
		if cfg[key] != want {
			t.Errorf("config[%s] = %v, want %q", key, cfg[key], want)
		}
	}
	// The secret comes back masked, which is what the editor re-submits.
	if cfg["oauth_client_secret"] != "[REDACTED]" {
		t.Errorf("oauth_client_secret = %v, want the mask", cfg["oauth_client_secret"])
	}
	for _, legacy := range []string{
		"oauth2_token_url", "oauth2_authorization_url", "oauth2_client_id",
		"oauth2_client_secret", "oauth2_scopes", "oauth2_endpoint_auth_style", "oauth2_prompt",
	} {
		if _, present := cfg[legacy]; present {
			t.Errorf("the stored config carries the legacy key %s", legacy)
		}
	}
}

// TestIssue1681_OAuthStatusAgreesWithTheStoredVocabulary is the other half of
// what the portal reads for such a connection: the status card's source. The
// API and the UI disagreeing about one connection is what the ticket reports.
func TestIssue1681_OAuthStatusAgreesWithTheStoredVocabulary(t *testing.T) {
	c := connect(t)
	name := issue1681Name("status")
	if status, _ := issue1681Register(t, c, name, issue1681Canonical(name)); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the canonical connection: HTTP %d", status)
	}

	status, body := c.rest(http.MethodGet,
		"/api/v1/admin/connections/api/"+name+"/oauth-status", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("oauth-status: HTTP %d (%v)", status, body)
	}
	if body["configured"] != true {
		t.Errorf("configured = %v, want true", body["configured"])
	}
	if body["grant"] != "authorization_code" {
		t.Errorf("grant = %v, want authorization_code", body["grant"])
	}
	if body["config_vocabulary"] != "canonical" {
		t.Errorf("config_vocabulary = %v, want canonical", body["config_vocabulary"])
	}
	if shadowed, present := body["shadowed_config_keys"]; present {
		t.Errorf("a canonical connection reports shadowed keys: %v", shadowed)
	}
}

// TestIssue1681_RefusalNamesTheModeAndGrantTheConnectionCarries covers the
// ticket's secondary defect. An authorization_code connection saved without a
// client id reported that oauth2.client_id was required when auth_mode was
// "oauth2_client_credentials" -- two names the connection does not carry, which
// sent the operator looking for a misconfiguration that did not exist.
func TestIssue1681_RefusalNamesTheModeAndGrantTheConnectionCarries(t *testing.T) {
	c := connect(t)
	name := issue1681Name("refusal")
	cfg := issue1681Canonical(name)
	delete(cfg, "oauth_client_id")

	status, body := issue1681Register(t, c, name, cfg)
	if status != http.StatusBadRequest {
		t.Fatalf("saving a connection with no client id: HTTP %d, want 400 (%v)", status, body)
	}
	detail, _ := body["detail"].(string)
	for _, want := range []string{
		"oauth_client_id is required",
		`auth_mode is "oauth"`,
		`oauth_grant is "authorization_code"`,
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not say %q: %s", want, detail)
		}
	}
	if strings.Contains(detail, "oauth2_client_credentials") {
		t.Errorf("the refusal names a mode the connection does not carry: %s", detail)
	}
}
