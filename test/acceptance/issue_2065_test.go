//go:build integration

package acceptance

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- RFC 6238's default algorithm, computed here to check the platform's codes
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"net/http"
	"strings"
	"testing"
	"time"
)

// #2065: a stored secret of kind totp holds an authenticator seed, and a
// request writes {{totp:<name>}} where the one-time code goes. The api-test
// fixture's /v1/echo answers with what it received, so the code the platform
// sent is read back and checked against RFC 6238, computed here independently
// of the platform's own implementation.
//
// Wire forms: api_invoke_endpoint's body admits an object and a string of
// JSON; each is sent as literal params with the placeholder inside, together
// with a header and a query parameter carrying it. The admin route's body is
// one JSON object, with kind and value strings; the value is sent as an
// otpauth URI and as a bare base32 seed. A script's platform.call arguments
// are the object form, from a run_draft whose allow_writes is a boolean.

// rfcSeed2065 is RFC 6238's SHA1 test key, "12345678901234567890", in base32.
const rfcSeed2065 = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// rfcKey2065 is the same key as bytes, which this test computes codes from.
var rfcKey2065 = []byte("12345678901234567890")

// code2065 is RFC 6238's code for counter, computed here.
func code2065(newHash func() hash.Hash, key []byte, digits int, counter int64) string {
	mac := hmac.New(newHash, key)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

// counterOf2065 finds which period in [from, to] a code is the SHA1 6-digit
// code of, or -1.
func counterOf2065(code string, period int64, from, to time.Time) int64 {
	for k := from.Unix()/period - 1; k <= to.Unix()/period+1; k++ {
		if code2065(sha1.New, rfcKey2065, 6, k) == code {
			return k
		}
	}
	return -1
}

// totpSecret2065 stores an authenticator seed through the admin API and
// removes it after the test.
func totpSecret2065(t *testing.T, c *client, value string) string {
	t.Helper()
	name := "acc2065-" + strings.ToLower(unique1579())
	status, body := c.rest(http.MethodPut, "/api/v1/admin/secrets/"+name, strings.NewReader(mustJSON2066(t, map[string]any{
		"description": "Acceptance #2065: an authenticator seed.", "kind": "totp", "value": value,
		"allow_connections": []string{fixture2051}, "allow_personas": []string{},
	})))
	if status != http.StatusCreated {
		t.Fatalf("creating the totp secret answered %d: %v", status, body)
	}
	if strings.Contains(fmt.Sprint(body), rfcSeed2065) {
		t.Fatalf("creating the secret returned the seed: %v", body)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/secrets/"+name, http.NoBody) })
	return name
}

// sentCode2065 calls the echo with the placeholder in the body, a header and
// a query parameter, and returns the one code the fixture received in all
// three.
func sentCode2065(t *testing.T, c *client, name string, body any) string {
	t.Helper()
	ph := "{{totp:" + name + "}}"
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": fixture2051, "method": http.MethodPost, "path": "/v1/echo",
		"body":         body,
		"headers":      map[string]string{"X-Portal-Code": ph},
		"query_params": map[string]any{"code": ph},
		"purpose":      "Acceptance #2065: an automation types a one-time code.",
	})
	echoed, _ := out["body"].(map[string]any)
	sentBody, _ := echoed["body"].(map[string]any)
	code, _ := sentBody["text"].(string)
	if len(code) != 6 {
		t.Fatalf("the fixture received %q in the body, not a six-digit code: %v", code, echoed)
	}
	if !strings.Contains(stringOf(echoed["headers"]), code) || !strings.Contains(stringOf(echoed["query"]), code) {
		t.Errorf("the header and query did not carry the body's code %s: %v", code, echoed)
	}
	if strings.Contains(stringOf(out), ph) {
		t.Errorf("the fixture received the placeholder as written: %v", echoed)
	}
	return code
}

