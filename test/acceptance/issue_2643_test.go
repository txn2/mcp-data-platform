//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Acceptance for #2643: an agent's promote, rollback or public domain on an
// environment other than dev waits on a person, whatever the role of the
// person it works for. A client that can be asked asks; one that cannot gets
// APPROVAL_REQUIRED and a request a person decides in the portal, which the
// agent's session can never decide. The portal's own controls are unchanged,
// and an environment an administrator marks agents_may_promote takes an
// agent's moves. History names whose decision moved it.
//
// Wire forms: manage_site takes command, site, from, to, environment and
// hostname as strings, release as a number and public as a boolean, sent as
// literal tools/call params; the eliciting client answers with action accept
// or decline and no content. manage_approval decide takes id and decision as
// strings. POST /api/v1/portal/sites/{site}/promote takes a JSON object of
// strings; PUT .../environments/{name} takes agents_may_promote as a boolean,
// true and false; POST /api/v1/portal/approvals/{id}/decide takes decision as a
// string.

// agentsMayPromote2643 marks env of site so an agent's promote, rollback and
// public domain there go ahead without a person's decision (#2643), as an
// administrator does in the portal. Criteria about how promotion works, rather
// than who decides it, use it.
func agentsMayPromote2643(t *testing.T, site, env string) {
	t.Helper()
	status, got := rest2559(t, keyAdmin2604, http.MethodPut, portal2559+"/"+site+"/environments/"+env, map[string]any{"agents_may_promote": true})
	want2559(t, "letting agents promote into "+env, status, http.StatusOK, got)
}

// decideInPortal2643 approves a request as an administrator in the portal: a
// site request is decided only there (#2643).
func decideInPortal2643(t *testing.T, id string) {
	t.Helper()
	status, got := rest2559(t, keyAdmin2604, http.MethodPost, "/api/v1/portal/approvals/"+url.PathEscape(id)+"/decide", map[string]any{"decision": "approve"})
	want2559(t, "approving "+id+" in the portal", status, http.StatusOK, got)
}

// requestIn2643 is the approval request id an APPROVAL_REQUIRED answer names.
func requestIn2643(t *testing.T, text string) string {
	t.Helper()
	i := strings.Index(text, "apr_")
	if i < 0 {
		t.Fatalf("the answer names no request: %s", text)
	}
	end := i
	for end < len(text) && (text[end] == '_' || (text[end] >= 'a' && text[end] <= 'z') || (text[end] >= '0' && text[end] <= '9')) {
		end++
	}
	return text[i:end]
}

// readyToPromote2643 is a site whose dev serves a release, owned by an
// administrator's key so the administrator's own agent is the one asking.
func readyToPromote2643(t *testing.T) *site2604 {
	t.Helper()
	s := newSite2604(t, keyAdmin2604, "acc2643-")
	s.publish(t, s.key, "one")
	return s
}

// lastMove2643 is the newest move of env as History records it.
func lastMove2643(t *testing.T, s *site2604, env string) map[string]any {
	t.Helper()
	status, got := s.rest(t, keyAdmin2604, http.MethodGet, "/deployments?environment="+env, nil)
	want2559(t, "reading history", status, http.StatusOK, got)
	rows, _ := got["deployments"].([]any)
	if len(rows) == 0 {
		t.Fatalf("%s has no history: %v", env, got["_raw"])
	}
	row, _ := rows[0].(map[string]any)
	return row
}

// TestIssue2643_AnAdministratorsAgentWaitsOnAPerson: an administrator's agent
// promoting dev to prod over MCP, with a client that cannot be asked, gets
// APPROVAL_REQUIRED and prod does not move; the agent cannot decide the
// request; an administrator decides it in the portal; the agent's next call
// promotes, and History says it went through an approval.
func TestIssue2643_AnAdministratorsAgentWaitsOnAPerson(t *testing.T) {
	s := readyToPromote2643(t)
	agent := connectAs(t, keyAdmin2604)
	promote := map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "prod"}
	res, text, err := agent.callRaw("manage_site", promote)
	if err != nil || !res.IsError || !strings.Contains(text, "APPROVAL_REQUIRED") || !strings.Contains(text, s.host("dev")) {
		t.Fatalf("an administrator's agent promoting to prod was not stopped naming the dev address: %v %s", err, text)
	}
	id := requestIn2643(t, text)
	if got := s.release2617(t, "prod"); got != 0 {
		t.Errorf("prod moved to release %d before a person decided", got)
	}

	res, text, _ = agent.callRaw("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve"})
	if !res.IsError || !strings.Contains(text, "decided by a person in the portal") {
		t.Errorf("the agent decided its own site request: %s", text)
	}

	decideInPortal2643(t, id)
	done := agent.call("manage_site", promote)
	if done["status"] != "promoted" || done["decided_by"] != "approval" {
		t.Fatalf("after a person approved, the agent's promote: %v", done)
	}
	if got := lastMove2643(t, s, "prod")["decided_by"]; got != "approval" {
		t.Errorf("History does not say whose decision moved prod: %v", got)
	}
}

