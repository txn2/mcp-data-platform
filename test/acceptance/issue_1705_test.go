//go:build integration

package acceptance

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1705: POST /api/v1/admin/auth/keys accepted a roles list no persona
// carries. The key it minted authenticated, initialized, and listed zero tools,
// and nothing at creation, in the key listing, or on the portal's API keys page
// said why. An operator who typed a persona's name where its role goes found
// out from an agent reporting an empty tool list.
//
// What these hold, through the admin REST routes the portal's API keys page
// calls: a key whose roles reach no persona is still created, and the creation
// answer carries a warning naming the roles the personas do carry; the listing
// flags the same key; a key whose role a persona carries names that persona and
// warns of nothing; and the warning is true, because the key really does list
// no tools over the MCP wire.
//
// Wire forms: the create route decodes its body strictly into
// authKeyCreateRequest, whose `name` and `expires_in` are strings and whose
// `roles` is a []string, so each admits exactly one JSON form: `name` and
// `expires_in` are sent as JSON strings and `roles` as a JSON array of strings,
// as literal request bytes.

const issue1705UnmappedRole = "issue-1705-no-persona-carries-this"

// issue1705CreateBody is the literal body sent to the create route. It is
// written as bytes rather than marshaled so the wire form is the one named in
// the header comment and nothing a Go encoder decides.
func issue1705CreateBody(name, rolesJSON string) *bytes.Reader {
	return bytes.NewReader([]byte(`{"name":"` + name + `","roles":` + rolesJSON + `,"expires_in":"1h"}`))
}

// issue1705Create mints a key, registers its deletion, and returns the decoded
// creation answer.
func issue1705Create(t *testing.T, c *client, name, rolesJSON string) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodPost, "/api/v1/admin/auth/keys", issue1705CreateBody(name, rolesJSON))
	if status != http.StatusCreated {
		t.Fatalf("POST /api/v1/admin/auth/keys roles=%s: status %d, body %v", rolesJSON, status, out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, nil) })
	return out
}

// issue1705Listed returns the listing entry for one key.
func issue1705Listed(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/admin/auth/keys", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/auth/keys: status %d, body %v", status, out)
	}
	keys, _ := out["keys"].([]any)
	for _, k := range keys {
		entry, _ := k.(map[string]any)
		if entry["name"] == name {
			return entry
		}
	}
	t.Fatalf("the listing does not carry key %q: %v", name, out)
	return nil
}

// issue1705Warnings reads a `warnings` array as strings.
func issue1705Warnings(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, w := range raw {
		if s, ok := w.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestIssue1705_ACreatedKeyWhoseRolesReachNoPersonaCarriesAWarning is the
// ticket's first criterion: creation answers 201 with a warning that names the
// role that reached nothing and the roles the personas carry, so the operator
// reads the mistake in the same answer that hands them the key.
func TestIssue1705_ACreatedKeyWhoseRolesReachNoPersonaCarriesAWarning(t *testing.T) {
	c := connect(t)
	out := issue1705Create(t, c, "issue-1705-unmapped", `["`+issue1705UnmappedRole+`"]`)

	if p, _ := out["persona"].(string); p != "" {
		t.Errorf("persona = %q; a key whose roles no persona carries resolves to none", p)
	}
	warnings := issue1705Warnings(out["warnings"])
	if len(warnings) == 0 {
		t.Fatalf("the creation answer carries no warnings for a key that reaches no persona: %v", out)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, issue1705UnmappedRole) {
		t.Errorf("the warning does not name the role that reached nothing:\n%s", joined)
	}
	// The dev stack's admin persona carries the role the suite's own key holds.
	if !strings.Contains(joined, "admin") {
		t.Errorf("the warning does not name the roles the personas carry:\n%s", joined)
	}
	if _, ok := out["key"].(string); !ok {
		t.Errorf("the key was not handed back: %v", out)
	}
}

// TestIssue1705_TheListingFlagsAKeyWhoseRolesReachNoPersona is the second
// criterion: the flag outlives the creation answer, so a key created before
// this release, or one whose persona was later removed, is visible too.
func TestIssue1705_TheListingFlagsAKeyWhoseRolesReachNoPersona(t *testing.T) {
	c := connect(t)
	name := "issue-1705-listed-unmapped"
	issue1705Create(t, c, name, `["`+issue1705UnmappedRole+`"]`)

	entry := issue1705Listed(t, c, name)
	if entry["no_persona"] != true {
		t.Errorf("no_persona = %v; want true for a key whose roles no persona carries: %v", entry["no_persona"], entry)
	}
	if p, _ := entry["persona"].(string); p != "" {
		t.Errorf("persona = %q; want none: %v", p, entry)
	}
}

// TestIssue1705_AKeyWhoseRoleAPersonaCarriesNamesThatPersona is the negative
// half: the warning is about a mistake, so a correct key carries none, and both
// the creation answer and the listing say which persona it acts as.
func TestIssue1705_AKeyWhoseRoleAPersonaCarriesNamesThatPersona(t *testing.T) {
	c := connect(t)
	name := "issue-1705-mapped"
	out := issue1705Create(t, c, name, `["admin"]`)

	if p, _ := out["persona"].(string); p != "admin" {
		t.Errorf("persona = %q; want admin, the dev persona carrying the admin role: %v", p, out)
	}
	if w := issue1705Warnings(out["warnings"]); len(w) != 0 {
		t.Errorf("a key whose role a persona carries was warned: %v", w)
	}
	entry := issue1705Listed(t, c, name)
	if entry["no_persona"] == true {
		t.Errorf("the listing flags a key that reaches a persona: %v", entry)
	}
	if p, _ := entry["persona"].(string); p != "admin" {
		t.Errorf("listing persona = %q; want admin: %v", p, entry)
	}
}

// TestIssue1705_TheWarningIsTrue proves the sentence the operator is shown: the
// flagged key authenticates and initializes, and tools/list answers empty. The
// session is opened directly rather than through connect, which calls
// platform_info and would fail on exactly the empty inventory being asserted.
func TestIssue1705_TheWarningIsTrue(t *testing.T) {
	c := connect(t)
	out := issue1705Create(t, c, "issue-1705-empty-inventory", `["`+issue1705UnmappedRole+`"]`)
	secret, _ := out["key"].(string)
	if secret == "" {
		t.Fatalf("no key value in the creation answer: %v", out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpClient := &http.Client{Transport: authRoundTripper{key: secret, base: http.DefaultTransport}}
	mc := mcp.NewClient(&mcp.Implementation{Name: "acceptance-1705", Version: "1.0.0"}, nil)
	session, err := mc.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.base, HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatalf("the flagged key did not initialize, so the warning's claim that it authenticates is untested: %v", err)
	}
	defer session.Close() //nolint:errcheck // best-effort close

	list, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list with the flagged key: %v", err)
	}
	names := make([]string, 0, len(list.Tools))
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	if len(names) != 0 {
		slices.Sort(names)
		t.Errorf("the warning says the key lists no tools, and it lists %d: %v", len(names), names)
	}
}
