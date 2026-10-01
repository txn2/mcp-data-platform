package hmacsig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGitHubReferenceVector is the example GitHub publishes for validating
// X-Hub-Signature-256: the secret "It's a Secret to Everybody" over the body
// "Hello, World!".
func TestGitHubReferenceVector(t *testing.T) {
	p, ok := LookupPreset(PresetGitHub)
	require.True(t, ok)
	got := p.Header("It's a Secret to Everybody", "", "", []byte("Hello, World!"))
	assert.Equal(t, "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17", got)
	assert.Equal(t, "X-Hub-Signature-256", p.SignatureHeader)
}

// TestStandardWebhooksReferenceVector is the example the Standard Webhooks
// specification's reference libraries are tested with: a whsec_ secret, whose
// key is the base64 after the prefix, signing id.timestamp.body.
func TestStandardWebhooksReferenceVector(t *testing.T) {
	p, ok := LookupPreset(PresetStandardWebhooks)
	require.True(t, ok)
	got := p.Header("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", "msg_p5jXN8AQM9LWM0D4loKWxJek", "1614265330",
		[]byte(`{"test": 2432232314}`))
	assert.Equal(t, "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=", got)
	assert.Equal(t, []string{"webhook-signature", "webhook-timestamp", "webhook-id"},
		[]string{p.SignatureHeader, p.TimestampHeader, p.IDHeader})
}

// TestStripeAgainstAnIndependentComputation computes Stripe's v1 signature by
// hand, HMAC-SHA256 of "<t>.<body>" in hex, and compares the header.
func TestStripeAgainstAnIndependentComputation(t *testing.T) {
	p, ok := LookupPreset(PresetStripe)
	require.True(t, ok)
	body := []byte(`{"id":"evt_1","type":"charge.succeeded"}`)
	mac := hmac.New(sha256.New, []byte("whsec_test"))
	_, _ = mac.Write([]byte("1492774577." + string(body)))
	want := "t=1492774577,v1=" + hex.EncodeToString(mac.Sum(nil))
	assert.Equal(t, want, p.Header("whsec_test", "", "1492774577", body),
		"a stripe secret is used as written: whsec_ is read as base64 only for id.timestamp.body")

	sig, err := p.Parse(want + ",v0=deadbeef")
	require.NoError(t, err)
	assert.Equal(t, "1492774577", sig.Timestamp)
	assert.True(t, p.Matches(sig, []string{"other", "whsec_test"}, "", sig.Timestamp, body))
	assert.False(t, p.Matches(sig, []string{"whsec_test"}, "", "1492774578", body))
}

func TestParse(t *testing.T) {
	s := Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingBase64, Prefix: "v1,", Signed: SignedIDTimestampBody}
	a := s.Header("k1", "id", "1", []byte("b"))
	b := s.Header("k2", "id", "1", []byte("b"))
	sig, err := s.Parse(a + " " + b + " v2,ignored")
	require.NoError(t, err)
	assert.Len(t, sig.MACs, 2, "a header may carry several signatures, separated by spaces")
	assert.True(t, s.Matches(sig, []string{"k2"}, "id", "1", []byte("b")))

	for name, value := range map[string]string{
		"no prefix":        "abc",
		"bad base64":       "v1,!!!",
		"empty":            "",
		"stripe no time":   "v1=00",
		"stripe no v1":     "t=1",
		"stripe junk":      "nonsense",
		"stripe bad v1 hx": "t=1,v1=zz",
	} {
		scheme := s
		if len(name) > 6 && name[:6] == "stripe" {
			scheme = Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingHex, Format: FormatStripe}
		}
		_, err := scheme.Parse(value)
		assert.ErrorIs(t, err, ErrMalformed, name)
	}

	url := Scheme{Encoding: EncodingBase64}
	sig, err = url.Parse("-_8=")
	require.NoError(t, err, "base64 is read in the URL alphabet too")
	assert.Equal(t, []byte{0xfb, 0xff}, sig.MACs[0])
	_, err = Scheme{Encoding: EncodingHex}.Parse("ABCD")
	require.NoError(t, err, "hex is read in either case")
}

func TestPayloadAndAlgorithms(t *testing.T) {
	body := []byte("b")
	assert.Equal(t, "b", string(Scheme{Signed: SignedBody}.Payload("i", "t", body)))
	assert.Equal(t, "t.b", string(Scheme{Signed: SignedTimestampBody}.Payload("i", "t", body)))
	assert.Equal(t, "i.t.b", string(Scheme{Signed: SignedIDTimestampBody}.Payload("i", "t", body)))
	assert.Equal(t, "t.b", string(Scheme{Format: FormatStripe}.Payload("i", "t", body)))

	lengths := map[string]int{AlgorithmSHA1: 20, AlgorithmSHA256: 32, AlgorithmSHA512: 64}
	for alg, n := range lengths {
		assert.Len(t, Scheme{Algorithm: alg}.MAC("k", "", "", body), n, alg)
	}

	assert.False(t, Scheme{Signed: SignedBody}.UsesTimestamp())
	assert.True(t, Scheme{Format: FormatStripe}.UsesTimestamp())
	assert.False(t, Scheme{Format: FormatStripe, Signed: SignedIDTimestampBody}.UsesID())
	assert.True(t, Scheme{Signed: SignedIDTimestampBody}.UsesID())

	notBase64 := Scheme{Signed: SignedIDTimestampBody}
	assert.Equal(t, notBase64.MAC("whsec_!!", "i", "t", body), Scheme{Signed: SignedIDTimestampBody}.MAC("whsec_!!", "i", "t", body))
	assert.NotEqual(t, notBase64.MAC("whsec_!!", "i", "t", body), notBase64.MAC("whsec_AA==", "i", "t", body),
		"a whsec_ secret that is not base64 is used as written")
}

func TestValidators(t *testing.T) {
	assert.True(t, ValidAlgorithm("sha512"))
	assert.False(t, ValidAlgorithm("md5"))
	assert.True(t, ValidEncoding("base64"))
	assert.False(t, ValidEncoding("base32"))
	assert.True(t, ValidSigned("id.timestamp.body"))
	assert.False(t, ValidSigned("timestamp"))
	assert.True(t, ValidFormat(""))
	assert.True(t, ValidFormat("stripe"))
	assert.False(t, ValidFormat("svix"))
	_, ok := LookupPreset("svix")
	assert.False(t, ok)
	for _, name := range []string{PresetStandardWebhooks, PresetGitHub, PresetStripe, PresetPlatform} {
		p, ok := LookupPreset(name)
		require.True(t, ok, name)
		assert.NotEmpty(t, p.SignatureHeader, name)
	}
}
