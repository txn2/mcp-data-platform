//go:build integration

package acceptance

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // G505: no sender here signs with sha1; imported for the hash table below
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Acceptance for #2223: a webhook source verifies the eleven senders the
// ticket names by preset, answers their setup checks, and reads a signature
// header laid out by a pattern (the ticket's Verkada addition).
//
// The senders' own services are not available here: each request is built by
// hand to the sender's published specification, with nothing from the
// platform's signing code, and posted to POST /hooks/{source} on the running
// platform. The admin API takes one JSON object per route; the outbound
// criterion sends api_invoke_endpoint's body as both forms it admits, an
// object and a string of JSON.

const issue2223Purpose = "Acceptance for #2223: deliver to a capturing receiver through an hmac connection."

// issue2223Secret is a secret every hmac-and-token sender is given. It is
// base64, which the teams preset reads it as, and plain text to the others.
const issue2223Secret = "YWNjZXB0YW5jZS0yMjIzLXNlbmRlci1zZWNyZXQ="

// issue2223GitLabSecret is a Standard Webhooks signing token: whsec_ and
// base64, the key being the bytes the base64 decodes to.
const issue2223GitLabSecret = "whsec_" + issue2223Secret

// issue2223Request is one request a sender sends.
type issue2223Request struct {
	contentType string
	header      map[string]string
	body        []byte
}

// issue2223Sender builds a sender's request for the event marker at now, and
// the same request made to fail with one byte changed.
type issue2223Sender struct {
	preset string
	secret func(pub ed25519.PublicKey) string
	build  func(now time.Time, marker string, priv ed25519.PrivateKey) issue2223Request
	// tamper changes one byte of what the proof covers: the body for a
	// signature, the token for a token, the JWT for monday.com, whose JWT
	// does not sign the body.
	tamper func(r issue2223Request) issue2223Request
	config map[string]any
}

func issue2223MAC(h func() hash.Hash, key, msg []byte) []byte {
	m := hmac.New(h, key)
	_, _ = m.Write(msg)
	return m.Sum(nil)
}

// issue2223FlipBody changes the first character of the body's first string
// value. The signature covers it and the body stays JSON, so the refusal is
// always the signature's. Changing the byte before the closing brace turned a
// Linear timestamp ending in 9 into ':' about one run in ten, and the request
// was refused for an unreadable timestamp instead (#2378). A body with no
// string value is returned unchanged, which the criterion reports as accepted.
func issue2223FlipBody(r issue2223Request) issue2223Request {
	b := append([]byte{}, r.body...)
	if i := bytes.Index(b, []byte(`":"`)); i >= 0 && i+3 < len(b) && b[i+3] != '"' {
		b[i+3]++
	}
	r.body = b
	return r
}

func issue2223Unix(now time.Time) string { return strconv.FormatInt(now.Unix(), 10) }

// issue2223SlackStyle signs as Slack and Zoom do: "v0=" and the hex
// HMAC-SHA256 of "v0:{timestamp}:{body}".
func issue2223SlackStyle(sigHeader, tsHeader string) func(time.Time, string, ed25519.PrivateKey) issue2223Request {
	return func(now time.Time, marker string, _ ed25519.PrivateKey) issue2223Request {
		body := []byte(`{"type":"event_callback","event_id":"` + marker + `"}`)
		ts := issue2223Unix(now)
		sig := "v0=" + hex.EncodeToString(issue2223MAC(sha256.New, []byte(issue2223Secret), []byte("v0:"+ts+":"+string(body))))
		return issue2223Request{contentType: "application/json", body: body, header: map[string]string{sigHeader: sig, tsHeader: ts}}
	}
}

