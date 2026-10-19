//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/scripttoken"
)

// Acceptance for #2175: signed tokens a script mints and verifies. A token
// one request to an endpoint mints verifies on a later request to the same
// endpoint with its claims intact; a tampered, an expired and another
// endpoint's token are each refused with a reason the handler reads; a
// single-use token verifies once and is refused on its second use on the
// other replica; and a script whose persona the stored secret does not allow
// cannot mint with it.
//
// Wire forms: the stored secret is written as one JSON object
// (allow_connections and allow_personas JSON arrays of strings); manage_script
// takes its typed arguments as literal tools/call params; the endpoint reads
// the token from the query string, the one form the handler takes it in.

// issue2175Handler mints on a request with no ?token and verifies the one a
// request carries. Its main() drafts both paths, so the save's test replays
// a mint and a verify.
func issue2175Handler(secret string) string {
	return `def handle(req):
    """Mints a token, or verifies the one the request carries."""
    tok = req["query"].get("token")
    if tok == None:
        claims = {"applicant": req["query"].get("applicant")}
        once = req["query"].get("once") == "1"
        return {"json": {"token": platform.mint_token(claims, 600, secret = "` + secret + `", single_use = once)}}
    return {"json": platform.verify_token(tok, secret = "` + secret + `")}

def main():
    """Drafts a mint and the verify of what it minted."""
    minted = handle({"query": {"applicant": "a-1"}, "caller": None})
    platform.result(handle({"query": {"token": minted["json"]["token"]}, "caller": None}))
`
}

