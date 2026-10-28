package hmacsig

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// referencePayload is the signed bytes as Payload built them before the
// template (#2223): the timestamp, or the id and the timestamp, then the body.
func referencePayload(s Scheme, id, ts string, body []byte) []byte {
	switch {
	case s.Format == FormatStripe, s.Signed == SignedTimestampBody:
		return append([]byte(ts+"."), body...)
	case s.Signed == SignedIDTimestampBody:
		return append([]byte(id+"."+ts+"."), body...)
	}
	return body
}

// TestPresetsSignAsBeforeTheTemplate holds the four presets that predate the
// template to the bytes and header values they produced, across bodies that
// include the empty one and bytes that are not UTF-8.
func TestPresetsSignAsBeforeTheTemplate(t *testing.T) {
	bodies := [][]byte{nil, {}, []byte(`{"a":1}`), {0xff, 0xfe, 0x00, '.', 0x80}, bytes.Repeat([]byte("x"), 4096)}
	for _, name := range []string{PresetStandardWebhooks, PresetGitHub, PresetStripe, PresetPlatform} {
		p, ok := LookupPreset(name)
		require.True(t, ok)
		for _, body := range bodies {
			want := referencePayload(p.Scheme, "msg_1", "1700000000", body)
			got := p.Payload("msg_1", "1700000000", body)
			assert.True(t, bytes.Equal(want, got), "%s signs the bytes it signed before, body %q", name, body)

			secret := "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
			c := p.compiled()
			mac := hmac.New(sha256.New, c.key(secret))
			_, _ = mac.Write(want)
			sig := p.encode(mac.Sum(nil))
			header := p.Prefix + sig
			if p.Format == FormatStripe {
				header = "t=1700000000,v1=" + sig
			}
			assert.Equal(t, header, p.Header(secret, "msg_1", "1700000000", body), "%s header", name)
		}
	}
}

func TestCompileTemplate(t *testing.T) {
	tmpl, err := CompileTemplate("v0:{timestamp}:{body}")
	require.NoError(t, err)
	assert.True(t, tmpl.UsesTimestamp())
	assert.False(t, tmpl.UsesID())
	assert.Equal(t, "v0:123:abc", string(tmpl.Bytes("", "123", []byte("abc"))))

	for named, text := range map[string]string{
		SignedBody: "{body}", SignedTimestampBody: "{timestamp}.{body}", SignedIDTimestampBody: "{id}.{timestamp}.{body}",
	} {
		tmpl, err := CompileTemplate(named)
		require.NoError(t, err)
		assert.Equal(t, text, tmpl.String())
	}

	for _, bad := range []string{"", "{timestamp}", "{body}{body}", "{nonce}.{body}", "{body", "v0:{timestamp}:{signature}"} {
		_, err := CompileTemplate(bad)
		assert.ErrorIs(t, err, ErrTemplate, bad)
	}
}

func TestCompilePattern(t *testing.T) {
	p, err := CompilePattern("{timestamp}|{signature}")
	require.NoError(t, err)
	assert.True(t, p.UsesTimestamp())
	sig, ts, ok := p.Read("1700000000|abcd")
	assert.True(t, ok)
	assert.Equal(t, "abcd", sig)
	assert.Equal(t, "1700000000", ts)
	assert.Equal(t, "1700000000|abcd", p.Write("abcd", "1700000000"))

	for _, value := range []string{"1700000000abcd", "|abcd", "1700000000|", ""} {
		_, _, ok := p.Read(value)
		assert.False(t, ok, value)
	}

	teams, err := CompilePattern("HMAC {signature}")
	require.NoError(t, err)
	sig, _, ok = teams.Read("HMAC abc=")
	assert.True(t, ok)
	assert.Equal(t, "abc=", sig)
	_, _, ok = teams.Read("Bearer abc=")
	assert.False(t, ok)

	trailing, err := CompilePattern("sig={signature};")
	require.NoError(t, err)
	_, _, ok = trailing.Read("sig=ab;extra")
	assert.False(t, ok, "a value with text past the pattern is not its layout")

	for bad, why := range map[string]string{
		"{signature}{timestamp}":  "two placeholders with no text between them",
		"{timestamp}|{timestamp}": "more than once",
		"{nonce}|{signature}":     "not one of",
		"{timestamp}":             "does not name {signature}",
		"{body}|{signature}":      "not one of",
	} {
		_, err := CompilePattern(bad)
		require.ErrorIs(t, err, ErrTemplate, bad)
		assert.Contains(t, err.Error(), why, bad)
	}
}