// issue2223Senders are the eleven presets' senders, each built to its docs.
func issue2223Senders() []issue2223Sender {
	plain := func(ed25519.PublicKey) string { return issue2223Secret }
	return []issue2223Sender{
		{preset: "slack", secret: plain, build: issue2223SlackStyle("X-Slack-Signature", "X-Slack-Request-Timestamp"), tamper: issue2223FlipBody},
		{preset: "zoom", secret: plain, build: issue2223SlackStyle("x-zm-signature", "x-zm-request-timestamp"), tamper: issue2223FlipBody},
		{
			preset: "mattermost", secret: plain,
			build: func(_ time.Time, marker string, _ ed25519.PrivateKey) issue2223Request {
				form := url.Values{"token": {issue2223Secret}, "text": {"deploy finished"}, "post_id": {marker}}
				return issue2223Request{contentType: "application/x-www-form-urlencoded", body: []byte(form.Encode())}
			},
			tamper: func(r issue2223Request) issue2223Request {
				r.body = []byte(strings.Replace(string(r.body), "token=YWN", "token=ZWN", 1))
				return r
			},
		},
		{
			preset: "teams", secret: plain,
			build: func(_ time.Time, marker string, _ ed25519.PrivateKey) issue2223Request {
				key, _ := base64.StdEncoding.DecodeString(issue2223Secret)
				body := []byte(`{"type":"message","id":"` + marker + `","text":"hi"}`)
				sig := "HMAC " + base64.StdEncoding.EncodeToString(issue2223MAC(sha256.New, key, body))
				return issue2223Request{contentType: "application/json", body: body, header: map[string]string{"Authorization": sig}}
			},
			tamper: issue2223FlipBody,
		},
		{
			preset: "discord", secret: func(pub ed25519.PublicKey) string { return hex.EncodeToString(pub) },
			build: func(now time.Time, marker string, priv ed25519.PrivateKey) issue2223Request {
				body := []byte(`{"type":2,"id":"` + marker + `"}`)
				ts := issue2223Unix(now)
				sig := hex.EncodeToString(ed25519.Sign(priv, append([]byte(ts), body...)))
				return issue2223Request{
					contentType: "application/json", body: body,
					header: map[string]string{"X-Signature-Ed25519": sig, "X-Signature-Timestamp": ts},
				}
			},
			tamper: issue2223FlipBody,
		},
		{
			preset: "monday", secret: plain,
			build: func(now time.Time, marker string, _ ed25519.PrivateKey) issue2223Request {
				// The criterion's fixed sender secret, which the source under test is
				// created with: it signs as monday.com does, so the receiver's check runs.
				tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"exp": now.Add(5 * time.Minute).Unix(), "dat": map[string]any{"app_id": 1},
				}).SignedString([]byte(issue2223Secret)) // nosemgrep: go.jwt-go.security.jwt.hardcoded-jwt-key
				body := []byte(`{"event":{"type":"create_pulse","pulseId":"` + marker + `"}}`)
				return issue2223Request{contentType: "application/json", body: body, header: map[string]string{"Authorization": tok}}
			},
			tamper: func(r issue2223Request) issue2223Request {
				tok := []byte(r.header["Authorization"])
				tok[len(tok)-3] ^= 1
				r.header = map[string]string{"Authorization": string(tok)}
				return r
			},
		},
		{
			preset: "shopify", secret: plain,
			build: func(_ time.Time, marker string, _ ed25519.PrivateKey) issue2223Request {
				body := []byte(`{"id":"` + marker + `","topic":"orders/create"}`)
				sig := base64.StdEncoding.EncodeToString(issue2223MAC(sha256.New, []byte(issue2223Secret), body))
				return issue2223Request{contentType: "application/json", body: body, header: map[string]string{"X-Shopify-Hmac-SHA256": sig}}
			},
			tamper: issue2223FlipBody,
		},
		{
			preset: "linear", secret: plain,
			build: func(now time.Time, marker string, _ ed25519.PrivateKey) issue2223Request {
				body := []byte(`{"action":"create","webhookId":"` + marker + `","webhookTimestamp":` + strconv.FormatInt(now.UnixMilli(), 10) + `}`)
				sig := hex.EncodeToString(issue2223MAC(sha256.New, []byte(issue2223Secret), body))
				return issue2223Request{contentType: "application/json", body: body, header: map[string]string{"Linear-Signature": sig}}
			},
			tamper: issue2223FlipBody,
		},
		{
			preset: "gitlab", secret: func(ed25519.PublicKey) string { return issue2223GitLabSecret },
			build: func(now time.Time, marker string, _ ed25519.PrivateKey) issue2223Request {
				key, _ := base64.StdEncoding.DecodeString(issue2223Secret)
				body := []byte(`{"object_kind":"push","checkout_sha":"` + marker + `"}`)
				ts := issue2223Unix(now)
				sig := "v1," + base64.StdEncoding.EncodeToString(issue2223MAC(sha256.New, key, []byte(marker+"."+ts+"."+string(body))))
				return issue2223Request{
					contentType: "application/json", body: body,
					header: map[string]string{"webhook-id": marker, "webhook-timestamp": ts, "webhook-signature": sig},
				}
			},
			tamper: issue2223FlipBody,
		},
		{
			preset: "jira", secret: plain,
			build: func(_ time.Time, marker string, _ ed25519.PrivateKey) issue2223Request {
				body := []byte(`{"webhookEvent":"jira:issue_created","id":"` + marker + `"}`)
				sig := "sha256=" + hex.EncodeToString(issue2223MAC(sha256.New, []byte(issue2223Secret), body))
				return issue2223Request{contentType: "application/json", body: body, header: map[string]string{"X-Hub-Signature": sig}}
			},
			tamper: issue2223FlipBody,
		},
		{
			preset: "microsoft_graph", secret: plain,
			build: func(_ time.Time, marker string, _ ed25519.PrivateKey) issue2223Request {
				body := []byte(`{"value":[{"subscriptionId":"` + marker + `","clientState":"` + issue2223Secret + `","changeType":"created"}]}`)
				return issue2223Request{contentType: "application/json", body: body}
			},
			tamper: func(r issue2223Request) issue2223Request {
				r.body = []byte(strings.Replace(string(r.body), `"clientState":"YWN`, `"clientState":"ZWN`, 1))
				return r
			},
			config: map[string]any{"split": "$.value"},
		},
	}
}

