//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Issue #1607: apply_knowledge's sink router had two arms and the deployment's
// own operating guidance was the third knowledge home with no arm. An
// operational_rule capture whose durable home is "how every agent on this
// deployment must work" belonged in server.agent_instructions, and the only
// writer was PUT /admin/config/entries/{key}, which takes the whole value: no
// review, no changeset, no rollback, and no way to touch one rule without
// rewriting the document around it. Nothing bounded the layer either, though it
// is composed into every session's first response.
//
// What these hold: a promotion lands in the customized layer and the next
// platform_info carries it; it edits its own section and leaves every other one
// byte-identical; a second promotion of the same rule consolidates; the
// promotion is listed by list_changesets and reverts cleanly; a rule naming a
// tool this deployment does not register is refused, proved for an api_ name
// outside the prefix list as it stood; the byte cap is refused on the sink and
// on the config PUT alike; a long rule lands as a page with an index entry the
// instructions carry instead of the body, and that entry resolves through
// fetch.
//
// Criterion 6 of the ticket (refuse when the deployment has no instructions
// entry) is retired: it predates built-in agent instructions and database
// configuration. A first promotion creates the value, the way a first page
// promotion creates the page.
//
// Wire forms: `sink` is a string and `action` is a string, one form each.
// `instructions` is declared "type": "object" in the tool schema, so the object
// is the one form the schema admits; the test also sends it as a string of
// JSON and asserts that form is refused rather than accepted and dropped, since
// a silently-ignored payload would report a promotion that never happened.
// `insight_ids`, `instructions.tags` and `instructions.references` are arrays
// of strings; `confirm` is a boolean, sent both true and absent. The config
// PUT's `value` is a string, one form.

const (
	// instructionsBaselinePath reports the layer's byte bounds, so a criterion
	// about the cap is checked against the numbers the deployment enforces
	// rather than against a copy of them here.
	instructionsBaselinePath = "/api/v1/admin/config/agent-instructions-baseline"
	// instructionsEntryPath is the manual writer: the config entry the
	// customized layer lives in.
	instructionsEntryPath = "/api/v1/admin/config/entries/server.agent_instructions"
)

// readInstructions returns the customized layer as the config API reports it,
// and "" when no row exists yet.
func readInstructions(t *testing.T, c *client) string {
	t.Helper()
	status, body := c.rest("GET", instructionsEntryPath, nil)
	if status == 404 {
		return ""
	}
	if status != 200 {
		t.Fatalf("GET %s: status %d (%v)", instructionsEntryPath, status, body)
	}
	value, _ := body["value"].(string)
	return value
}

// writeInstructions sets the customized layer through the config API, the
// manual writer, and returns the status.
func writeInstructions(t *testing.T, c *client, value string) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"value": value})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return c.rest("PUT", instructionsEntryPath, bytes.NewReader(raw))
}

// restoreInstructions puts the layer back to what it was when the test started,
// so a run leaves the dev deployment as it found it.
func restoreInstructions(t *testing.T, c *client, original string) {
	t.Helper()
	t.Cleanup(func() {
		if status, body := writeInstructions(t, c, original); status != 200 {
			t.Logf("restoring the agent instructions: status %d (%v)", status, body)
		}
	})
}

// promote1607 promotes a rule through the sink and returns the response.
func promote1607(t *testing.T, c *client, instructions map[string]any) map[string]any {
	t.Helper()
	return c.call("apply_knowledge", map[string]any{
		"action": "apply", "sink": "agent_instructions", "confirm": true,
		"instructions": instructions,
	})
}

