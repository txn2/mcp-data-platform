package contenturl

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var key = []byte("0123456789abcdef0123456789abcdef")

func TestSignVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	target := Target{AssetID: "asset-1", Version: 3, Expires: now.Add(time.Minute)}
	tok := Sign(key, target)

	got, err := Verify(key, tok, now)
	require.NoError(t, err)
	assert.Equal(t, target, got)

	_, err = Verify(key, tok, now.Add(time.Minute))
	require.ErrorIs(t, err, ErrExpired, "expired at its expiry")

	_, err = Verify([]byte("another key entirely, 32 bytes!!"), tok, now)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestVerifyRefusesWhatWasNotSigned(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	enc := base64.RawURLEncoding.EncodeToString
	forged := func(payload string) string { return enc([]byte(payload)) + "." + enc(mac(key, payload)) }
	for name, tok := range map[string]string{
		"no separator":    "abc",
		"payload not b64": "!!!." + enc([]byte("x")),
		"mac not b64":     enc([]byte("a|1|2")) + ".!!!",
		"wrong mac":       enc([]byte("a|1|9999999999")) + "." + enc([]byte("nope")),
		"two fields":      forged("a|1"),
		"bad version":     forged("a|x|9999999999"),
		"bad expiry":      forged("a|1|soon"),
		"no asset":        forged("|1|9999999999"),
	} {
		_, err := Verify(key, tok, now)
		assert.ErrorIs(t, err, ErrInvalid, name)
	}
}

func TestClampTTL(t *testing.T) {
	assert.Equal(t, DefaultTTL, ClampTTL(0))
	assert.Equal(t, 30*time.Second, ClampTTL(30*time.Second))
	assert.Equal(t, MaxTTL, ClampTTL(48*time.Hour))
}