// TestSlackReferenceExample is the worked example in Slack's "Verifying
// requests from Slack": its signing secret over its form body and timestamp
// gives the signature the docs print.
func TestSlackReferenceExample(t *testing.T) {
	p, ok := LookupPreset(PresetSlack)
	require.True(t, ok)
	body := []byte("token=xyzz0WbapA4vBCDEFasx0q6G&team_id=T1DC2JH3J&team_domain=testteamnow&channel_id=G8PSS9T3V&channel_name=foobar&user_id=U2CERLKJA&user_name=roadrunner&command=%2Fwebhook-collect&text=&response_url=https%3A%2F%2Fhooks.slack.com%2Fcommands%2FT1DC2JH3J%2F397700885554%2F96rGlfmibIGlgcZRskXaIFfN&trigger_id=398738663015.47445629121.803a0bc887a14d10d2c447fce8b6703c")
	got := p.Header("8f742231b10e8888abcd99yyyzzz85a5", "", "1531420618", body)
	assert.Equal(t, "v0=a2114d57b48eac39b9ad189dd8316235a7b4a8d21a10bd27519666489c69b503", got)
}

// TestTeamsSecretIsBase64 signs as a Teams outgoing webhook does: the HMAC
// key is the bytes the security token decodes to, and the header is
// "HMAC <base64>".
func TestTeamsSecretIsBase64(t *testing.T) {
	p, ok := LookupPreset(PresetTeams)
	require.True(t, ok)
	key := []byte("0123456789abcdef0123456789abcdef")
	secret := base64.StdEncoding.EncodeToString(key)
	body := []byte(`{"type":"message","text":"hi"}`)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(body)
	want := "HMAC " + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	assert.Equal(t, want, p.Header(secret, "", "", body))

	sig, err := p.Parse(want)
	require.NoError(t, err)
	assert.True(t, p.Matches(sig, []string{secret}, "", "", body))
	assert.False(t, p.Matches(sig, []string{secret}, "", "", []byte(`{}`)))
}

// TestPatternSchemeSignsAndReads is the Verkada configuration #2223 names.
func TestPatternSchemeSignsAndReads(t *testing.T) {
	s := Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingHex, Signed: "{body}|{timestamp}", HeaderPattern: "{timestamp}|{signature}"}
	c, err := s.Compile()
	require.NoError(t, err)
	assert.True(t, c.TimestampInHeader())
	assert.Equal(t, "{body}|{timestamp}", c.Template().String())
	body := []byte(`{"event":"motion"}`)
	mac := hmac.New(sha256.New, []byte("k"))
	_, _ = mac.Write([]byte(string(body) + "|1700000000"))
	want := "1700000000|" + hex.EncodeToString(mac.Sum(nil))
	assert.Equal(t, want, c.Header("k", "", "1700000000", body))

	sig, err := c.Parse(want)
	require.NoError(t, err)
	assert.Equal(t, "1700000000", sig.Timestamp)
	assert.True(t, c.Matches(sig, []string{"k"}, "", sig.Timestamp, body))
	_, err = c.Parse("1700000000" + hex.EncodeToString(mac.Sum(nil)))
	assert.ErrorIs(t, err, ErrMalformed)
	_, err = c.Parse("1700000000|zz")
	assert.ErrorIs(t, err, ErrMalformed)
}

func TestCompileRefusesBadScheme(t *testing.T) {
	_, err := Scheme{Signed: "{nonce}.{body}"}.Compile()
	assert.ErrorIs(t, err, ErrTemplate)
	_, err = Scheme{HeaderPattern: "{signature}{timestamp}"}.Compile()
	assert.ErrorIs(t, err, ErrTemplate)
	// A scheme that does not compile signs the body alone.
	broken := Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingHex, Signed: "{nonce}"}
	assert.Equal(t, "b", string(broken.Payload("", "", []byte("b"))))
}

func TestTemplateValidators(t *testing.T) {
	assert.True(t, ValidSigned("v0:{timestamp}:{body}"))
	assert.False(t, ValidSigned("{timestamp}"))
	assert.True(t, ValidSecretEncoding(""))
	assert.True(t, ValidSecretEncoding(SecretBase64))
	assert.False(t, ValidSecretEncoding("hex"))
	assert.Len(t, PresetNames(), 11)
	for _, n := range PresetNames() {
		_, ok := LookupPreset(n)
		assert.True(t, ok, n)
	}
}
