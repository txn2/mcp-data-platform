//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2259: one access mode per endpoint (authenticated, public,
// bot_check), the callers an authenticated endpoint admits, and named bot
// checks an endpoint picks from.
//
// Wire forms: manage_script endpoint_set takes auth_mode and bot_check as
// strings and callers and checked_methods as JSON arrays of strings, sent as
// the literal tools/call params; PUT /api/v1/portal/scripts/{id}/endpoint
// takes one JSON object with the same fields; PUT /api/v1/admin/bot-checks/
// {name} takes one JSON object, numbers as JSON numbers; a bot_check request
// carries its answer in the X-Bot-Check header; an authenticated request
// carries its API key in Authorization: Bearer.
//
// Cloudflare's published Turnstile testing key verifies every token it is
// sent, so the criterion of two checks refusing each other's answers is
// shown for two proof-of-work checks (refused by the challenge's signed
// check name) and, for Turnstile, as a proof-of-work endpoint refusing a
// Turnstile token and two Turnstile checks holding different keys answering
// one token differently: each endpoint verifies with its own check's key.

const (
	issue2259AnalystPersona = "inventory-analyst"
	issue2259OwnerKeyName   = "owner"
)

// issue2259Echo saves the echo handler as a script of its own and returns its
// name and id.
func issue2259Echo(t *testing.T, admin *client, prefix string) (name, id string) {
	t.Helper()
	name = endpointName(prefix)
	id = saveEndpointScript(t, admin, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)
	return name, id
}

// issue2259Get is a GET of an endpoint with key as the caller's API key, none
// when key is empty.
func issue2259Get(t *testing.T, admin *client, name, key string) endpointResponse {
	t.Helper()
	var header http.Header
	if key != "" {
		header = bearer(key)
	}
	return endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name, header, "")
}

// issue2259Refused waits for an endpoint's refusals counted under reason to
// reach want: each replica adds its counts every few seconds.
func issue2259Refused(t *testing.T, admin *client, name, reason string, want float64) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for {
		status, detail := admin.rest(http.MethodGet, "/api/v1/admin/endpoints/"+name+"?range=day", nil)
		if status != http.StatusOK {
			t.Fatalf("GET the endpoint's page: %d %v", status, detail)
		}
		byReason, _ := detail["refused_by_reason"].(map[string]any)
		if got, _ := byReason[reason].(float64); got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never counted %v refusals under %q: %v", name, want, reason, byReason)
		}
		time.Sleep(2 * time.Second)
	}
}