// TestIssue2065_TheUpstreamIsSentTheCodeForTheSendTime sends the code in both
// body forms, one call each. Each code is RFC 6238's for the moment it was
// sent, and the second call, in the same period, is sent the next period's
// code rather than the same one again.
func TestIssue2065_TheUpstreamIsSentTheCodeForTheSendTime(t *testing.T) {
	c := connect(t)
	// A five-second period keeps the wait for the next one short.
	name := totpSecret2065(t, c, "otpauth://totp/Vendor:ops?secret="+rfcSeed2065+"&period=5")
	const period = 5

	before := time.Now()
	first := sentCode2065(t, c, name, map[string]any{"text": "{{totp:" + name + "}}"})
	second := sentCode2065(t, c, name, `{"text": "{{totp:`+name+`}}"}`)
	after := time.Now()

	k1 := counterOf2065(first, period, before, after)
	k2 := counterOf2065(second, period, before, after)
	if k1 < 0 || k2 < 0 {
		t.Fatalf("the codes %s and %s are not RFC 6238 codes for the send time", first, second)
	}
	if first == second || k2 <= k1 {
		t.Errorf("two calls were sent %s (period %d) and %s (period %d); want the second from a later period", first, k1, second, k2)
	}
	if k1 < before.Unix()/period || k2 > after.Unix()/period {
		t.Errorf("a code was not for a period inside the calls: %d and %d outside [%d, %d]", k1, k2, before.Unix()/period, after.Unix()/period)
	}
}

const script2065 = `
def main():
    """Types the one-time code into the portal's second sign-in step."""
    res = platform.call("api_invoke_endpoint", {
        "connection": "api-test-fixture", "method": "POST", "path": "/v1/echo",
        "body": {"text": "{{totp:%s}}"},
        "purpose": "Acceptance #2065: a script sends a one-time code.",
    })
    print(json.encode(res["status"]))
`

func TestIssue2065_TheAuditTheCallRecordAndTheRecordingHoldThePlaceholder(t *testing.T) {
	c := connect(t)
	name := totpSecret2065(t, c, rfcSeed2065)
	ph := "{{totp:" + name + "}}"

	code := sentCode2065(t, c, name, map[string]any{"text": ph})
	var row map[string]any
	for attempt := 0; attempt < 40 && row == nil; attempt++ {
		if attempt > 0 {
			time.Sleep(250 * time.Millisecond)
		}
		for _, r := range c.list("/api/v1/admin/audit/events?per_page=200&tool_name=api_invoke_endpoint&session_id=" + c.sessionID) {
			if m, ok := r.(map[string]any); ok && strings.Contains(stringOf(m["parameters"]), ph) {
				row = m
			}
		}
	}
	if row == nil {
		t.Fatalf("no audit row for the call holds the placeholder")
	}
	if params := stringOf(row["parameters"]); strings.Contains(params, code) || strings.Contains(params, rfcSeed2065) {
		t.Errorf("the audit row's parameters hold the code or the seed: %s", params)
	}
	records := c.list("/api/v1/admin/calls?session_id=" + c.sessionID)
	if len(records) == 0 {
		t.Fatalf("no call record for the call")
	}
	if text := stringOf(records); strings.Contains(text, code) || strings.Contains(text, rfcSeed2065) {
		t.Errorf("the call record holds the code or the seed: %s", text)
	}

	ran := c.call("manage_script", map[string]any{"command": "run_draft", "name": "acceptance-2065-" + unique1579(),
		"source": strings.Replace(script2065, "%s", name, 1), "allow_writes": true})
	if ran["status"] != "succeeded" {
		t.Fatalf("the draft failed: %v", ran)
	}
	recording, _ := ran["recording"].(string)
	if recording == "" {
		t.Fatalf("the draft kept no recording: %v", ran)
	}
	data := recording2051(t, recording)
	if !bytes.Contains(data, []byte(ph)) || bytes.Contains(data, []byte(rfcSeed2065)) {
		t.Errorf("the recording does not hold the placeholder, or holds the seed")
	}
}

