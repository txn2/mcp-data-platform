package whauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// compiled is the verifier for a source configured with a preset.
func compiled(t *testing.T, a whsource.Auth) *Verifier {
	t.Helper()
	src := whsource.Source{Name: "s", Connection: "c", Auth: a}.WithDefaults()
	require.NoError(t, whsource.Validate(src))
	v, err := Compile(src)
	require.NoError(t, err)
	return v
}

func TestEd25519Discord(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	v := compiled(t, whsource.Auth{Preset: whsource.PresetDiscord, Secret: hex.EncodeToString(pub)})
	body := []byte(`{"type":1}`)
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := hex.EncodeToString(ed25519.Sign(priv, append([]byte(ts), body...)))
	req := Request{Header: headers("X-Signature-Ed25519", sig, "X-Signature-Timestamp", ts), Body: body}

	assert.NoError(t, v.Verify(req, now))
	assert.ErrorIs(t, v.Verify(Request{Header: req.Header, Body: []byte(`{"type":2}`)}, now), ErrBadSignature)
	assert.ErrorIs(t, v.Verify(Request{Header: headers("X-Signature-Timestamp", ts), Body: body}, now), ErrMissingSignature)
	assert.ErrorIs(t, v.Verify(Request{Header: headers("X-Signature-Ed25519", "zz", "X-Signature-Timestamp", ts), Body: body}, now),
		ErrBadSignature)
	assert.ErrorIs(t, v.Verify(req, now.Add(time.Hour)), ErrStaleTimestamp)

	other, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	wrong := compiled(t, whsource.Auth{Preset: whsource.PresetDiscord, Secret: hex.EncodeToString(other)})
	assert.ErrorIs(t, wrong.Verify(req, now), ErrBadSignature)
}

func TestEd25519Base64(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	v := compiled(t, whsource.Auth{Mode: whsource.AuthEd25519, Secret: hex.EncodeToString(pub), SignatureHeader: "X-Sig", Encoding: whsource.EncodingBase64})
	body := []byte(`{}`)
	raw := ed25519.Sign(priv, body)
	for _, enc := range []string{base64.StdEncoding.EncodeToString(raw), base64.URLEncoding.EncodeToString(raw)} {
		assert.NoError(t, v.Verify(Request{Header: headers("X-Sig", enc), Body: body}, now), enc)
	}
	assert.ErrorIs(t, v.Verify(Request{Header: headers("X-Sig", "!!"), Body: body}, now), ErrBadSignature)
}

func TestBodyTokenMattermost(t *testing.T) {
	v := compiled(t, whsource.Auth{Preset: whsource.PresetMattermost, Secret: "tok"})
	assert.NoError(t, v.Verify(Request{Header: headers("Content-Type", "application/json"), Body: []byte(`{"token":"tok","text":"hi"}`)}, now))
	assert.NoError(t, v.Verify(Request{
		Header: headers("Content-Type", "application/x-www-form-urlencoded"), Body: []byte("token=tok&text=hi"),
	}, now), "a form body's fields are read")
	assert.ErrorIs(t, v.Verify(Request{Body: []byte(`{"token":"other"}`)}, now), ErrBadToken)
	assert.ErrorIs(t, v.Verify(Request{Body: []byte(`{"text":"hi"}`)}, now), ErrMissingToken)
	assert.ErrorIs(t, v.Verify(Request{Body: []byte(`not json`)}, now), ErrMissingToken)
	assert.ErrorIs(t, v.Verify(Request{Body: []byte(`{"token":7}`)}, now), ErrMissingToken)
}

func TestBodyTokenGraphEveryItem(t *testing.T) {
	v := compiled(t, whsource.Auth{Preset: whsource.PresetMicrosoftGraph, Secret: "state"})
	assert.NoError(t, v.Verify(Request{Body: []byte(`{"value":[{"clientState":"state"},{"clientState":"state"}]}`)}, now))
	assert.ErrorIs(t, v.Verify(Request{Body: []byte(`{"value":[{"clientState":"state"},{"id":"x"}]}`)}, now), ErrMissingToken,
		"one item without the token refuses the request")
	assert.ErrorIs(t, v.Verify(Request{Body: []byte(`{"value":[{"clientState":"state"},{"clientState":"no"}]}`)}, now), ErrBadToken)
	assert.ErrorIs(t, v.Verify(Request{Body: []byte(`{"value":[]}`)}, now), ErrMissingToken)
}