// TestIssue2259_TheThreeModesOnBothSurfacesAndSignedInIsRefused: endpoint_set
// and the portal route take authenticated, public and bot_check, refuse
// signed_in, and refuse a bot_check binding naming a check that does not
// exist.
func TestIssue2259_TheThreeModesOnBothSurfacesAndSignedInIsRefused(t *testing.T) {
	admin := connect(t)
	check := putBotCheck(t, admin, issue2173Quick)
	viaTool, _ := issue2259Echo(t, admin, "acc-2259-tool")
	viaPortal, portalID := issue2259Echo(t, admin, "acc-2259-portal")

	for _, args := range []map[string]any{
		{"auth_mode": "authenticated", "callers": []string{"persona:" + issue2259AnalystPersona}},
		{"auth_mode": "public"},
		{"auth_mode": "bot_check", "bot_check": check, "methods": []string{"GET", "POST"}, "checked_methods": []string{"POST"}},
	} {
		args["endpoint"] = viaTool
		listed := bindEndpoint(t, admin, viaTool, "", args)
		if listed["auth_mode"] != args["auth_mode"] {
			t.Fatalf("endpoint_set %v listed back %v", args, listed)
		}
		if args["auth_mode"] == "bot_check" && (listed["bot_check"] != check || fmt.Sprint(listed["checked_methods"]) != "[POST]") {
			t.Fatalf("the bot_check binding listed back: %v", listed)
		}
		if args["auth_mode"] == "authenticated" && fmt.Sprint(listed["callers"]) != "[persona:"+issue2259AnalystPersona+"]" {
			t.Fatalf("the authenticated binding's callers: %v", listed["callers"])
		}
	}
	// The tool's schema enumerates the modes, so the arguments are refused
	// before the binding is read.
	if out, refusal := endpointSet(t, admin, viaTool, "", map[string]any{"endpoint": viaTool, "auth_mode": "signed_in"}); out != nil ||
		!strings.Contains(refusal, "signed_in does not equal any of: [authenticated public bot_check]") {
		t.Fatalf("endpoint_set signed_in: want refused, got %v %q", out, refusal)
	}
	if out, refusal := endpointSet(t, admin, viaTool, "", map[string]any{"endpoint": viaTool, "auth_mode": "bot_check", "bot_check": "acc-2259-no-such-check"}); out != nil ||
		!strings.Contains(refusal, `no bot check is named "acc-2259-no-such-check"`) {
		t.Fatalf("endpoint_set naming a check that does not exist: want refused, got %v %q", out, refusal)
	}

	put := func(body map[string]any) (int, map[string]any) {
		body["endpoint"] = viaPortal
		return admin.rest(http.MethodPut, "/api/v1/portal/scripts/"+portalID+"/endpoint", jsonBody(t, body))
	}
	for _, body := range []map[string]any{
		{"auth_mode": "authenticated", "callers": []string{"*"}},
		{"auth_mode": "public"},
		{"auth_mode": "bot_check", "bot_check": check},
	} {
		mode := body["auth_mode"]
		if status, out := put(body); status != http.StatusOK || out["auth_mode"] != mode {
			t.Fatalf("the portal PUT %v: %d %v", body, status, out)
		}
	}
	if status, out := put(map[string]any{"auth_mode": "signed_in"}); status != http.StatusBadRequest {
		t.Fatalf("the portal PUT signed_in: want 400, got %d %v", status, out)
	}
	if status, out := put(map[string]any{"auth_mode": "bot_check", "bot_check": "acc-2259-no-such-check"}); status != http.StatusBadRequest {
		t.Fatalf("the portal PUT naming a check that does not exist: want 400, got %d %v", status, out)
	}
}

// TestIssue2259_AnAuthenticatedEndpointAdmitsTheCallersItNames: callers
// naming the analyst persona answer the analyst's key and refuse another
// persona's, counted under its own refusal reason; callers naming an API key
// answer that key; the administrator is always admitted.
func TestIssue2259_AnAuthenticatedEndpointAdmitsTheCallersItNames(t *testing.T) {
	admin := connect(t)
	name, _ := issue2259Echo(t, admin, "acc-2259-callers")
	bindEndpoint(t, admin, name, "", map[string]any{
		"endpoint": name, "auth_mode": "authenticated", "callers": []string{"persona:" + issue2259AnalystPersona},
	})
	if got := issue2259Get(t, admin, name, analystKey1892); got.status != http.StatusOK {
		t.Fatalf("the analyst's key: want 200, got %d %s", got.status, got.body)
	}
	got := issue2259Get(t, admin, name, devOwnerAPIKey)
	if got.status != http.StatusForbidden || !strings.Contains(got.body, "does not admit this caller") {
		t.Fatalf("a key under another persona: want 403, got %d %s", got.status, got.body)
	}
	if got := issue2259Get(t, admin, name, admin.apiKey); got.status != http.StatusOK {
		t.Fatalf("the administrator: want 200, got %d %s", got.status, got.body)
	}
	issue2259Refused(t, admin, name, "caller", 1)

	bindEndpoint(t, admin, name, "", map[string]any{"callers": []string{"api_key:" + issue2259OwnerKeyName}})
	if got := issue2259Get(t, admin, name, devOwnerAPIKey); got.status != http.StatusOK {
		t.Fatalf("the key the callers name: want 200, got %d %s", got.status, got.body)
	}
	if got := issue2259Get(t, admin, name, analystKey1892); got.status != http.StatusForbidden {
		t.Fatalf("the analyst once the callers name only a key: want 403, got %d %s", got.status, got.body)
	}
	if got := issue2259Get(t, admin, name, ""); got.status != http.StatusUnauthorized ||
		!strings.Contains(got.body, "authenticated caller: a platform API key or a browser session") {
		t.Fatalf("no credential: want 401 naming an authenticated caller, got %d %s", got.status, got.body)
	}
}