// issue2175Secret stores a token key and returns its name and value.
func issue2175Secret(t *testing.T, admin *client, personas []string) (name, value string) {
	t.Helper()
	stamp := strings.ToLower(unique1579())
	name, value = "acc2175_"+stamp, "token-key-"+stamp
	status := admin.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+name, map[string]any{
		"description": "Acceptance #2175", "value": value,
		"allow_connections": []string{scripttoken.SecretUse}, "allow_personas": personas,
	})
	if status != http.StatusCreated {
		t.Fatalf("creating the token key answered %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+name, http.NoBody) })
	return name, value
}

// issue2175Endpoint saves the handler as a script and binds it under name.
func issue2175Endpoint(t *testing.T, admin *client, name, secret string) {
	t.Helper()
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": issue2175Handler(secret)}, nil)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "auth_mode": "signed_in"})
}

// issue2175Get sends a GET to an endpoint on c's replica and decodes the JSON.
func issue2175Get(t *testing.T, c *client, endpoint string, query url.Values) map[string]any {
	t.Helper()
	got := endpointRequest(t, c.base, http.MethodGet, "/endpoints/"+endpoint+"?"+query.Encode(), bearer(c.apiKey), "")
	if got.status != http.StatusOK {
		t.Fatalf("GET /endpoints/%s: %d %s", endpoint, got.status, got.body)
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("GET /endpoints/%s answered %q: %v", endpoint, got.body, err)
	}
	return out
}

func issue2175Mint(t *testing.T, c *client, endpoint string, once bool) string {
	t.Helper()
	q := url.Values{"applicant": {"a-17"}}
	if once {
		q.Set("once", "1")
	}
	out := issue2175Get(t, c, endpoint, q)
	tok, _ := out["token"].(string)
	if tok == "" {
		t.Fatalf("the mint answered no token: %v", out)
	}
	return tok
}

func issue2175Verify(t *testing.T, c *client, endpoint, token string) map[string]any {
	t.Helper()
	return issue2175Get(t, c, endpoint, url.Values{"token": {token}})
}

func issue2175Refused(t *testing.T, got map[string]any, reason string) {
	t.Helper()
	if got["ok"] != false || got["reason"] != reason {
		t.Fatalf("want a refusal for %q the handler can read, got %v", reason, got)
	}
}

// TestIssue2175_ATokenVerifiesOnALaterRequestWithItsClaims: minted by one
// request, verified by the next, claims intact.
func TestIssue2175_ATokenVerifiesOnALaterRequestWithItsClaims(t *testing.T) {
	admin := connect(t)
	secret, _ := issue2175Secret(t, admin, nil)
	name := endpointName("acc-2175-mint")
	issue2175Endpoint(t, admin, name, secret)

	tok := issue2175Mint(t, admin, name, false)
	got := issue2175Verify(t, admin, name, tok)
	claims, _ := got["claims"].(map[string]any)
	if got["ok"] != true || claims["applicant"] != "a-17" {
		t.Fatalf("the later request verified %v; want ok with the applicant claim", got)
	}
	if again := issue2175Verify(t, admin, name, tok); again["ok"] != true {
		t.Fatalf("a token minted without single_use verified once only: %v", again)
	}
}

// TestIssue2175_TamperedExpiredAndOtherEndpointTokensAreRefused: each is
// answered with its reason. The expired token is signed here with the key the
// platform derives from the stored value, a minute long and an hour ago, so
// nothing waits for a token to expire.
func TestIssue2175_TamperedExpiredAndOtherEndpointTokensAreRefused(t *testing.T) {
	admin := connect(t)
	secret, value := issue2175Secret(t, admin, nil)
	name, other := endpointName("acc-2175-refuse"), endpointName("acc-2175-other")
	issue2175Endpoint(t, admin, name, secret)
	issue2175Endpoint(t, admin, other, secret)

	tok := issue2175Mint(t, admin, name, false)
	body, sig, _ := strings.Cut(tok, ".")
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(raw), `"a-17"`, `"a-99"`, 1)
	issue2175Refused(t, issue2175Verify(t, admin, name, base64.RawURLEncoding.EncodeToString([]byte(forged))+"."+sig), scripttoken.ReasonTampered)

	expired, err := scripttoken.Sign(scripttoken.Key(value), scripttoken.Binding{Endpoint: name},
		scripttoken.Mint{Claims: map[string]any{"applicant": "a-17"}, TTL: time.Minute}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	issue2175Refused(t, issue2175Verify(t, admin, name, expired), scripttoken.ReasonExpired)
	issue2175Refused(t, issue2175Verify(t, admin, other, tok), scripttoken.ReasonOtherEndpoint)
}

// TestIssue2175_ASingleUseTokenIsRefusedOnItsSecondUseOnTheOtherReplica.
func TestIssue2175_ASingleUseTokenIsRefusedOnItsSecondUseOnTheOtherReplica(t *testing.T) {
	a, b := connectReplicaPair(t)
	secret, _ := issue2175Secret(t, a, nil)
	name := endpointName("acc-2175-once")
	issue2175Endpoint(t, a, name, secret)

	tok := issue2175Mint(t, a, name, true)
	if first := issue2175Verify(t, a, name, tok); first["ok"] != true || first["single_use"] != true {
		t.Fatalf("the first use on %s: %v", a.base, first)
	}
	issue2175Refused(t, issue2175Verify(t, b, name, tok), scripttoken.ReasonUsed)
}

// TestIssue2175_APersonaTheSecretDoesNotAllowCannotMint: the key names only
// the administrator's persona, and the owner's script, acting as the owner's
// persona, is refused when it mints.
func TestIssue2175_APersonaTheSecretDoesNotAllowCannotMint(t *testing.T) {
	admin := connect(t)
	secret, _ := issue2175Secret(t, admin, []string{"admin"})
	owner := connectAs(t, devOwnerAPIKey)
	res, text, err := owner.callRaw("manage_script", map[string]any{
		"command": "run_draft", "name": endpointName("acc-2175-persona"), "source": issue2175Handler(secret),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "may not be used by persona") {
		t.Fatalf("the owner's draft minted with a key its persona is not allowed (error=%v): %s", res.IsError, text)
	}
}