func TestIssue2065_KindsDoNotCrossAndScopeHolds(t *testing.T) {
	c := connect(t)
	seed := totpSecret2065(t, c, rfcSeed2065)
	value, _ := secret2051(t, c, nil, fixture2051)
	other := "acc-2065-other-" + strings.ToLower(unique1579())
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+other, map[string]any{
		"config": fixtureConnectionConfig(other, ""), "description": "Acceptance #2065: a connection a seed does not name.",
	}); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("creating the connection answered %d", status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+other, http.NoBody) })

	for _, tc := range []struct {
		label, conn, text, want string
	}{
		{"a seed named as a value", fixture2051, "{{secret:" + seed + "}}", "is an authenticator seed, which is never sent"},
		{"a value named as a code", fixture2051, "{{totp:" + value + "}}", "holds a value, not an authenticator seed"},
		{"a connection outside allow_connections", other, "{{totp:" + seed + "}}", `may not be sent through connection "` + other + `"`},
	} {
		t.Run(tc.label, func(t *testing.T) {
			res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
				"connection": tc.conn, "method": http.MethodPost, "path": "/v1/echo",
				"body": map[string]any{"text": tc.text}, "purpose": "Acceptance #2065: a placeholder of the wrong kind is refused.",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError || !strings.Contains(text, tc.want) || !strings.Contains(text, "nothing was sent") {
				t.Errorf("not refused by name before sending: %s", text)
			}
			if strings.Contains(text, rfcSeed2065) {
				t.Errorf("the refusal quotes the seed: %s", text)
			}
		})
	}
}

func TestIssue2065_URIParametersAndRefusedSeeds(t *testing.T) {
	c := connect(t)
	name := totpSecret2065(t, c, "otpauth://totp/Vendor:ops?secret="+rfcSeed2065+"&algorithm=SHA256&digits=8&period=60")
	status, got := c.rest(http.MethodGet, "/api/v1/admin/secrets/"+name, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the secret answered %d", status)
	}
	params, _ := got["totp"].(map[string]any)
	if got["kind"] != "totp" || params["algorithm"] != "SHA256" || params["digits"] != float64(8) || params["period"] != float64(60) {
		t.Errorf("the secret reads back as %v, want totp SHA256/8/60", got)
	}
	if strings.Contains(fmt.Sprint(got), rfcSeed2065) {
		t.Errorf("reading the secret returned the seed")
	}

	// The current code is RFC 6238's for the moment it was shown, from the
	// SHA256 key RFC 6238 pairs with that algorithm's test vectors; the
	// stored seed is the SHA1 key, which HMAC-SHA256 takes as its key too.
	before := time.Now()
	status, current := c.rest(http.MethodGet, "/api/v1/admin/secrets/"+name+"/code", http.NoBody)
	after := time.Now()
	if status != http.StatusOK {
		t.Fatalf("the current code answered %d: %v", status, current)
	}
	code, _ := current["code"].(string)
	matched := false
	for k := before.Unix() / 60; k <= after.Unix()/60; k++ {
		if code2065(sha256.New, rfcKey2065, 8, k) == code {
			matched = true
		}
	}
	if len(code) != 8 || !matched {
		t.Errorf("the current code %q is not the 8-digit SHA256 code for the moment it was shown", code)
	}
	if left, _ := current["seconds_left"].(float64); left < 1 || left > 60 {
		t.Errorf("seconds_left = %v, want within the 60-second period", current["seconds_left"])
	}

	// A call is sent an 8-digit SHA256 code for its own 60-second period.
	sent := time.Now()
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": fixture2051, "method": http.MethodPost, "path": "/v1/echo",
		"body": map[string]any{"text": "{{totp:" + name + "}}"}, "purpose": "Acceptance #2065: an eight-digit code.",
	})
	echoed, _ := out["body"].(map[string]any)
	sentBody, _ := echoed["body"].(map[string]any)
	sentCode, _ := sentBody["text"].(string)
	matched = false
	for k := sent.Unix()/60 - 1; k <= time.Now().Unix()/60+1; k++ {
		if sentCode == code2065(sha256.New, rfcKey2065, 8, k) {
			matched = true
		}
	}
	if !matched {
		t.Errorf("the call was sent %q, not an 8-digit SHA256 code for its period: %v", sentCode, echoed)
	}

	for value, want := range map[string]string{
		"otpauth://hotp/Vendor:ops?secret=" + rfcSeed2065 + "&counter=1": "hotp",
		"not!base32": "not base32",
	} {
		status, body := c.rest(http.MethodPut, "/api/v1/admin/secrets/acc2065-refused", strings.NewReader(mustJSON2066(t, map[string]any{
			"kind": "totp", "value": value, "allow_connections": []string{fixture2051},
		})))
		if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["detail"]), want) {
			t.Errorf("saving %q answered %d %v, want a refusal naming %q", value, status, body, want)
		}
	}
}