// issue2223Source creates a source configured with a preset.
func issue2223Source(t *testing.T, c *client, preset, secret string, auth, config map[string]any) string {
	t.Helper()
	name := issue1870Name("s2223-" + strings.ReplaceAll(preset, "_", "-"))
	a := map[string]any{"preset": preset, "secret": secret}
	for k, v := range auth {
		a[k] = v
	}
	if config == nil {
		config = map[string]any{}
	}
	issue1870Create(t, c, map[string]any{"name": name, "connection": issue1870Conn, "auth": a, "config": config})
	return name
}

// issue2223Post sends a sender's request to a source.
func issue2223Post(t *testing.T, source string, r issue2223Request) (int, string) {
	t.Helper()
	res, body := issue1870Post(t, baseURL(), source, r.contentType, r.body, r.header)
	return res.StatusCode, body
}

// TestIssue2223_EveryPresetVerifiesItsSender is the ticket's first criterion:
// for each of the eleven presets, a request signed as the sender's docs
// specify lands in the source's table, and the same request with one byte of
// its proof changed is refused with the reason and lands nothing.
func TestIssue2223_EveryPresetVerifiesItsSender(t *testing.T) {
	c := connect(t)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{
		"mattermost": "the token does not match", "microsoft_graph": "the token does not match", "monday": "the token does not match",
	}
	for _, s := range issue2223Senders() {
		t.Run(s.preset, func(t *testing.T) {
			source := issue2223Source(t, c, s.preset, s.secret(pub), nil, s.config)
			marker := "m-" + source
			good := s.build(time.Now(), marker, priv)

			status, body := issue2223Post(t, source, s.tamper(good))
			want := reasons[s.preset]
			if want == "" {
				want = "the signature does not match"
			}
			if status != http.StatusUnauthorized || !strings.Contains(body, want) {
				t.Fatalf("the changed request was answered %d %s, want 401 naming %q", status, body, want)
			}
			if status, body := issue2223Post(t, source, good); status != http.StatusAccepted {
				t.Fatalf("the request signed as the docs specify was answered %d %s, want 202", status, body)
			}
			issue1870AwaitRows(t, c, source, "", 1)
			if n := issue1870Rows(t, c, source, ""); n != 1 {
				t.Fatalf("the source holds %d events, want only the accepted one", n)
			}
		})
	}
}

