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

// Acceptance for #2656: an agent in an interactive MCP session reaching the
// portal's API over the platform-admin connection is an agent, whether or not
// its call carries the person's token. Its promote, rollback and public domain
// on an environment other than dev answer 409 APPROVAL_REQUIRED and move
// nothing, and it cannot decide a site request through the approvals route. A
// person's own request through the portal's API is unchanged, and a managed
// script's call on platform-admin stays gated.
//
// Wire forms: api_invoke_endpoint takes connection, method and path as
// strings, and body as a JSON object and as a string of JSON, both sent as
// literal tools/call params for the promote; the rollback, the domain and the
// decision send the object form. manage_site takes command, site, from and to
// as strings. manage_script's command, name, description and source, and
// run_script's name and wait_seconds, are typed strings and numbers with one
// form each. The REST promote takes a JSON object of strings.

// invoke2656 calls a portal route over platform-admin from an interactive
// session and returns the answer's text and whether it was refused with
// APPROVAL_REQUIRED.
func invoke2656(t *testing.T, c *client, method, path string, body any) (text string, waits bool) {
	t.Helper()
	_, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": "platform-admin", "method": method, "path": path, "body": body,
		"headers": map[string]string{"Content-Type": "application/json"},
		"purpose": "Acceptance #2656: an agent moves a site environment over platform-admin.",
	})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return text, strings.Contains(text, "409") && strings.Contains(text, "APPROVAL_REQUIRED")
}

// TestIssue2656_APromoteOverPlatformAdminWaits: an administrator's agent
// promoting dev to prod over platform-admin, with the body as an object and as
// a string of JSON, gets 409 APPROVAL_REQUIRED, and prod does not move.
func TestIssue2656_APromoteOverPlatformAdminWaits(t *testing.T) {
	s := readyToPromote2643(t)
	agent := connectAs(t, keyAdmin2604)
	payload := map[string]any{"from": "dev", "to": "prod", "reason": "acceptance 2656"}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for form, body := range map[string]any{"object": payload, "string of JSON": string(raw)} {
		t.Run(form, func(t *testing.T) {
			text, waits := invoke2656(t, agent, http.MethodPost, s.path("/promote"), body)
			if !waits || !strings.Contains(text, "Only a person carries this out") {
				t.Errorf("the agent's promote over platform-admin was not stopped: %s", text)
			}
		})
	}
	if got := s.release2617(t, "prod"); got != 0 {
		t.Errorf("prod moved to release %d on an agent's word", got)
	}
}

// TestIssue2656_RollbackAndAPublicDomainOverPlatformAdminWait: the agent's
// rollback of prod and its public domain on prod, over platform-admin, each
// answer 409 APPROVAL_REQUIRED, and prod keeps its release.
func TestIssue2656_RollbackAndAPublicDomainOverPlatformAdminWait(t *testing.T) {
	s := readyToPromote2643(t)
	s.promote(t, "dev", "prod")
	s.publish(t, s.key, "two")
	s.promote(t, "dev", "prod")
	agent := connectAs(t, keyAdmin2604)
	if text, waits := invoke2656(t, agent, http.MethodPost, s.path("/rollback"), map[string]any{"environment": "prod"}); !waits {
		t.Errorf("rollback: the agent's move of prod went through: %s", text)
	}
	domain := map[string]any{"environment": "prod", "hostname": s.name + ".example.test", "public": true}
	if text, waits := invoke2656(t, agent, http.MethodPost, s.path("/domains"), domain); !waits {
		t.Errorf("public domain: the agent's move of prod went through: %s", text)
	}
	if got := s.release2617(t, "prod"); got != 2 {
		t.Errorf("prod moved to release %d on an agent's word", got)
	}
}

// TestIssue2656_AnAgentCannotDecideOverPlatformAdmin: a site request the
// agent's promote opened is refused when the agent decides it over
// platform-admin, stays pending, and is decided by a person in the portal.
func TestIssue2656_AnAgentCannotDecideOverPlatformAdmin(t *testing.T) {
	s := readyToPromote2643(t)
	agent := connectAs(t, keyAdmin2604)
	_, text, _ := agent.callRaw("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "prod"})
	id := requestIn2643(t, text)

	_, decided, err := agent.callRaw("api_invoke_endpoint", map[string]any{
		"connection": "platform-admin", "method": http.MethodPost,
		"path": "/api/v1/portal/approvals/" + url.PathEscape(id) + "/decide", "body": map[string]any{"decision": "approve"},
		"headers": map[string]string{"Content-Type": "application/json"},
		"purpose": "Acceptance #2656: an agent decides its own site request over platform-admin.",
	})
	if err != nil || !strings.Contains(decided, "decided by a person in the portal") {
		t.Fatalf("the agent decided its own site request over platform-admin: %v %s", err, decided)
	}
	decideInPortal2643(t, id)
}

// TestIssue2656_APersonsOwnRequestGoesThrough: the same promote through the
// portal's API with the person's own key goes ahead, recorded as theirs.
func TestIssue2656_APersonsOwnRequestGoesThrough(t *testing.T) {
	s := readyToPromote2643(t)
	status, got := s.rest(t, keyAdmin2604, http.MethodPost, "/promote", map[string]any{"from": "dev", "to": "prod", "reason": "acceptance 2656"})
	want2559(t, "a person's promote", status, http.StatusOK, got)
	if got["status"] != "promoted" {
		t.Fatalf("a person's promote: %v", got["_raw"])
	}
	if by := lastMove2643(t, s, "prod")["decided_by"]; by != nil {
		t.Errorf("a person's own promote says decided_by %v", by)
	}
}

// TestIssue2656_AScriptsPromoteStaysGated: a saved script's promote over
// platform-admin answers 409 APPROVAL_REQUIRED, and prod does not move.
func TestIssue2656_AScriptsPromoteStaysGated(t *testing.T) {
	s := readyToPromote2643(t)
	c := connectAs(t, keyAdmin2604)
	source := fmt.Sprintf(`def main():
    """Promotes a site's dev release to prod through the platform-admin connection."""
    res = platform.call("api_invoke_endpoint", {
        "connection": "platform-admin",
        "method": "POST",
        "path": %q,
        "body": {"from": "dev", "to": "prod", "reason": "acceptance 2656"},
        "headers": {"Content-Type": "application/json"},
        "purpose": "Acceptance #2656: a saved script promotes a site.",
    }, on_error = "return")
    platform.result({"status": res.get("status", 0), "text": str(res)[:600]})
`, s.path("/promote"))
	name := fmt.Sprintf("acc-2656-%d", time.Now().UnixNano())
	c.saveScript(map[string]any{"command": "create", "name": name, "description": "Acceptance #2656: a script promotes over platform-admin.", "source": source}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	res := result2086(t, c, name)
	if status, _ := res["status"].(float64); status != http.StatusConflict || !strings.Contains(fmt.Sprint(res["text"]), "APPROVAL_REQUIRED") {
		t.Errorf("the script's promote over platform-admin: %v", res)
	}
	if got := s.release2617(t, "prod"); got != 0 {
		t.Errorf("prod moved to release %d on a script's word", got)
	}
}