// refuse1607 promotes a rule expecting a refusal and returns its text.
func refuse1607(t *testing.T, c *client, args map[string]any) string {
	t.Helper()
	res, text, err := c.callRaw("apply_knowledge", args)
	if err != nil {
		t.Fatalf("apply_knowledge: transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("apply_knowledge %v: expected a refusal, got %s", args, text)
	}
	return text
}

// bounds1607 reads the layer's byte limit and advisory from the server, so the
// criterion is checked against the numbers the deployment actually enforces.
func bounds1607(t *testing.T, c *client) (limit, advisory int) {
	t.Helper()
	status, body := c.rest("GET", instructionsBaselinePath, nil)
	if status != 200 {
		t.Fatalf("GET %s: status %d (%v)", instructionsBaselinePath, status, body)
	}
	l, ok := body["limit_bytes"].(float64)
	if !ok {
		t.Fatalf("the baseline endpoint reported no limit_bytes: %v", body)
	}
	a, ok := body["advisory_bytes"].(float64)
	if !ok {
		t.Fatalf("the baseline endpoint reported no advisory_bytes: %v", body)
	}
	return int(l), int(a)
}

// Criterion 1: a promotion reaches the customized layer, and the next
// platform_info carries it beneath the platform's own baseline.
func TestIssue1607_PromotionReachesPlatformInfo(t *testing.T) {
	c := connect(t)
	original := readInstructions(t, c)
	restoreInstructions(t, c, original)

	const section = "Acceptance 1607 engines"
	const rule = "Trino holds the warehouse on this deployment; nothing else is queryable."
	out := promote1607(t, c, map[string]any{"section": section, "body": rule})

	if got := out["action"]; got != "created" && got != "updated" {
		t.Fatalf("action = %v, want created or updated: %v", got, out)
	}
	if got, want := out["target_urn"], "ai:"+section; got != want {
		t.Errorf("target_urn = %v, want %q", got, want)
	}
	if out["revertible"] != true {
		t.Errorf("revertible = %v, want true", out["revertible"])
	}

	stored := readInstructions(t, c)
	if !strings.Contains(stored, "## "+section) || !strings.Contains(stored, rule) {
		t.Fatalf("the stored layer does not carry the promoted section:\n%s", stored)
	}

	// A fresh session is what a caller gets: the rule must be in its first
	// response, under the platform baseline.
	fresh := connect(t)
	info := fresh.call("platform_info", nil)
	composed, _ := info["agent_instructions"].(string)
	if !strings.Contains(composed, rule) {
		t.Fatalf("platform_info does not carry the promoted rule:\n%s", composed)
	}
	if i := strings.Index(composed, "How to operate this platform"); i >= 0 {
		if j := strings.Index(composed, "## "+section); j < i {
			t.Errorf("the promoted section (%d) precedes the platform baseline (%d)", j, i)
		}
	}
}

// Criterion 2 and 3: the promotion edits its own section, and a second
// promotion of the same rule consolidates into it.
func TestIssue1607_PromotionEditsOnlyItsOwnSection(t *testing.T) {
	c := connect(t)
	original := readInstructions(t, c)
	restoreInstructions(t, c, original)

	const section = "Acceptance 1607 consolidation"
	before := promote1607(t, c, map[string]any{"section": "Acceptance 1607 neighbour", "body": "A neighbouring rule."})
	if before == nil {
		t.Fatal("the neighbouring promotion returned nothing")
	}
	afterNeighbour := readInstructions(t, c)

	promote1607(t, c, map[string]any{"section": section, "body": "First wording."})
	withFirst := readInstructions(t, c)
	if !strings.HasPrefix(withFirst, afterNeighbour) {
		t.Fatalf("the promotion rewrote the text before it:\nwas:\n%s\nnow:\n%s", afterNeighbour, withFirst)
	}

	out := promote1607(t, c, map[string]any{"section": section, "body": "Corrected wording."})
	if out["action"] != "updated" {
		t.Errorf("the second promotion of one section reports %v, want updated", out["action"])
	}
	final := readInstructions(t, c)

	if !strings.HasPrefix(final, afterNeighbour) {
		t.Errorf("the section the promotion did not name is no longer byte-identical:\n%s", final)
	}
	if n := strings.Count(final, "## "+section); n != 1 {
		t.Errorf("the section appears %d times, want 1 (a repeat promotion must consolidate):\n%s", n, final)
	}
	if strings.Contains(final, "First wording.") {
		t.Errorf("the superseded wording survived:\n%s", final)
	}
	if !strings.Contains(final, "Corrected wording.") {
		t.Errorf("the corrected wording did not land:\n%s", final)
	}
}

// Criterion 4: the promotion is listed by list_changesets under its own target
// and reverts cleanly, restoring the previous text.
func TestIssue1607_PromotionListsAndRollsBack(t *testing.T) {
	c := connect(t)
	original := readInstructions(t, c)
	restoreInstructions(t, c, original)

	const section = "Acceptance 1607 rollback"
	promote1607(t, c, map[string]any{"section": "Acceptance 1607 rollback neighbour", "body": "Left alone."})
	prior := readInstructions(t, c)

	out := promote1607(t, c, map[string]any{"section": section, "body": "A rule that will be reverted."})
	csID, _ := out["changeset_id"].(string)
	if csID == "" {
		t.Fatalf("the apply response carried no changeset_id: %v", out)
	}

	listed := c.call("apply_knowledge", map[string]any{
		"action": "list_changesets", "entity_urn": "ai:" + section,
	})
	rows, ok := listed["changesets"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("list_changesets returned no changesets for ai:%s: %v", section, listed)
	}
	found := false
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok || row["changeset_id"] != csID {
			continue
		}
		found = true
		if row["revertible"] != true {
			t.Errorf("the listed promotion reports revertible = %v, want true", row["revertible"])
		}
		if row["rolled_back"] != false {
			t.Errorf("the listed promotion reports rolled_back = %v, want false", row["rolled_back"])
		}
	}
	if !found {
		t.Fatalf("changeset %s is not in the listing for ai:%s: %v", csID, section, listed)
	}

	// confirm is a boolean; the rollback here sends it true.
	c.call("apply_knowledge", map[string]any{
		"action": "rollback", "changeset_id": csID, "confirm": true,
	})
	if got := readInstructions(t, c); got != prior {
		t.Errorf("rollback did not restore the layer byte for byte:\nwant:\n%s\ngot:\n%s", prior, got)
	}
}

