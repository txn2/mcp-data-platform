//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq" // the acceptance fixture writes one pre-fix row directly
)

// Issue #1682: a connection could carry both OAuth config vocabularies at once.
// Nothing rejected it, nothing normalized it, and nothing told the operator.
// ParseConfig resolves per field with the canonical key winning, so a
// connection holding both oauth_client_id and oauth2_client_id authenticated
// with the canonical one and silently ignored the value the operator had typed
// into the portal, while every surface reported it as fully configured.
//
// The criteria below are the four requested fixes as an operator meets them:
// a write in the legacy vocabulary is persisted canonical, a write carrying
// both with disagreeing values is refused naming both keys, the portal editor
// no longer carries the rival vocabulary through a save, and a row that
// predates the check reports the collision where the operator looks.
//
// Wire forms: these are admin REST routes with typed request bodies, so each
// field admits one JSON form -- except the scope, whose two shapes are the
// whole point: the canonical oauth_scope (a space-delimited string) and the
// legacy oauth2_scopes (an array of strings) are both sent as literal request
// bodies below, and both are asserted to leave one space-delimited string in
// the stored row.

const issue1682Purpose = "Acceptance for #1682: one OAuth config vocabulary is ever persisted, and a row carrying two says so."

func issue1682Name(label string) string {
	return fmt.Sprintf("acc-1682-%s-%d", label, time.Now().UnixNano())
}

// issue1682Put saves a config under an api connection name and removes the
// connection when the test ends, returning the status and body so a criterion
// about a refusal reads them.
func issue1682Put(t *testing.T, c *client, name string, cfg map[string]any) (int, map[string]any) {
	t.Helper()
	path := "/api/v1/admin/connection-instances/api/" + name
	t.Cleanup(func() { c.rest(http.MethodDelete, path, http.NoBody) })
	return c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config":      cfg,
		"description": issue1682Purpose,
	}))
}

func issue1682Config(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading %s back: HTTP %d", name, status)
	}
	cfg, ok := body["config"].(map[string]any)
	if !ok {
		t.Fatalf("%s carries no config: %v", name, body)
	}
	return cfg
}

// TestIssue1682_ALegacyWriteIsPersistedInTheCanonicalVocabulary is the fix that
// makes migration 000050 continuous rather than one-shot: whatever vocabulary a
// caller writes in, one vocabulary is stored.
func TestIssue1682_ALegacyWriteIsPersistedInTheCanonicalVocabulary(t *testing.T) {
	c := connect(t)
	name := issue1682Name("legacy")
	status, body := issue1682Put(t, c, name, map[string]any{
		"base_url":                   "https://analytics.example.com",
		"auth_mode":                  "oauth2_authorization_code",
		"oauth2_authorization_url":   "https://idp.example.com/authorize",
		"oauth2_token_url":           "https://idp.example.com/token",
		"oauth2_client_id":           "platform-client",
		"oauth2_client_secret":       "acceptance-1682-secret",
		"oauth2_scopes":              []any{"analytics.readonly", "openid"},
		"oauth2_endpoint_auth_style": "params",
		"oauth2_prompt":              "consent",
		"connection_name":            name,
	})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving a legacy-vocabulary connection: HTTP %d (%v)", status, body)
	}

	cfg := issue1682Config(t, c, name)
	for key, want := range map[string]string{
		"auth_mode":                 "oauth",
		"oauth_grant":               "authorization_code",
		"oauth_authorization_url":   "https://idp.example.com/authorize",
		"oauth_token_url":           "https://idp.example.com/token",
		"oauth_client_id":           "platform-client",
		"oauth_endpoint_auth_style": "params",
		"oauth_prompt":              "consent",
		// The legacy array becomes the canonical space-delimited string.
		"oauth_scope": "analytics.readonly openid",
	} {
		if cfg[key] != want {
			t.Errorf("config[%s] = %v, want %q", key, cfg[key], want)
		}
	}
	for _, legacy := range []string{
		"oauth2_authorization_url", "oauth2_token_url", "oauth2_client_id",
		"oauth2_client_secret", "oauth2_scopes", "oauth2_endpoint_auth_style", "oauth2_prompt",
	} {
		if _, present := cfg[legacy]; present {
			t.Errorf("the legacy key %s survived the write", legacy)
		}
	}
}