// TestIssue2259_AnEmptyListAdmitsOnlyTheAdministratorAndStarEveryCaller.
func TestIssue2259_AnEmptyListAdmitsOnlyTheAdministratorAndStarEveryCaller(t *testing.T) {
	admin := connect(t)
	name, _ := issue2259Echo(t, admin, "acc-2259-empty")
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "auth_mode": "authenticated", "callers": []string{}})
	for _, key := range []string{devOwnerAPIKey, devPeerAPIKey, analystKey1892} {
		if got := issue2259Get(t, admin, name, key); got.status != http.StatusForbidden {
			t.Fatalf("an empty callers list and key %s: want 403, got %d %s", key, got.status, got.body)
		}
	}
	if got := issue2259Get(t, admin, name, admin.apiKey); got.status != http.StatusOK {
		t.Fatalf("an empty callers list and the administrator: want 200, got %d %s", got.status, got.body)
	}
	bindEndpoint(t, admin, name, "", map[string]any{"callers": []string{"*"}})
	for _, key := range []string{devOwnerAPIKey, devPeerAPIKey, analystKey1892, admin.apiKey} {
		if got := issue2259Get(t, admin, name, key); got.status != http.StatusOK {
			t.Fatalf(`callers "*" and key %s: want 200, got %d %s`, key, got.status, got.body)
		}
	}
}

// TestIssue2259_APublicEndpointAnswersWithNoCheckWhereABotCheckOneRefuses.
func TestIssue2259_APublicEndpointAnswersWithNoCheckWhereABotCheckOneRefuses(t *testing.T) {
	admin := connect(t)
	check := putBotCheck(t, admin, issue2173Quick)
	public, _ := issue2259Echo(t, admin, "acc-2259-public")
	bindEndpoint(t, admin, public, "", map[string]any{"endpoint": public, "auth_mode": "public", "methods": []string{"POST"}})
	checked := checkedEndpoint(t, admin, "acc-2259-checked", check)

	if got := postAnswered(t, admin.base, public, ""); got.status != http.StatusOK || !strings.Contains(got.body, `"caller":null`) {
		t.Fatalf("the public endpoint with no X-Bot-Check: want 200, got %d %s", got.status, got.body)
	}
	if got := postAnswered(t, admin.base, checked, ""); got.status != http.StatusForbidden {
		t.Fatalf("the bot_check endpoint with no X-Bot-Check: want 403, got %d %s", got.status, got.body)
	}
	if got := endpointRequest(t, admin.base, http.MethodGet, "/botcheck/challenge?endpoint="+public, nil, ""); got.status != http.StatusNotFound {
		t.Fatalf("a challenge for a public endpoint: want 404, got %d %s", got.status, got.body)
	}
}

// TestIssue2259_TwoProofOfWorkChecksIssueAtTheirOwnDifficultyAndRefuseEachOther.
func TestIssue2259_TwoProofOfWorkChecksIssueAtTheirOwnDifficultyAndRefuseEachOther(t *testing.T) {
	admin := connect(t)
	easy := putBotCheck(t, admin, issue2173Quick)
	hard := putBotCheck(t, admin, map[string]any{
		"provider": "pow", "difficulty": 11, "max_difficulty": 14, "rate_threshold": 50, "challenge_ttl_seconds": 30,
	})
	onEasy, onHard := checkedEndpoint(t, admin, "acc-2259-easy", easy), checkedEndpoint(t, admin, "acc-2259-hard", hard)

	easyCh, hardCh := challengeFor(t, admin.base, onEasy), challengeFor(t, admin.base, onHard)
	if easyCh.Difficulty != 8 || easyCh.Check != easy || hardCh.Difficulty != 11 || hardCh.Check != hard {
		t.Fatalf("the challenges: %s at %d (want 8), %s at %d (want 11)", easyCh.Check, easyCh.Difficulty, hardCh.Check, hardCh.Difficulty)
	}

	// Rebinding swaps the checks the two endpoints name, so each challenge is
	// presented on an endpoint now naming the other check.
	bindEndpoint(t, admin, onEasy, "", map[string]any{"bot_check": hard})
	got := postAnswered(t, admin.base, onEasy, solvePoW(easyCh.Token, easyCh.Difficulty))
	if got.status != http.StatusForbidden || !strings.Contains(got.body, "another bot check") {
		t.Fatalf("an easy check's answer on an endpoint naming the hard check: want 403, got %d %s", got.status, got.body)
	}
	bindEndpoint(t, admin, onEasy, "", map[string]any{"bot_check": easy})
	if got := postAnswered(t, admin.base, onEasy, solvePoW(easyCh.Token, easyCh.Difficulty)); got.status != http.StatusOK {
		t.Fatalf("the easy check's answer on its own endpoint: %d %s", got.status, got.body)
	}
	if got := postAnswered(t, admin.base, onHard, solvePoW(hardCh.Token, hardCh.Difficulty)); got.status != http.StatusOK {
		t.Fatalf("the hard check's answer on its own endpoint: %d %s", got.status, got.body)
	}
}