// Criterion 5: a promotion naming a tool this deployment does not register is
// refused, naming the tool. api_list_endpoints is the case the fixed prefix
// list could not see.
func TestIssue1607_PromotionNamingAnUnregisteredToolIsRefused(t *testing.T) {
	c := connect(t)
	original := readInstructions(t, c)
	restoreInstructions(t, c, original)

	text := refuse1607(t, c, map[string]any{
		"action": "apply", "sink": "agent_instructions", "confirm": true,
		"instructions": map[string]any{
			"section": "Acceptance 1607 stale tool",
			"body":    "Enumerate the operations with api_list_endpoints before invoking one.",
		},
	})
	if !strings.Contains(text, "api_list_endpoints") {
		t.Errorf("the refusal does not name the tool: %s", text)
	}
	if got := readInstructions(t, c); got != original {
		t.Errorf("the refused promotion changed the layer:\n%s", got)
	}

	// A registered name in the same family must still pass, or the guard would
	// refuse every rule that names a tool at all.
	out := promote1607(t, c, map[string]any{
		"section": "Acceptance 1607 live tool",
		"body":    "Discover an operation with api_discover before invoking it.",
	})
	if out["changeset_id"] == nil {
		t.Errorf("a rule naming a registered tool was not promoted: %v", out)
	}
}

// Criterion 7 and 8: the byte cap is a property of the layer. The sink refuses
// a promotion that would pass it, and so does the manual config PUT.
func TestIssue1607_LayerIsByteBoundedOnBothWriters(t *testing.T) {
	c := connect(t)
	original := readInstructions(t, c)
	restoreInstructions(t, c, original)

	limit, advisory := bounds1607(t, c)
	if advisory >= limit {
		t.Fatalf("the advisory (%d) is not below the limit (%d)", advisory, limit)
	}

	// The manual writer refuses an over-limit value, naming the alternative.
	status, body := writeInstructions(t, c, strings.Repeat("x", limit+1))
	if status != 400 {
		t.Fatalf("PUT of an over-limit value returned %d, want 400 (%v)", status, body)
	}
	detail := fmt.Sprint(body)
	if !strings.Contains(detail, "knowledge page") {
		t.Errorf("the PUT refusal does not name the knowledge-page alternative: %s", detail)
	}

	// The same writer accepts a value between the advisory and the limit, and
	// says the layer is getting long.
	status, body = writeInstructions(t, c, "## Acceptance 1607 bulk\n\n"+strings.Repeat("x", advisory+1))
	if status != 200 {
		t.Fatalf("PUT of an over-advisory value returned %d, want 200 (%v)", status, body)
	}
	notice, _ := body["notice"].(string)
	if !strings.Contains(notice, "knowledge page") {
		t.Errorf("the PUT response carries no size advisory: %v", body)
	}

	// With the layer near the limit, the sink refuses a promotion that would
	// pass it, naming the size and the alternative.
	if status, body := writeInstructions(t, c, strings.Repeat("x", limit-10)); status != 200 {
		t.Fatalf("PUT of a value just under the limit returned %d (%v)", status, body)
	}
	text := refuse1607(t, c, map[string]any{
		"action": "apply", "sink": "agent_instructions", "confirm": true,
		"instructions": map[string]any{
			"section": "Acceptance 1607 over cap", "body": "One more rule.",
		},
	})
	if !strings.Contains(text, "knowledge page") {
		t.Errorf("the sink refusal does not name the knowledge-page alternative: %s", text)
	}
	if !strings.Contains(text, fmt.Sprint(limit)) {
		t.Errorf("the sink refusal does not name the limit (%d): %s", limit, text)
	}
}

