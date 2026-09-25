package whauth

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // G505: the test signs the way a sha1 sender does
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func sign(h func() hash.Hash, secret string, msg []byte) []byte {
	m := hmac.New(h, []byte(secret))
	_, _ = m.Write(msg)
	return m.Sum(nil)
}

func hmacSource() whsource.Source {
	return whsource.Source{Auth: whsource.Auth{
		Mode: whsource.AuthHMAC, Secret: "new", SignatureHeader: "X-Sig",
	}}.WithDefaults()
}

func headers(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestHMACBody(t *testing.T) {
	body := []byte(`{"id":1}`)
	src := hmacSource()
	good := hex.EncodeToString(sign(sha256.New, "new", body))

	assert.NoError(t, Verify(src, Request{Header: headers("X-Sig", good), Body: body}, now))
	assert.ErrorIs(t, Verify(src, Request{Header: headers(), Body: body}, now), ErrMissingSignature)
	assert.ErrorIs(t, Verify(src, Request{Header: headers("X-Sig", good), Body: []byte(`{"id":2}`)}, now), ErrBadSignature)
	assert.ErrorIs(t, Verify(src, Request{Header: headers("X-Sig", "zz"), Body: body}, now), ErrBadSignature)
}

func TestHMACPrefixBase64SHA1(t *testing.T) {
	body := []byte("payload")
	src := hmacSource()
	src.Auth.Algorithm = whsource.AlgorithmSHA1
	src.Auth.Encoding = whsource.EncodingBase64
	src.Auth.Prefix = "sha1="
	std := "sha1=" + base64.StdEncoding.EncodeToString(sign(sha1.New, "new", body))
	url := "sha1=" + base64.URLEncoding.EncodeToString(sign(sha1.New, "new", body))

	assert.NoError(t, Verify(src, Request{Header: headers("X-Sig", std), Body: body}, now))
	assert.NoError(t, Verify(src, Request{Header: headers("X-Sig", url), Body: body}, now))
	assert.ErrorIs(t, Verify(src, Request{Header: headers("X-Sig", std[5:]), Body: body}, now), ErrBadSignature,
		"a signature without the declared prefix is refused")
	assert.ErrorIs(t, Verify(src, Request{Header: headers("X-Sig", "sha1=!!"), Body: body}, now), ErrBadSignature)
}

func TestHMACTimestamp(t *testing.T) {
	body := []byte(`{}`)
	src := hmacSource()
	src.Auth.TimestampHeader = "X-Ts"
	src.Auth.Signed = whsource.SignedTimestampBody
	src = src.WithDefaults()

	sigAt := func(ts string) string {
		return hex.EncodeToString(sign(sha256.New, "new", append([]byte(ts+"."), body...)))
	}
	fresh := strconv.FormatInt(now.Add(-time.Minute).Unix(), 10)
	assert.NoError(t, Verify(src, Request{Header: headers("X-Sig", sigAt(fresh), "X-Ts", fresh), Body: body}, now))

	ms := strconv.FormatInt(now.UnixMilli(), 10)
	assert.NoError(t, Verify(src, Request{Header: headers("X-Sig", sigAt(ms), "X-Ts", ms), Body: body}, now),
		"a millisecond timestamp is read as one")

	stale := strconv.FormatInt(now.Add(-6*time.Minute).Unix(), 10)
	assert.ErrorIs(t, Verify(src, Request{Header: headers("X-Sig", sigAt(stale), "X-Ts", stale), Body: body}, now),
		ErrStaleTimestamp, "a valid signature over a timestamp outside the window is refused")
	future := strconv.FormatInt(now.Add(6*time.Minute).Unix(), 10)
	assert.ErrorIs(t, Verify(src, Request{Header: headers("X-Sig", sigAt(future), "X-Ts", future), Body: body}, now),
		ErrStaleTimestamp)

	assert.ErrorIs(t, Verify(src, Request{Header: headers("X-Sig", sigAt(fresh)), Body: body}, now), ErrMissingTimestamp)
	assert.ErrorIs(t, Verify(src, Request{Header: headers("X-Sig", sigAt("x"), "X-Ts", "x"), Body: body}, now), ErrBadTimestamp)

	other := strconv.FormatInt(now.Unix(), 10)
	assert.ErrorIs(t, Verify(src, Request{Header: headers("X-Sig", sigAt(fresh), "X-Ts", other), Body: body}, now),
		ErrBadSignature, "the timestamp is part of what is signed")
}

func TestHMACTimestampBodyOnly(t *testing.T) {
	body := []byte(`{}`)
	src := hmacSource()
	src.Auth.TimestampHeader = "X-Ts"
	src = src.WithDefaults()
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := hex.EncodeToString(sign(sha256.New, "new", body))
	assert.NoError(t, Verify(src, Request{Header: headers("X-Sig", sig, "X-Ts", ts), Body: body}, now),
		"signed=body checks the window but signs only the body")
}

func TestRotationOverlap(t *testing.T) {
	body := []byte("x")
	src := hmacSource()
	src.Auth.PreviousSecret = "old"
	src.Auth.PreviousUntil = now.Add(time.Hour)
	old := hex.EncodeToString(sign(sha256.New, "old", body))
	fresh := hex.EncodeToString(sign(sha256.New, "new", body))

	assert.NoError(t, Verify(src, Request{Header: headers("X-Sig", old), Body: body}, now))
	assert.NoError(t, Verify(src, Request{Header: headers("X-Sig", fresh), Body: body}, now))
	later := now.Add(time.Hour)
	assert.ErrorIs(t, Verify(src, Request{Header: headers("X-Sig", old), Body: body}, later), ErrBadSignature)
	assert.NoError(t, Verify(src, Request{Header: headers("X-Sig", fresh), Body: body}, later))
}

func TestHeaderAndPathToken(t *testing.T) {
	hdr := whsource.Source{Auth: whsource.Auth{Mode: whsource.AuthHeaderToken, Secret: "tok", Header: "X-Token"}}
	assert.NoError(t, Verify(hdr, Request{Header: headers("X-Token", "tok")}, now))
	assert.ErrorIs(t, Verify(hdr, Request{Header: headers("X-Token", "tik")}, now), ErrBadToken)
	assert.ErrorIs(t, Verify(hdr, Request{Header: headers()}, now), ErrMissingToken)

	path := whsource.Source{Auth: whsource.Auth{Mode: whsource.AuthPathToken, Secret: "tok"}}
	assert.NoError(t, Verify(path, Request{Header: headers(), PathToken: "tok"}, now))
	assert.ErrorIs(t, Verify(path, Request{Header: headers(), PathToken: "x"}, now), ErrBadToken)
	assert.ErrorIs(t, Verify(path, Request{Header: headers()}, now), ErrMissingToken)
}

func TestBasic(t *testing.T) {
	src := whsource.Source{Auth: whsource.Auth{Mode: whsource.AuthBasic, Secret: "pw", Username: "sender"}}
	basic := func(u, p string) http.Header {
		return headers("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u+":"+p)))
	}
	assert.NoError(t, Verify(src, Request{Header: basic("sender", "pw")}, now))
	assert.ErrorIs(t, Verify(src, Request{Header: basic("sender", "no")}, now), ErrBadCredentials)
	assert.ErrorIs(t, Verify(src, Request{Header: basic("other", "pw")}, now), ErrBadCredentials)
	assert.ErrorIs(t, Verify(src, Request{Header: headers()}, now), ErrBadCredentials)
}

func TestUnknownMode(t *testing.T) {
	assert.ErrorIs(t, Verify(whsource.Source{}, Request{Header: headers()}, now), ErrUnknownMode)
}
