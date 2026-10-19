//go:build integration

package acceptance

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/bits"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/botcheck"
)

// Acceptance for #2173: the bot check a public endpoint requires. A request
// with a solved challenge is accepted, the same answer sent again is refused,
// and so is an expired one; a burst past the endpoint's recent rate raises
// the difficulty and the raised challenge solved is still accepted; with
// Cloudflare Turnstile configured, a request is accepted only after
// Cloudflare verifies its token, and the secret key never appears in a
// response or the platform's log; and a challenge issued for one endpoint is
// refused on another.
//
// Wire forms: the challenge is answered in the X-Bot-Check header, solved
// once by the browser script the platform serves (/botcheck/solver.js, run in
// Node with its fetch) and otherwise by the same work in Go; the settings
// route takes one JSON object, numbers as JSON numbers.

// issue2173Quick is a proof of work cheap enough to solve many times.
var issue2173Quick = map[string]any{
	"provider": "pow", "difficulty": 8, "max_difficulty": 12, "rate_threshold": 2, "challenge_ttl_seconds": 5,
	"allow_public_without_check": false,
}

// solvePoW finds the counter whose SHA-256 of token~counter has difficulty
// leading zero bits, the work the platform's solver script does, and returns
// the answer a request carries.
func solvePoW(token string, difficulty int) string {
	for i := 0; ; i++ {
		answer := token + "~" + strconv.Itoa(i)
		sum := sha256.Sum256([]byte(answer))
		zeros := 0
		for _, b := range sum {
			if b != 0 {
				zeros += bits.LeadingZeros8(b)
				break
			}
			zeros += 8
		}
		if zeros >= difficulty {
			return answer
		}
	}
}

// publicEndpoint saves and binds a public echo endpoint.
func publicEndpoint(t *testing.T, admin *client, prefix string) string {
	t.Helper()
	name := endpointName(prefix)
	saveEndpointScript(t, admin, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "methods": []string{"POST"}, "auth_mode": "public"})
	return name
}

// challengeFor fetches a challenge for endpoint from base.
func challengeFor(t *testing.T, base, endpoint string) botcheck.Challenge {
	t.Helper()
	got := endpointRequest(t, base, http.MethodGet, "/botcheck/challenge?endpoint="+endpoint, nil, "")
	if got.status != http.StatusOK {
		t.Fatalf("GET /botcheck/challenge: %d %s", got.status, got.body)
	}
	var ch botcheck.Challenge
	if err := json.Unmarshal([]byte(got.body), &ch); err != nil {
		t.Fatalf("decoding the challenge: %v %s", err, got.body)
	}
	return ch
}

// postAnswered posts to a public endpoint with answer in X-Bot-Check.
func postAnswered(t *testing.T, base, endpoint, answer string) endpointResponse {
	t.Helper()
	header := http.Header{"Content-Type": {"application/json"}}
	if answer != "" {
		header.Set("X-Bot-Check", answer)
	}
	return endpointRequest(t, base, http.MethodPost, "/endpoints/"+endpoint, header, `{"email":"ann@example.com"}`)
}