// TestIssue2259_ATurnstileCheckAndAProofOfWorkCheckOnOneDeployment: a
// proof-of-work endpoint refuses a Turnstile token, a Turnstile endpoint
// accepts the token its check's key verifies, and a second Turnstile check
// holding a key that verifies nothing refuses the same kind of token.
func TestIssue2259_ATurnstileCheckAndAProofOfWorkCheckOnOneDeployment(t *testing.T) {
	admin := connect(t)
	const (
		passes = "1x0000000000000000000000000000000AA" // #nosec G101 -- Cloudflare's published testing key, which verifies every token
		fails  = "2x0000000000000000000000000000000AA" // #nosec G101 -- Cloudflare's published testing key, which verifies none
	)
	pow := putBotCheck(t, admin, issue2173Quick)
	accepting, refusing := endpointName("acc-2259-ts-yes"), endpointName("acc-2259-ts-no")
	issue2173Turnstile(t, admin, accepting, endpointName("acc-2259-ts-yes-key"), passes)
	issue2173Turnstile(t, admin, refusing, endpointName("acc-2259-ts-no-key"), fails)
	t.Cleanup(func() {
		admin.rest(http.MethodDelete, botChecksPath+"/"+accepting, nil)
		admin.rest(http.MethodDelete, botChecksPath+"/"+refusing, nil)
	})
	onPoW := checkedEndpoint(t, admin, "acc-2259-pow", pow)
	onYes := checkedEndpoint(t, admin, "acc-2259-ts-yes", accepting)
	onNo := checkedEndpoint(t, admin, "acc-2259-ts-no", refusing)

	ch := challengeFor(t, admin.base, onYes)
	if ch.Provider != "turnstile" || ch.Check != accepting || ch.Token != "" {
		t.Fatalf("the Turnstile endpoint's challenge: %+v", ch)
	}
	token := "XXXX.DUMMY.TOKEN.XXXX." + endpointName("run")
	if got := postAnswered(t, admin.base, onPoW, token); got.status != http.StatusForbidden {
		t.Fatalf("a Turnstile token on the proof-of-work endpoint: want 403, got %d %s", got.status, got.body)
	}
	if got := postAnswered(t, admin.base, onNo, token+".no"); got.status != http.StatusForbidden {
		t.Fatalf("a token on the endpoint whose check's key verifies nothing: want 403, got %d %s", got.status, got.body)
	}
	if got := postAnswered(t, admin.base, onYes, token); got.status != http.StatusOK {
		t.Fatalf("the token on the endpoint whose check's key verifies it: %d %s", got.status, got.body)
	}
	powCh := challengeFor(t, admin.base, onPoW)
	if got := postAnswered(t, admin.base, onPoW, solvePoW(powCh.Token, powCh.Difficulty)); got.status != http.StatusOK {
		t.Fatalf("the proof-of-work endpoint's own answer: %d %s", got.status, got.body)
	}
}