// Criterion 9: a rule too long to stay inline lands as a knowledge page, the
// instructions carry a one-line index entry instead of the body, and that entry
// is a reference the caller can follow with fetch.
func TestIssue1607_ALongRuleBecomesAPageTheInstructionsIndex(t *testing.T) {
	c := connect(t)
	original := readInstructions(t, c)
	restoreInstructions(t, c, original)

	const section = "Acceptance 1607 long rule"
	const summary = "how an aggregation is run against the search index"
	body := "Aggregations go through raw_query; the SQL surface cannot express them. " +
		strings.Repeat("The reasoning, at length, so this is a document rather than a rule. ", 40)

	out := promote1607(t, c, map[string]any{
		"section": section, "body": body, "summary": summary,
	})
	slug, _ := out["slug"].(string)
	if slug == "" {
		t.Fatalf("a rule over the inline limit did not land on a page: %v", out)
	}

	stored := readInstructions(t, c)
	ref := "mcp:knowledge_page:" + slug
	if !strings.Contains(stored, ref) {
		t.Fatalf("the instructions carry no index entry for the page:\n%s", stored)
	}
	if strings.Contains(stored, "The reasoning, at length") {
		t.Fatalf("the instructions carry the body rather than a pointer to it:\n%s", stored)
	}
	if !strings.Contains(stored, summary) {
		t.Errorf("the index entry does not say what reading the page answers:\n%s", stored)
	}

	// The pointer the instructions hand out is one the caller can follow.
	fetched := c.call("fetch", map[string]any{
		"reference": ref,
		"purpose":   "Follow the reference the deployment's agent instructions index.",
	})
	raw, err := json.Marshal(fetched)
	if err != nil {
		t.Fatalf("marshal the fetch result: %v", err)
	}
	if !strings.Contains(string(raw), "Aggregations go through raw_query") {
		t.Fatalf("fetch did not resolve %s to the promoted page: %s", ref, raw)
	}

	// A fresh session reads the index entry, not the body.
	composed, _ := connect(t).call("platform_info", nil)["agent_instructions"].(string)
	if !strings.Contains(composed, ref) {
		t.Errorf("platform_info does not carry the index entry:\n%s", composed)
	}
	if strings.Contains(composed, "The reasoning, at length") {
		t.Errorf("platform_info carries the full text rather than the index entry:\n%s", composed)
	}
}

// The instructions payload is declared "type": "object". A string of JSON is
// the other form a caller might send, and it must be refused rather than
// accepted and dropped: a promotion reported as done but never written is worse
// than one refused.
func TestIssue1607_AStringInstructionsPayloadIsRefused(t *testing.T) {
	c := connect(t)
	original := readInstructions(t, c)
	restoreInstructions(t, c, original)

	text := refuse1607(t, c, map[string]any{
		"action": "apply", "sink": "agent_instructions", "confirm": true,
		"instructions": `{"section":"Acceptance 1607 wire form","body":"A rule sent as a string."}`,
	})
	if text == "" {
		t.Error("the refusal carried no message")
	}
	if got := readInstructions(t, c); got != original {
		t.Errorf("the refused wire form changed the layer:\n%s", got)
	}
}

// A promotion with no instructions payload at all names what is missing.
func TestIssue1607_AMissingInstructionsPayloadIsRefused(t *testing.T) {
	c := connect(t)

	text := refuse1607(t, c, map[string]any{
		"action": "apply", "sink": "agent_instructions",
	})
	if !strings.Contains(text, "instructions") {
		t.Errorf("the refusal does not name the missing payload: %s", text)
	}
}
