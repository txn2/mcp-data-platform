package whsetup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

func TestChallengeOf(t *testing.T) {
	for body, want := range map[string]bool{
		`{"challenge":"c"}`:                           true,
		`{"challenge":"c","type":"url_verification"}`: true,
		`{"challenge":"c","token":"t"}`:               true,
		`{"challenge":""}`:                            false,
		`{"challenge":7}`:                             false,
		`{"challenge":"c","type":"event_callback"}`:   false,
		`{"challenge":"c","type":7}`:                  false,
		`{"challenge":"c","event":{}}`:                false,
		`not json`:                                    false,
	} {
		_, ok := challengeOf([]byte(body), "challenge")
		assert.Equal(t, want, ok, body)
	}
}

func source(handshake, secret string) whsource.Source {
	return whsource.Source{Auth: whsource.Auth{Secret: secret}, Config: whsource.Config{Handshake: handshake}}
}

func TestFromQuery(t *testing.T) {
	a, ok := FromQuery(source(whsource.HandshakeGraphValidation, ""), url.Values{"validationToken": {"Validation: x y"}})
	require.True(t, ok)
	assert.Equal(t, ContentText, a.ContentType)
	assert.Equal(t, "Validation: x y", string(a.Body))
	_, ok = FromQuery(source(whsource.HandshakeGraphValidation, ""), url.Values{})
	assert.False(t, ok)
	_, ok = FromQuery(source(whsource.HandshakeNone, ""), url.Values{"validationToken": {"t"}})
	assert.False(t, ok, "only a graph_validation source answers it")
}

func TestBeforeVerification(t *testing.T) {
	a, ok := BeforeVerification(source(whsource.HandshakeChallengeEcho, ""), []byte(`{"challenge":"c1","type":"url_verification","token":"t"}`))
	require.True(t, ok)
	assert.JSONEq(t, `{"challenge":"c1"}`, string(a.Body))
	src := source(whsource.HandshakeChallengeEcho, "")
	src.Config.ChallengeField = "code"
	a, ok = BeforeVerification(src, []byte(`{"code":"c2"}`))
	require.True(t, ok)
	assert.JSONEq(t, `{"code":"c2"}`, string(a.Body))
	_, ok = BeforeVerification(source(whsource.HandshakeZoom, ""), []byte(`{"challenge":"c1"}`))
	assert.False(t, ok)
}

func TestAfterVerification(t *testing.T) {
	a, ok := AfterVerification(source(whsource.HandshakeZoom, "zs"), []byte(`{"event":"endpoint.url_validation","payload":{"plainToken":"p1"}}`))
	require.True(t, ok)
	m := hmac.New(sha256.New, []byte("zs"))
	_, _ = m.Write([]byte("p1"))
	assert.JSONEq(t, `{"plainToken":"p1","encryptedToken":"`+hex.EncodeToString(m.Sum(nil))+`"}`, string(a.Body))
	for _, body := range []string{`{"event":"meeting.started"}`, `{"event":"endpoint.url_validation","payload":{}}`, `nope`} {
		_, ok = AfterVerification(source(whsource.HandshakeZoom, "zs"), []byte(body))
		assert.False(t, ok, body)
	}
	a, ok = AfterVerification(source(whsource.HandshakeDiscord, ""), []byte(`{"type":1}`))
	require.True(t, ok)
	assert.JSONEq(t, `{"type":1}`, string(a.Body))
	_, ok = AfterVerification(source(whsource.HandshakeDiscord, ""), []byte(`{"type":2}`))
	assert.False(t, ok, "an interaction is an event")
	_, ok = AfterVerification(source(whsource.HandshakeNone, ""), []byte(`{"type":1}`))
	assert.False(t, ok)
}

// A check whose value holds a character HTML gives meaning to is not
// answered: no answer writes such a value back.
func TestAValueWithMarkupIsNotEchoed(t *testing.T) {
	const markup = `<script>alert(1)</script>`
	_, ok := FromQuery(source(whsource.HandshakeGraphValidation, ""), url.Values{"validationToken": {markup}})
	assert.False(t, ok, "graph_validation")
	_, ok = BeforeVerification(source(whsource.HandshakeChallengeEcho, ""), []byte(`{"challenge":"a&b"}`))
	assert.False(t, ok, "challenge_echo")
	_, ok = AfterVerification(source(whsource.HandshakeZoom, "zs"), []byte(`{"event":"endpoint.url_validation","payload":{"plainToken":"`+`\"x>"}}`))
	assert.False(t, ok, "zoom")
	for _, v := range []string{"", "<", ">", "&", "'", `"`} {
		_, ok = echoable(v)
		assert.False(t, ok, v)
	}
	got, ok := echoable("Validation: Testing client application reachability")
	require.True(t, ok)
	assert.Equal(t, "Validation: Testing client application reachability", got)
}