// TestIssue2223_SlackWorkedExampleVerifies posts the example Slack's
// "Verifying requests from Slack" publishes, with its key, timestamp, body
// and signature, to a slack source whose window reaches back to it.
func TestIssue2223_SlackWorkedExampleVerifies(t *testing.T) {
	c := connect(t)
	const ts = 1531420618
	body := []byte("token=xyzz0WbapA4vBCDEFasx0q6G&team_id=T1DC2JH3J&team_domain=testteamnow&channel_id=G8PSS9T3V&channel_name=foobar&user_id=U2CERLKJA&user_name=roadrunner&command=%2Fwebhook-collect&text=&response_url=https%3A%2F%2Fhooks.slack.com%2Fcommands%2FT1DC2JH3J%2F397700885554%2F96rGlfmibIGlgcZRskXaIFfN&trigger_id=398738663015.47445629121.803a0bc887a14d10d2c447fce8b6703c")
	window := int(time.Since(time.Unix(ts, 0)).Seconds()) + 3600
	source := issue2223Source(t, c, "slack", "8f742231b10e8888abcd99yyyzzz85a5", map[string]any{"tolerance_seconds": window}, nil)
	status, out := issue2223Post(t, source, issue2223Request{contentType: "application/x-www-form-urlencoded", body: body, header: map[string]string{
		"X-Slack-Signature":         "v0=a2114d57b48eac39b9ad189dd8316235a7b4a8d21a10bd27519666489c69b503",
		"X-Slack-Request-Timestamp": strconv.Itoa(ts),
	}})
	if status != http.StatusAccepted {
		t.Fatalf("Slack's worked example was answered %d %s, want 202", status, out)
	}
	issue1870AwaitRows(t, c, source, "", 1)
}

// issue2223Answer posts body to a source and returns the answer.
func issue2223Answer(t *testing.T, source, query, contentType string, body []byte, header map[string]string) (int, string, string) {
	t.Helper()
	res, out := issue1870Post(t, baseURL(), source+query, contentType, body, header)
	return res.StatusCode, res.Header.Get("Content-Type"), out
}

// TestIssue2223_SetupChecksAreAnsweredAndStoreNothing is the ticket's setup
// check criterion: each is answered as its sender's docs require, and none
// appears as an event.
func TestIssue2223_SetupChecksAreAnsweredAndStoreNothing(t *testing.T) {
	c := connect(t)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	slack := issue2223Source(t, c, "slack", issue2223Secret, nil, nil)
	monday := issue2223Source(t, c, "monday", issue2223Secret, nil, nil)
	zoom := issue2223Source(t, c, "zoom", issue2223Secret, nil, nil)
	discord := issue2223Source(t, c, "discord", hex.EncodeToString(pub), nil, nil)
	graph := issue2223Source(t, c, "microsoft_graph", issue2223Secret, nil, nil)
	now := time.Now()
	ts := issue2223Unix(now)

	expectJSON := func(name string, status int, got, want string) {
		t.Helper()
		var g, w any
		if status != http.StatusOK || json.Unmarshal([]byte(got), &g) != nil || json.Unmarshal([]byte(want), &w) != nil || fmt.Sprint(g) != fmt.Sprint(w) {
			t.Errorf("%s was answered %d %s, want 200 %s", name, status, got, want)
		}
	}

	slackBody := []byte(`{"token":"Jhj5dZrVaK7ZwHHjRyZWjbDl","challenge":"3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P","type":"url_verification"}`)
	slackSig := "v0=" + hex.EncodeToString(issue2223MAC(sha256.New, []byte(issue2223Secret), []byte("v0:"+ts+":"+string(slackBody))))
	status, _, out := issue2223Answer(t, slack, "", "application/json", slackBody,
		map[string]string{"X-Slack-Signature": slackSig, "X-Slack-Request-Timestamp": ts})
	expectJSON("Slack's url_verification", status, out, `{"challenge":"3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P"}`)

	status, _, out = issue2223Answer(t, monday, "", "application/json", []byte(`{"challenge":"3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P"}`), nil)
	expectJSON("monday.com's challenge", status, out, `{"challenge":"3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P"}`)

	zoomBody := []byte(`{"payload":{"plainToken":"qgg8vlvZRS6UYooatFL8Aw"},"event_ts":1654503849680,"event":"endpoint.url_validation"}`)
	zoomSig := "v0=" + hex.EncodeToString(issue2223MAC(sha256.New, []byte(issue2223Secret), []byte("v0:"+ts+":"+string(zoomBody))))
	encrypted := hex.EncodeToString(issue2223MAC(sha256.New, []byte(issue2223Secret), []byte("qgg8vlvZRS6UYooatFL8Aw")))
	status, _, out = issue2223Answer(t, zoom, "", "application/json", zoomBody,
		map[string]string{"x-zm-signature": zoomSig, "x-zm-request-timestamp": ts})
	expectJSON("Zoom's endpoint.url_validation", status, out, `{"plainToken":"qgg8vlvZRS6UYooatFL8Aw","encryptedToken":"`+encrypted+`"}`)

	ping := []byte(`{"type":1}`)
	pingSig := hex.EncodeToString(ed25519.Sign(priv, append([]byte(ts), ping...)))
	status, _, out = issue2223Answer(t, discord, "", "application/json", ping,
		map[string]string{"X-Signature-Ed25519": pingSig, "X-Signature-Timestamp": ts})
	expectJSON("Discord's PING", status, out, `{"type":1}`)
	if status, _, out := issue2223Answer(t, discord, "", "application/json", ping, map[string]string{"X-Signature-Timestamp": ts}); status != http.StatusUnauthorized {
		t.Errorf("an unsigned PING was answered %d %s, want 401", status, out)
	}

	token := "Validation: Testing client application reachability for subscription Request-Id: 25ec4b5e-1f1d-4ff4-8a4c-5d6a2c8c0b9a"
	status, ctype, out := issue2223Answer(t, graph, "?validationToken="+url.QueryEscape(token), "text/plain", nil, nil)
	if status != http.StatusOK || !strings.HasPrefix(ctype, "text/plain") || out != token {
		t.Errorf("Graph's validation was answered %d %q %q, want 200 text/plain with the decoded token", status, ctype, out)
	}

	// One event is posted after the checks and awaited: once it has landed,
	// the slack source holds it alone, and the others hold nothing.
	event := issue2223SlackStyle("X-Slack-Signature", "X-Slack-Request-Timestamp")(time.Now(), "after-checks", nil)
	if status, out := issue2223Post(t, slack, event); status != http.StatusAccepted {
		t.Fatalf("the event after the checks was answered %d %s, want 202", status, out)
	}
	issue1870AwaitRows(t, c, slack, "", 1)
	for _, source := range []string{monday, zoom, discord, graph} {
		if n := issue1870Count(t, c, "SELECT count(*) FROM "+issue1870Table(source)); n != 0 {
			t.Errorf("source %s holds %d events after its setup check, want 0", source, n)
		}
	}
}