// TestIssue2643_TheClientAsksThePerson: a client that can be asked shows the
// person the release, the target and the dev address; declining moves nothing
// and accepting promotes, recorded as decided by elicitation.
func TestIssue2643_TheClientAsksThePerson(t *testing.T) {
	s := readyToPromote2643(t)
	ec := connectEliciting(t, keyAdmin2604, "2026-07-28")
	promote := map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "prod"}

	ec.answer("decline", nil)
	res, text, _ := ec.callRaw("manage_site", promote)
	asked := ec.prompts()
	if len(asked) == 0 {
		t.Fatalf("the client was not asked: %s", text)
	}
	q := asked[len(asked)-1]
	for _, want := range []string{"release 1", s.name, "prod", s.host("dev")} {
		if !strings.Contains(q, want) {
			t.Errorf("the question does not name %q: %s", want, q)
		}
	}
	if !res.IsError || !strings.Contains(text, "declined") {
		t.Errorf("declining did not stop the promote: %s", text)
	}

	ec.answer("accept", nil)
	done := ec.call("manage_site", promote)
	if done["status"] != "promoted" || done["decided_by"] != "elicitation" {
		t.Fatalf("accepting: %v", done)
	}
	served2518For(t, s, "", "one")
	if got := lastMove2643(t, s, "prod")["decided_by"]; got != "elicitation" {
		t.Errorf("History: %v", got)
	}
}

// TestIssue2643_RollbackAndAPublicDomainWaitToo: an agent's rollback of prod
// and its public domain on prod each wait on a person.
func TestIssue2643_RollbackAndAPublicDomainWaitToo(t *testing.T) {
	s := readyToPromote2643(t)
	s.promote(t, "dev", "prod")
	s.publish(t, s.key, "two")
	s.promote(t, "dev", "prod")
	agent := connectAs(t, keyAdmin2604)
	for name, args := range map[string]map[string]any{
		"rollback":      {"command": "rollback", "site": s.name, "environment": "prod"},
		"public domain": {"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.name + ".example.test", "public": true},
	} {
		res, text, _ := agent.callRaw("manage_site", args)
		if !res.IsError || !strings.Contains(text, "APPROVAL_REQUIRED") {
			t.Errorf("%s: an agent's move of prod went through: %s", name, text)
		}
	}
	if got := s.release2617(t, "prod"); got != 2 {
		t.Errorf("prod moved to release %d on an agent's word", got)
	}
}

// TestIssue2643_ThePortalIsThePerson: the same promote from the portal goes
// ahead as it always has, recorded as the person's own.
func TestIssue2643_ThePortalIsThePerson(t *testing.T) {
	s := readyToPromote2643(t)
	s.promote(t, "dev", "prod")
	if got := lastMove2643(t, s, "prod")["decided_by"]; got != nil {
		t.Errorf("a person's own promote says decided_by %v", got)
	}
}

// TestIssue2643_AnEnvironmentAgentsMayPromoteInto: only an administrator, in
// the portal, lets agents promote into an environment; an agent's promote
// there then goes ahead, recorded as through the setting.
func TestIssue2643_AnEnvironmentAgentsMayPromoteInto(t *testing.T) {
	s := readyToPromote2643(t)
	agent := connectAs(t, keyAdmin2604)
	res, text, _ := agent.callRaw("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "prod", "agents_may_promote": true})
	if !res.IsError || !strings.Contains(text, "administrator in the portal") {
		t.Errorf("an agent set agents_may_promote: %s", text)
	}
	owned := newSite2604(t, keyOwner2604, "acc2643-own-")
	if status, got := owned.rest(t, keyOwner2604, http.MethodPut, "/environments/prod", map[string]any{"agents_may_promote": true}); status != http.StatusForbidden {
		t.Errorf("a non-administrator set agents_may_promote: %d %v", status, got["_raw"])
	}
	status, got := s.rest(t, keyAdmin2604, http.MethodPut, "/environments/dev", map[string]any{"agents_may_promote": true})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("dev took agents_may_promote: %d %v", status, got["_raw"])
	}

	agentsMayPromote2643(t, s.name, "prod")
	done := agent.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "prod"})
	if done["status"] != "promoted" || done["decided_by"] != "environment_setting" {
		t.Fatalf("an agent's promote into an environment agents may promote into: %v", done)
	}
	s.setEnv(t, keyAdmin2604, "prod", map[string]any{"agents_may_promote": false})
	s.publish(t, s.key, "two")
	if res, text, _ := agent.callRaw("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "prod"}); !res.IsError {
		t.Errorf("turned off, the setting still let an agent promote: %s", text)
	}
}