// TestIssue1682_TheCanonicalScopeStringIsStoredAsWritten is the other form the
// scope arrives in. Both forms are sent as literal request bodies; both leave
// one space-delimited string.
func TestIssue1682_TheCanonicalScopeStringIsStoredAsWritten(t *testing.T) {
	c := connect(t)
	name := issue1682Name("scope")
	status, body := issue1682Put(t, c, name, map[string]any{
		"base_url":            "https://analytics.example.com",
		"auth_mode":           "oauth",
		"oauth_grant":         "client_credentials",
		"oauth_token_url":     "https://idp.example.com/token",
		"oauth_client_id":     "platform-client",
		"oauth_client_secret": "acceptance-1682-secret",
		"oauth_scope":         "analytics.readonly openid",
		"connection_name":     name,
	})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving a canonical connection: HTTP %d (%v)", status, body)
	}

	if got := issue1682Config(t, c, name)["oauth_scope"]; got != "analytics.readonly openid" {
		t.Errorf("oauth_scope = %v, want the string as written", got)
	}
}

// TestIssue1682_DisagreeingVocabulariesAreRefusedNamingBothKeys is the change
// that would have prevented the incident. Both values were authored
// deliberately and only the operator knows which is current, so no rule picks
// one: the write is refused, naming the pair and neither value.
func TestIssue1682_DisagreeingVocabulariesAreRefusedNamingBothKeys(t *testing.T) {
	c := connect(t)
	name := issue1682Name("conflict")
	status, body := issue1682Put(t, c, name, map[string]any{
		"base_url":                "https://analytics.example.com",
		"auth_mode":               "oauth",
		"oauth_grant":             "authorization_code",
		"oauth_authorization_url": "https://idp.example.com/authorize",
		"oauth_token_url":         "https://idp.example.com/token",
		"oauth_client_id":         "PENDING-REPLACE-ME.apps.googleusercontent.com",
		"oauth_client_secret":     "acceptance-1682-secret",
		"oauth2_client_id":        "986495125425.apps.googleusercontent.com",
		"connection_name":         name,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("a write carrying two disagreeing client ids: HTTP %d, want 400 (%v)", status, body)
	}
	detail, _ := body["detail"].(string)
	for _, want := range []string{"oauth_client_id", "oauth2_client_id"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not name %s: %s", want, detail)
		}
	}
	for _, secret := range []string{"PENDING-REPLACE-ME", "986495125425"} {
		if strings.Contains(detail, secret) {
			t.Errorf("the refusal repeats a configured value (%s): %s", secret, detail)
		}
	}
	// Nothing was stored: the connection does not exist.
	if got, _ := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+name, http.NoBody); got != http.StatusNotFound {
		t.Errorf("reading the refused connection back: HTTP %d, want 404", got)
	}
}

// TestIssue1682_AgreeingVocabulariesCollapseToOne is the resolvable half. Every
// sensitive value reaches the API as the literal mask, so a mixed row re-saved
// untouched arrives with both secret keys holding the same placeholder; that is
// not a conflict and must not be refused.
func TestIssue1682_AgreeingVocabulariesCollapseToOne(t *testing.T) {
	c := connect(t)
	name := issue1682Name("agree")
	status, body := issue1682Put(t, c, name, map[string]any{
		"base_url":            "https://analytics.example.com",
		"auth_mode":           "oauth",
		"oauth_grant":         "client_credentials",
		"oauth_token_url":     "https://idp.example.com/token",
		"oauth_client_id":     "platform-client",
		"oauth2_client_id":    "platform-client",
		"oauth_client_secret": "acceptance-1682-secret",
		"connection_name":     name,
	})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving a config whose two vocabularies agree: HTTP %d (%v)", status, body)
	}

	cfg := issue1682Config(t, c, name)
	if cfg["oauth_client_id"] != "platform-client" {
		t.Errorf("oauth_client_id = %v, want platform-client", cfg["oauth_client_id"])
	}
	if _, present := cfg["oauth2_client_id"]; present {
		t.Error("the legacy key survived a write whose two vocabularies agreed")
	}
}