// TestIssue2223_GraphNotificationsCarryTheClientState is the Graph criterion:
// two items carrying the source's clientState land as two events; a second
// item missing or mismatching it refuses the request and lands nothing; a
// lifecycle notification carrying it lands.
func TestIssue2223_GraphNotificationsCarryTheClientState(t *testing.T) {
	c := connect(t)
	source := issue2223Source(t, c, "microsoft_graph", issue2223Secret, nil, map[string]any{"split": "$.value", "event_type_path": "$.changeType"})
	post := func(items ...string) (int, string) {
		return issue2223Post(t, source, issue2223Request{contentType: "application/json", body: []byte(`{"value":[` + strings.Join(items, ",") + `]}`)})
	}
	item := func(id, state string) string {
		return `{"subscriptionId":"` + id + `","changeType":"updated","clientState":"` + state + `"}`
	}
	if status, out := post(item("a", issue2223Secret), `{"subscriptionId":"b","changeType":"updated"}`); status != http.StatusUnauthorized {
		t.Fatalf("an item without clientState was answered %d %s, want 401", status, out)
	}
	if status, out := post(item("a", issue2223Secret), item("b", "someone-else")); status != http.StatusUnauthorized {
		t.Fatalf("an item with another clientState was answered %d %s, want 401", status, out)
	}
	if status, out := post(item("a", issue2223Secret), item("b", issue2223Secret)); status != http.StatusAccepted || !strings.Contains(out, `"accepted":2`) {
		t.Fatalf("two items carrying the clientState were answered %d %s, want 202 accepting 2", status, out)
	}
	lifecycle := `{"subscriptionId":"a","lifecycleEvent":"reauthorizationRequired","clientState":"` + issue2223Secret + `","changeType":"lifecycle"}`
	if status, out := post(lifecycle); status != http.StatusAccepted {
		t.Fatalf("a lifecycle notification was answered %d %s, want 202", status, out)
	}
	issue1870AwaitRows(t, c, source, "", 3)
	issue1870AwaitRows(t, c, source, "event_type = 'lifecycle'", 1)
}