func TestJWTMonday(t *testing.T) {
	v := compiled(t, whsource.Auth{Preset: whsource.PresetMonday, Secret: "signing"})
	sign := func(key string, claims jwt.MapClaims, method jwt.SigningMethod) string {
		s, err := jwt.NewWithClaims(method, claims).SignedString([]byte(key))
		require.NoError(t, err)
		return s
	}
	good := sign("signing", jwt.MapClaims{"exp": now.Add(time.Minute).Unix()}, jwt.SigningMethodHS256)
	assert.NoError(t, v.Verify(Request{Header: headers("Authorization", good)}, now))
	assert.NoError(t, v.Verify(Request{Header: headers("Authorization", "Bearer "+good)}, now))
	noExp := sign("signing", jwt.MapClaims{"dat": "x"}, jwt.SigningMethodHS256)
	assert.NoError(t, v.Verify(Request{Header: headers("Authorization", noExp)}, now), "a token with no exp does not expire")

	expired := sign("signing", jwt.MapClaims{"exp": now.Add(-time.Minute).Unix()}, jwt.SigningMethodHS256)
	assert.ErrorIs(t, v.Verify(Request{Header: headers("Authorization", expired)}, now), ErrExpiredToken)
	assert.ErrorIs(t, v.Verify(Request{Header: headers("Authorization", sign("other", jwt.MapClaims{}, jwt.SigningMethodHS256))}, now),
		ErrBadToken)
	assert.ErrorIs(t, v.Verify(Request{Header: headers("Authorization", sign("signing", jwt.MapClaims{}, jwt.SigningMethodHS512))}, now),
		ErrBadToken, "only HS256 is accepted")
	assert.ErrorIs(t, v.Verify(Request{Header: headers()}, now), ErrMissingToken)
}

func TestBodyTimestampLinear(t *testing.T) {
	v := compiled(t, whsource.Auth{Preset: whsource.PresetLinear, Secret: "lin"})
	body := []byte(`{"action":"create","webhookTimestamp":` + strconv.FormatInt(now.UnixMilli(), 10) + `}`)
	sig := hex.EncodeToString(sign(sha256.New, "lin", body))
	assert.NoError(t, v.Verify(Request{Header: headers("Linear-Signature", sig), Body: body}, now))
	assert.ErrorIs(t, v.Verify(Request{Header: headers("Linear-Signature", sig), Body: body}, now.Add(2*time.Minute)),
		ErrStaleTimestamp, "Linear's window is a minute")
	noTS := []byte(`{"action":"create"}`)
	missing := v.Verify(Request{Header: headers("Linear-Signature", hex.EncodeToString(sign(sha256.New, "lin", noTS))), Body: noTS}, now)
	require.ErrorIs(t, missing, ErrMissingBodyTimestamp)
	assert.Contains(t, missing.Error(), "webhookTimestamp", "the refusal names the body field it read")
	assert.NotContains(t, missing.Error(), "header", "a body-timestamp source is never told about a header")
	notJSON := []byte(`{"action":"create","webhookTimestamp":1:}`)
	unreadable := v.Verify(Request{Header: headers("Linear-Signature", hex.EncodeToString(sign(sha256.New, "lin", notJSON))), Body: notJSON}, now)
	require.ErrorIs(t, unreadable, ErrUnreadableBody)
	assert.NotContains(t, unreadable.Error(), "header")
}

func TestHeaderTimestampStillNamesTheHeader(t *testing.T) {
	body := []byte(`{"type":"event_callback"}`)
	slack := compiled(t, whsource.Auth{Preset: whsource.PresetSlack, Secret: "sec"})
	sig := "v0=" + hex.EncodeToString(sign(sha256.New, "sec", []byte("v0::"+string(body))))
	assert.ErrorIs(t, slack.Verify(Request{Header: headers("X-Slack-Signature", sig), Body: body}, now), ErrMissingTimestamp)
}