// issue1682DevDSN is where the dev stack's database listens. dev/start.sh
// relocates the stack when the default ports are busy and records the ports in
// dev/.dev-ports.env, which `make acceptance` sources into the environment.
func issue1682DevDSN() string {
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn
	}
	port := os.Getenv("DEV_PG_PORT")
	if port == "" {
		port = "5432"
	}
	return "postgres://platform:platform_secret@localhost:" + port + "/mcp_platform?sslmode=disable"
}

// issue1682PreFixRow writes one connection row carrying both vocabularies
// straight into the platform's database, and removes it afterwards.
//
// That is the only way such a row can exist once the write boundary refuses to
// produce one, and it is exactly the row the incident left behind: a deployment
// upgraded from a release before this check still holds one, and the operator
// has to be able to see which half of it is live.
func issue1682PreFixRow(t *testing.T, name string, cfg map[string]any) {
	t.Helper()
	dsn := issue1682DevDSN()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("opening the dev database at %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("the dev database does not answer at %s (%v). The suite needs the local stack: `make dev`", dsn, err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encoding the fixture config: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO connection_instances (kind, name, config, description, created_by)
		 VALUES ('api', $1, $2, $3, 'acceptance-1682')`,
		name, raw, issue1682Purpose); err != nil {
		t.Fatalf("planting the pre-fix row: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx,
			`DELETE FROM connection_instances WHERE kind = 'api' AND name = $1`, name); err != nil {
			t.Logf("removing the pre-fix row: %v", err)
		}
	})
}

// TestIssue1682_ARowCarryingBothVocabulariesReportsTheCollision is the surface
// an operator triaging the failing connection reaches: the status endpoint the
// portal's OAuth card reads. It reported the shape it resolved and nothing
// about the value it was ignoring, which is how the incident cost an afternoon.
// What the portal then draws from it -- the struck-through values and the card's
// warning -- is executed in the browser by
// ui/e2e/interactive/connections-oauth.spec.ts.
func TestIssue1682_ARowCarryingBothVocabulariesReportsTheCollision(t *testing.T) {
	c := connect(t)
	name := issue1682Name("mixed")
	issue1682PreFixRow(t, name, map[string]any{
		"base_url":                "https://analytics.example.com",
		"auth_mode":               "oauth",
		"oauth_grant":             "authorization_code",
		"oauth_authorization_url": "https://idp.example.com/authorize",
		"oauth_token_url":         "https://idp.example.com/token",
		"oauth_client_id":         "PENDING-REPLACE-ME.apps.googleusercontent.com",
		"oauth_client_secret":     "acceptance-1682-canonical",
		"oauth_scope":             "analytics.readonly",
		"oauth2_client_id":        "986495125425.apps.googleusercontent.com",
		"oauth2_client_secret":    "acceptance-1682-legacy",
		"oauth2_scopes":           []any{"analytics.readonly"},
		"connection_name":         name,
	})

	status, body := c.rest(http.MethodGet,
		"/api/v1/admin/connections/api/"+name+"/oauth-status", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("oauth-status: HTTP %d (%v)", status, body)
	}
	if body["config_vocabulary"] != "mixed" {
		t.Errorf("config_vocabulary = %v, want mixed", body["config_vocabulary"])
	}
	shadowed, _ := body["shadowed_config_keys"].([]any)
	got := make([]string, 0, len(shadowed))
	for _, key := range shadowed {
		s, _ := key.(string)
		got = append(got, s)
	}
	want := []string{"oauth2_client_id", "oauth2_client_secret", "oauth2_scopes"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("shadowed_config_keys = %v, want %v", got, want)
	}
}