// TestIssue2223_StaleRequestsAreRefused is the tolerance criterion for the
// senders that sign a timestamp: Linear in its body, Slack and Zoom in a
// header. Each request is signed correctly for a time outside the window.
func TestIssue2223_StaleRequestsAreRefused(t *testing.T) {
	c := connect(t)
	stale := time.Now().Add(-time.Hour)
	for _, s := range issue2223Senders() {
		if s.preset != "linear" && s.preset != "slack" && s.preset != "zoom" {
			continue
		}
		t.Run(s.preset, func(t *testing.T) {
			source := issue2223Source(t, c, s.preset, issue2223Secret, nil, nil)
			status, body := issue2223Post(t, source, s.build(stale, "stale-"+source, nil))
			if status != http.StatusUnauthorized || !strings.Contains(body, "the timestamp is outside the tolerance window") {
				t.Fatalf("a request signed an hour ago was answered %d %s, want 401 stale", status, body)
			}
			if n := issue1870Count(t, c, "SELECT count(*) FROM "+issue1870Table(source)); n != 0 {
				t.Fatalf("the stale request landed %d events", n)
			}
		})
	}
}

// issue2223Verkada is the configuration the ticket gives for Verkada.
func issue2223Verkada() map[string]any {
	return map[string]any{
		"mode": "hmac", "secret": issue2223Secret, "algorithm": "sha256", "encoding": "hex",
		"signature_header": "Verkada-Signature", "header_pattern": "{timestamp}|{signature}", "signed": "{body}|{timestamp}",
	}
}

// TestIssue2223_HeaderPatternVerkada is the addition's receiver criterion: a
// source configured as the ticket gives accepts a request signed that way, and
// refuses one byte changed, one outside the window, and a header with no "|".
func TestIssue2223_HeaderPatternVerkada(t *testing.T) {
	c := connect(t)
	name := issue1870Name("s2223-verkada")
	issue1870Create(t, c, map[string]any{"name": name, "connection": issue1870Conn, "auth": issue2223Verkada(), "config": map[string]any{}})
	sign := func(at time.Time, body []byte) map[string]string {
		ts := issue2223Unix(at)
		sig := hex.EncodeToString(issue2223MAC(sha256.New, []byte(issue2223Secret), []byte(string(body)+"|"+ts)))
		return map[string]string{"Verkada-Signature": ts + "|" + sig}
	}
	body := []byte(`{"webhook_type":"notification","data":{"camera_id":"` + name + `"}}`)
	for what, tc := range map[string]struct {
		body   []byte
		header map[string]string
		reason string
	}{
		"one body byte changed": {issue2223FlipBody(issue2223Request{body: body}).body, sign(time.Now(), body), "the signature does not match"},
		"outside the window":    {body, sign(time.Now().Add(-time.Hour), body), "the timestamp is outside the tolerance window"},
		"no separator": {
			body,
			map[string]string{"Verkada-Signature": strings.Replace(sign(time.Now(), body)["Verkada-Signature"], "|", "", 1)},
			"the signature does not match",
		},
	} {
		status, out := issue2223Post(t, name, issue2223Request{contentType: "application/json", body: tc.body, header: tc.header})
		if status != http.StatusUnauthorized || !strings.Contains(out, tc.reason) {
			t.Errorf("%s was answered %d %s, want 401 %q", what, status, out, tc.reason)
		}
	}
	if status, out := issue2223Post(t, name, issue2223Request{contentType: "application/json", body: body, header: sign(time.Now(), body)}); status != http.StatusAccepted {
		t.Fatalf("a request signed as configured was answered %d %s, want 202", status, out)
	}
	issue1870AwaitRows(t, c, name, "", 1)
}