// solveInNode fetches the platform's solver script from base and answers a
// challenge for endpoint with it, as a page does.
func solveInNode(t *testing.T, base, endpoint string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is needed to run the platform's solver script: %v", err)
	}
	script := endpointRequest(t, base, http.MethodGet, "/botcheck/solver.js", nil, "")
	if script.status != http.StatusOK {
		t.Fatalf("GET /botcheck/solver.js: %d", script.status)
	}
	dir := t.TempDir()
	solver := filepath.Join(dir, "solver.js")
	if err := os.WriteFile(solver, []byte(script.body), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(dir, "run.js")
	page := `global.window = {};
require(process.argv[2]);
window.PlatformBotCheck.answer(process.argv[3], {base: process.argv[4]}).then(a => process.stdout.write(a)).catch(e => { console.error(e); process.exit(1); });`
	if err := os.WriteFile(runner, []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", runner, solver, endpoint, base).Output() // #nosec G204 -- the suite's own temp files and the platform under test
	if err != nil {
		t.Fatalf("the solver script: %v", err)
	}
	return string(out)
}

// TestIssue2173_ASolvedChallengeIsAcceptedOnceAndNotAfterItExpires.
func TestIssue2173_ASolvedChallengeIsAcceptedOnceAndNotAfterItExpires(t *testing.T) {
	admin := connect(t)
	putBotCheck(t, admin, issue2173Quick)
	name := publicEndpoint(t, admin, "acc-2173-once")

	if got := postAnswered(t, admin.base, name, ""); got.status != http.StatusForbidden {
		t.Fatalf("with no answer: want 403, got %d %s", got.status, got.body)
	}
	answer := solveInNode(t, admin.base, name)
	if got := postAnswered(t, admin.base, name, answer); got.status != http.StatusOK {
		t.Fatalf("solved by the platform's script: %d %s", got.status, got.body)
	}
	if got := postAnswered(t, admin.base, name, answer); got.status != http.StatusForbidden || !strings.Contains(got.body, "already used") {
		t.Fatalf("the same answer again: want 403 already used, got %d %s", got.status, got.body)
	}

	ch := challengeFor(t, admin.base, name)
	late := solvePoW(ch.Token, ch.Difficulty)
	time.Sleep(time.Until(ch.ExpiresAt) + 500*time.Millisecond)
	if got := postAnswered(t, admin.base, name, late); got.status != http.StatusForbidden || !strings.Contains(got.body, "expired") {
		t.Fatalf("an expired challenge: want 403 expired, got %d %s", got.status, got.body)
	}
}

// TestIssue2173_ABurstRaisesTheDifficultyAndTheRaisedChallengeIsAccepted.
func TestIssue2173_ABurstRaisesTheDifficultyAndTheRaisedChallengeIsAccepted(t *testing.T) {
	admin := connect(t)
	putBotCheck(t, admin, issue2173Quick)
	name := publicEndpoint(t, admin, "acc-2173-burst")

	quiet := challengeFor(t, admin.base, name)
	for range 8 {
		ch := challengeFor(t, admin.base, name)
		if got := postAnswered(t, admin.base, name, solvePoW(ch.Token, ch.Difficulty)); got.status != http.StatusOK {
			t.Fatalf("a burst request: %d %s", got.status, got.body)
		}
	}
	raised := challengeFor(t, admin.base, name)
	if raised.Difficulty <= quiet.Difficulty {
		t.Fatalf("difficulty after a burst of 8 against a threshold of 2: %d, quiet was %d", raised.Difficulty, quiet.Difficulty)
	}
	if got := postAnswered(t, admin.base, name, solvePoW(raised.Token, raised.Difficulty)); got.status != http.StatusOK {
		t.Fatalf("the raised challenge solved: %d %s", got.status, got.body)
	}
}

// issue2173Turnstile stores value as the secret name and points the bot
// check at Cloudflare Turnstile with its testing site key.
func issue2173Turnstile(t *testing.T, admin *client, secret, value string) {
	t.Helper()
	status, out := admin.rest(http.MethodPut, "/api/v1/admin/secrets/"+secret, jsonBody(t, map[string]any{
		"kind": "value", "value": value, "allow_connections": []string{"bot_check"}, "allow_personas": []string{},
	}))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("storing the Turnstile secret: %d %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+secret, nil) })
	putBotCheck(t, admin, map[string]any{"provider": "turnstile", "site_key": "1x00000000000000000000AA", "secret_name": secret})
}

// TestIssue2173_TurnstileAcceptsOnlyWhatCloudflareVerified: Cloudflare's
// testing secret keys verify every token (1x...) and none (2x...); the
// platform answers as Cloudflare does, and the key appears in no response
// and no log line.
func TestIssue2173_TurnstileAcceptsOnlyWhatCloudflareVerified(t *testing.T) {
	admin := connect(t)
	name := publicEndpoint(t, admin, "acc-2173-turnstile")
	secret := endpointName("acc-2173-turnstile-key")
	const (
		passes = "1x0000000000000000000000000000000AA" // #nosec G101 -- Cloudflare's published testing key, which verifies every token
		fails  = "2x0000000000000000000000000000000AA" // #nosec G101 -- Cloudflare's published testing key, which verifies none
	)
	// A widget hands out a new token each time; Cloudflare's testing keys
	// verify whatever token they are sent, so a run's own is unique.
	token := "XXXX.DUMMY.TOKEN.XXXX." + endpointName("run")
	since := time.Now().Add(-time.Second)

	issue2173Turnstile(t, admin, secret, passes)
	ch := challengeFor(t, admin.base, name)
	if ch.Provider != "turnstile" || ch.SiteKey != "1x00000000000000000000AA" || ch.Token != "" {
		t.Fatalf("the Turnstile challenge: %+v", ch)
	}
	if got := postAnswered(t, admin.base, name, ""); got.status != http.StatusForbidden {
		t.Fatalf("with no token: want 403, got %d", got.status)
	}
	if got := postAnswered(t, admin.base, name, token); got.status != http.StatusOK {
		t.Fatalf("a token Cloudflare verified: %d %s", got.status, got.body)
	}
	if got := postAnswered(t, admin.base, name, token); got.status != http.StatusForbidden || !strings.Contains(got.body, "already used") {
		t.Fatalf("the same token again: want 403 already used, got %d %s", got.status, got.body)
	}

	issue2173Turnstile(t, admin, secret, fails)
	if got := postAnswered(t, admin.base, name, token+".2"); got.status != http.StatusForbidden {
		t.Fatalf("a token Cloudflare refused: want 403, got %d %s", got.status, got.body)
	}

	status, settings := admin.rest(http.MethodGet, issue2171BotCheckPath, nil)
	raw, _ := json.Marshal(settings)
	if status != http.StatusOK || strings.Contains(string(raw), passes) || strings.Contains(string(raw), fails) {
		t.Fatalf("the settings response carries the key: %s", raw)
	}
	if rec := stderrRecordSince(t, since, func(r map[string]any) bool {
		line, _ := json.Marshal(r)
		return strings.Contains(string(line), passes) || strings.Contains(string(line), fails)
	}); rec != nil {
		t.Fatalf("a log line carries the key: %v", rec)
	}
}

// TestIssue2173_AChallengeForOneEndpointIsRefusedOnAnother.
func TestIssue2173_AChallengeForOneEndpointIsRefusedOnAnother(t *testing.T) {
	admin := connect(t)
	putBotCheck(t, admin, issue2173Quick)
	first, second := publicEndpoint(t, admin, "acc-2173-first"), publicEndpoint(t, admin, "acc-2173-second")
	ch := challengeFor(t, admin.base, first)
	got := postAnswered(t, admin.base, second, solvePoW(ch.Token, ch.Difficulty))
	if got.status != http.StatusForbidden || !strings.Contains(got.body, "another endpoint") {
		t.Fatalf("%s's challenge on %s: want 403, got %d %s", first, second, got.status, got.body)
	}
	if got := postAnswered(t, admin.base, first, solvePoW(ch.Token, ch.Difficulty)); got.status != http.StatusOK {
		t.Fatal(fmt.Sprintf("the challenge on its own endpoint: %d %s", got.status, got.body))
	}
}