// TestIssue2259_ACheckAnEndpointNamesIsNotDeleted: the refusal names the
// endpoint; once nothing names the check, it is deleted.
func TestIssue2259_ACheckAnEndpointNamesIsNotDeleted(t *testing.T) {
	admin := connect(t)
	check := putBotCheck(t, admin, issue2173Quick)
	name := checkedEndpoint(t, admin, "acc-2259-inuse", check)

	status, out := admin.rest(http.MethodDelete, botChecksPath+"/"+check, nil)
	if status != http.StatusConflict || !strings.Contains(fmt.Sprint(out["detail"]), name) {
		t.Fatalf("deleting a check an endpoint names: want 409 naming %s, got %d %v", name, status, out)
	}
	status, listed := admin.rest(http.MethodGet, botChecksPath+"/"+check, nil)
	if status != http.StatusOK || fmt.Sprint(listed["used_by"]) != "["+name+"]" {
		t.Fatalf("the check's endpoints: %d %v", status, listed)
	}
	bindEndpoint(t, admin, name, "", map[string]any{"auth_mode": "public"})
	if status, out := admin.rest(http.MethodDelete, botChecksPath+"/"+check, nil); status != http.StatusNoContent {
		t.Fatalf("deleting a check nothing names: want 204, got %d %v", status, out)
	}
	if status, _ := admin.rest(http.MethodGet, botChecksPath+"/"+check, nil); status != http.StatusNotFound {
		t.Fatalf("the deleted check: want 404, got %d", status)
	}
}

// TestIssue2259_ACheckThatChecksOnlyPOSTAnswersGETWithoutOne.
func TestIssue2259_ACheckThatChecksOnlyPOSTAnswersGETWithoutOne(t *testing.T) {
	admin := connect(t)
	check := putBotCheck(t, admin, issue2173Quick)
	name, _ := issue2259Echo(t, admin, "acc-2259-form")
	bindEndpoint(t, admin, name, "", map[string]any{
		"endpoint": name, "auth_mode": "bot_check", "bot_check": check,
		"methods": []string{"GET", "POST"}, "checked_methods": []string{"POST"},
	})
	if got := issue2259Get(t, admin, name, ""); got.status != http.StatusOK {
		t.Fatalf("GET with no check: want 200, got %d %s", got.status, got.body)
	}
	if got := postAnswered(t, admin.base, name, ""); got.status != http.StatusForbidden {
		t.Fatalf("POST with no check: want 403, got %d %s", got.status, got.body)
	}
	ch := challengeFor(t, admin.base, name)
	if got := postAnswered(t, admin.base, name, solvePoW(ch.Token, ch.Difficulty)); got.status != http.StatusOK {
		t.Fatalf("POST with its check answered: %d %s", got.status, got.body)
	}
}

// TestIssue2259_NoSurfaceCallsAnEndpointCallerSignedIn: the manage_script
// schema an agent reads, the admin list's access filter and the bot check
// listing speak of access modes and named checks, never signed_in.
func TestIssue2259_NoSurfaceCallsAnEndpointCallerSignedIn(t *testing.T) {
	admin := connect(t)
	for _, tool := range admin.tools() {
		if tool.Name != "manage_script" {
			continue
		}
		var buf strings.Builder
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(tool.InputSchema); err != nil {
			t.Fatal(err)
		}
		schema := buf.String()
		for _, word := range []string{"signed_in", "signed-in", "open_reads"} {
			if strings.Contains(schema, word) {
				t.Errorf("manage_script's schema still says %q", word)
			}
		}
		for _, want := range []string{"authenticated", "bot_check", "checked_methods", "persona:<name>"} {
			if !strings.Contains(schema, want) {
				t.Errorf("manage_script's schema does not name %q", want)
			}
		}
	}
	if status, _ := admin.rest(http.MethodGet, "/api/v1/admin/endpoints?access=signed_in", nil); status != http.StatusBadRequest {
		t.Errorf("the endpoint list's access=signed_in: want 400, got %d", status)
	}
	if status, _ := admin.rest(http.MethodGet, "/api/v1/admin/settings/bot-check", nil); status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		t.Errorf("the retired settings route: want it gone, got %d", status)
	}
	status, list := admin.rest(http.MethodGet, botChecksPath, nil)
	if checks, _ := list["checks"].([]any); status != http.StatusOK || len(checks) == 0 {
		t.Fatalf("the bot checks listing: %d %v", status, list)
	}
}