// TestIssue2223_BadHeaderPatternsAreRefusedOnSave is the addition's save
// criterion.
func TestIssue2223_BadHeaderPatternsAreRefusedOnSave(t *testing.T) {
	c := connect(t)
	for pattern, why := range map[string]string{
		"{signature}{timestamp}":  "two placeholders with no text between them",
		"{timestamp}|{timestamp}": "more than once",
		"{nonce}|{signature}":     "{nonce}",
	} {
		auth := issue2223Verkada()
		auth["header_pattern"] = pattern
		status, out := c.rest(http.MethodPost, "/api/v1/admin/webhooks/sources", jsonBody(t, map[string]any{
			"name": issue1870Name("s2223-bad"), "connection": issue1870Conn, "auth": auth, "config": map[string]any{},
		}))
		if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out), why) {
			t.Errorf("saving header_pattern %q was answered %d %v, want 400 naming %q", pattern, status, out, why)
		}
	}
}

// issue2223Hash is the hash each existing preset signs with.
var issue2223Hash = map[string]func() hash.Hash{"sha256": sha256.New, "sha1": sha1.New}

// TestIssue2223_OutboundSignsAsBefore is the outbound criterion: an hmac
// connection configured with each scheme that existed before the template
// signs what a capturing receiver gets exactly as that scheme's definition
// gives, computed here by hand, for both forms of the body.
func TestIssue2223_OutboundSignsAsBefore(t *testing.T) {
	c := connect(t)
	var mu sync.Mutex
	var got []issue1996Captured
	capture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, issue1996Captured{header: r.Header.Clone(), body: raw})
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(capture.Close)

	// want is the signature header a scheme's definition gives for what was
	// sent: the signed bytes, the encoding and the layout.
	type scheme struct {
		keys map[string]any
		want func(h http.Header, body []byte) (string, string)
	}
	hexSig := func(prefix, msg string) string {
		return prefix + hex.EncodeToString(issue2223MAC(issue2223Hash["sha256"], []byte(issue1996Secret), []byte(msg)))
	}
	schemes := map[string]scheme{
		"github": {map[string]any{"hmac_preset": "github"}, func(h http.Header, b []byte) (string, string) {
			return "X-Hub-Signature-256", hexSig("sha256=", string(b))
		}},
		"platform": {map[string]any{"hmac_preset": "platform"}, func(h http.Header, b []byte) (string, string) {
			return "X-Signature", hexSig("sha256=", h.Get("X-Timestamp")+"."+string(b))
		}},
		"stripe": {map[string]any{"hmac_preset": "stripe"}, func(h http.Header, b []byte) (string, string) {
			ts, _, _ := strings.Cut(strings.TrimPrefix(h.Get("Stripe-Signature"), "t="), ",")
			return "Stripe-Signature", "t=" + ts + "," + hexSig("v1=", ts+"."+string(b))
		}},
		"standard_webhooks": {map[string]any{"hmac_preset": "standard_webhooks"}, func(h http.Header, b []byte) (string, string) {
			msg := h.Get("webhook-id") + "." + h.Get("webhook-timestamp") + "." + string(b)
			return "webhook-signature", "v1," + base64.StdEncoding.EncodeToString(issue2223MAC(sha256.New, []byte(issue1996Secret), []byte(msg)))
		}},
		"body sha1 base64": {
			map[string]any{"hmac_algorithm": "sha1", "hmac_encoding": "base64", "hmac_prefix": "sha1="},
			func(h http.Header, b []byte) (string, string) {
				return "X-Signature", "sha1=" + base64.StdEncoding.EncodeToString(issue2223MAC(issue2223Hash["sha1"], []byte(issue1996Secret), b))
			},
		},
	}
	for name, s := range schemes {
		conn := issue1996Connection(t, c, capture.URL, s.keys)
		for form, body := range map[string]any{
			"object": map[string]any{"id": "out-" + name, "type": "order.created"},
			"string": `{"id": "out-` + name + `", "type": "order.created"}`,
		} {
			mu.Lock()
			got = nil
			mu.Unlock()
			c.call("api_invoke_endpoint", map[string]any{
				"connection": conn, "method": "POST", "path": "/hooks/capture", "body": body, "purpose": issue2223Purpose,
			})
			mu.Lock()
			if len(got) != 1 {
				mu.Unlock()
				t.Fatalf("%s, %s body: the receiver got %d requests, want 1", name, form, len(got))
			}
			sent := got[0]
			mu.Unlock()
			header, want := s.want(sent.header, sent.body)
			if sent.header.Get(header) != want {
				t.Errorf("%s, %s body: %s is %q, want %q as the scheme defines it", name, form, header, sent.header.Get(header), want)
			}
		}
	}
}