func TestSlackAndZoomTemplates(t *testing.T) {
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"event_callback"}`)
	sig := "v0=" + hex.EncodeToString(sign(sha256.New, "sec", []byte("v0:"+ts+":"+string(body))))
	slack := compiled(t, whsource.Auth{Preset: whsource.PresetSlack, Secret: "sec"})
	assert.NoError(t, slack.Verify(Request{Header: headers("X-Slack-Signature", sig, "X-Slack-Request-Timestamp", ts), Body: body}, now))
	assert.ErrorIs(t, slack.Verify(Request{Header: headers("X-Slack-Signature", sig, "X-Slack-Request-Timestamp", ts), Body: body},
		now.Add(10*time.Minute)), ErrStaleTimestamp)
	zoom := compiled(t, whsource.Auth{Preset: whsource.PresetZoom, Secret: "sec"})
	assert.NoError(t, zoom.Verify(Request{Header: headers("x-zm-signature", sig, "x-zm-request-timestamp", ts), Body: body}, now))
}

func TestHeaderPatternVerkada(t *testing.T) {
	v := compiled(t, whsource.Auth{
		Mode: whsource.AuthHMAC, Secret: "vk", SignatureHeader: "Verkada-Signature",
		HeaderPattern: "{timestamp}|{signature}", Signed: "{body}|{timestamp}",
	})
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"webhook_type":"notification"}`)
	mac := hex.EncodeToString(sign(sha256.New, "vk", []byte(string(body)+"|"+ts)))
	assert.NoError(t, v.Verify(Request{Header: headers("Verkada-Signature", ts+"|"+mac), Body: body}, now))
	assert.ErrorIs(t, v.Verify(Request{Header: headers("Verkada-Signature", ts+"|"+mac), Body: []byte(`{"webhook_type":"notificatioN"}`)}, now),
		ErrBadSignature)
	assert.ErrorIs(t, v.Verify(Request{Header: headers("Verkada-Signature", ts+mac), Body: body}, now), ErrBadSignature)
	assert.ErrorIs(t, v.Verify(Request{Header: headers("Verkada-Signature", ts+"|"+mac), Body: body}, now.Add(time.Hour)), ErrStaleTimestamp)
}

func TestVerifyCompilesEachCall(t *testing.T) {
	src := whsource.Source{Auth: whsource.Auth{Mode: whsource.AuthHMAC, Secret: "k", SignatureHeader: "X", Signed: "{nonce}"}}
	assert.ErrorIs(t, Verify(src, Request{Header: headers("X", "00")}, now), ErrBadSignature)
	unknown, err := Compile(whsource.Source{Auth: whsource.Auth{Mode: "telepathy"}})
	require.NoError(t, err, "a form's source has no mode and is still served")
	assert.ErrorIs(t, unknown.Verify(Request{}, now), ErrUnknownMode)
}

func TestRedactRemovesTheVerifiedToken(t *testing.T) {
	doc := func(s string) map[string]any {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(s), &m))
		return m
	}
	t.Run("mattermost", func(t *testing.T) {
		v := compiled(t, whsource.Auth{Preset: whsource.PresetMattermost, Secret: "tok"})
		d := doc(`{"token":"tok","text":"hi"}`)
		v.Redact(d, now)
		assert.Equal(t, map[string]any{"text": "hi"}, d)
	})
	t.Run("graph, every item", func(t *testing.T) {
		v := compiled(t, whsource.Auth{Preset: whsource.PresetMicrosoftGraph, Secret: "state"})
		d := doc(`{"value":[{"clientState":"state","id":"1"},{"clientState":"state","id":"2"}]}`)
		v.Redact(d, now)
		assert.Equal(t, map[string]any{"value": []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}}}, d)
	})
	t.Run("the previous secret, inside its overlap", func(t *testing.T) {
		a := whsource.Auth{Preset: whsource.PresetMattermost, Secret: "old"}.Rotate("new", time.Hour, now)
		v := compiled(t, a)
		d := doc(`{"token":"old"}`)
		v.Redact(d, now)
		assert.Empty(t, d)
	})
	t.Run("a value that is not a secret is kept", func(t *testing.T) {
		v := compiled(t, whsource.Auth{Preset: whsource.PresetMattermost, Secret: "tok"})
		d := doc(`{"token":"someone-else"}`)
		v.Redact(d, now)
		assert.Equal(t, map[string]any{"token": "someone-else"}, d)
	})
	t.Run("a header token a sender repeats in its body", func(t *testing.T) {
		v := compiled(t, whsource.Auth{Mode: whsource.AuthHeaderToken, Header: "X-Token", Secret: "tok"})
		d := doc(`{"token":"tok","nested":{"list":["tok","other"]},"id":"1"}`)
		v.Redact(d, now)
		assert.Equal(t, map[string]any{"nested": map[string]any{"list": []any{nil, "other"}}, "id": "1"}, d)
	})
	t.Run("an ed25519 public key is no secret", func(t *testing.T) {
		key := strings.Repeat("ab", 32)
		v := compiled(t, whsource.Auth{Preset: whsource.PresetDiscord, Secret: key})
		d := doc(`{"key":"` + key + `"}`)
		v.Redact(d, now)
		assert.Equal(t, map[string]any{"key": key}, d)
	})
}
