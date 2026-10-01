package upstreamauth

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hmacConfig(t *testing.T, keys map[string]any) Config {
	t.Helper()
	cfg := map[string]any{"auth_mode": AuthModeHMAC, "credential": "secret"}
	maps.Copy(cfg, keys)
	c, err := Parse("api", "apigateway", "https://receiver.example.com", cfg)
	require.NoError(t, err)
	return c
}

func TestParseHMACPresetAndOverrides(t *testing.T) {
	c := hmacConfig(t, map[string]any{"hmac_preset": "github", "hmac_signature_header": "X-Custom"})
	assert.Equal(t, "X-Custom", c.HMAC.SignatureHeader, "an explicit key overrides the preset")
	assert.Equal(t, "sha256=", c.HMAC.Prefix, "the preset fills what is not set")
	assert.Equal(t, "X-Custom", c.AuthHeader())

	d := hmacConfig(t, nil)
	assert.Equal(t, HMACConfig{
		Algorithm: "sha256", Encoding: "hex", Signed: "body",
		SignatureHeader: DefaultHMACSignatureHeader, TimestampUnit: HMACTimestampSeconds,
	}, d.HMAC, "the defaults with no preset")
	require.NoError(t, d.Validate())

	s := hmacConfig(t, map[string]any{"hmac_header_format": "stripe", "hmac_signed": "body"})
	assert.Equal(t, "timestamp.body", s.HMAC.Signed, "stripe always signs the timestamp and the body")
}

func TestValidateHMACRefuses(t *testing.T) {
	cases := map[string]map[string]any{
		"no secret":               {"credential": ""},
		"unknown preset":          {"hmac_preset": "svix"},
		"bad algorithm":           {"hmac_algorithm": "md5"},
		"bad encoding":            {"hmac_encoding": "base32"},
		"bad signed":              {"hmac_signed": "headers"},
		"bad format":              {"hmac_header_format": "svix"},
		"bad unit":                {"hmac_timestamp_unit": "minutes"},
		"prefix with a newline":   {"hmac_prefix": "a\nb"},
		"bad signature header":    {"hmac_signature_header": "X Sig"},
		"timestamp without a hdr": {"hmac_signed": "timestamp.body"},
		"id without a header":     {"hmac_signed": "id.timestamp.body", "hmac_timestamp_header": "X-Ts"},
		"stripe with a ts header": {"hmac_header_format": "stripe", "hmac_timestamp_header": "X-Ts"},
	}
	for name, keys := range cases {
		t.Run(name, func(t *testing.T) {
			err := hmacConfig(t, keys).Validate()
			require.Error(t, err)
			assert.True(t, strings.HasPrefix(err.Error(), "apigateway: "), err)
		})
	}
	_, err := NewAuthenticator(Config{AuthMode: AuthModeHMAC})
	assert.Error(t, err, "an authenticator is not built without a secret")
}

func TestHMACApply(t *testing.T) {
	at := time.Unix(1_790_000_000, 123_000_000)
	c := hmacConfig(t, map[string]any{
		"hmac_signed": "id.timestamp.body", "hmac_timestamp_header": "X-Ts", "hmac_id_header": "X-Id",
		"hmac_timestamp_unit": "milliseconds",
	})
	a, err := newHMACAuth(c, func() time.Time { return at })
	require.NoError(t, err)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://r.example.com", strings.NewReader("body"))
	req.Header.Set("X-Id", "order-1001")
	require.NoError(t, a.Apply(req))
	assert.Equal(t, "order-1001", req.Header.Get("X-Id"), "the caller's delivery id is kept")
	assert.Equal(t, strconv.FormatInt(at.UnixMilli(), 10), req.Header.Get("X-Ts"))
	assert.Len(t, req.Header.Get("X-Signature"), 64)
	sent, _ := io.ReadAll(req.Body)
	assert.Equal(t, "body", string(sent), "signing leaves the body to be sent")

	first := req.Header.Get("X-Signature")
	again, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://r.example.com", strings.NewReader("body"))
	again.Header.Set("X-Id", "order-1001")
	at = at.Add(time.Second)
	require.NoError(t, a.Apply(again))
	assert.NotEqual(t, first, again.Header.Get("X-Signature"), "a request built again is signed afresh")

	gen, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://r.example.com", strings.NewReader("body"))
	require.NoError(t, a.Apply(gen))
	assert.True(t, strings.HasPrefix(gen.Header.Get("X-Id"), "msg_"), "an id is generated when the call sets none")

	a.newID = func() (string, error) { return "", errors.New("no entropy") }
	fail, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://r.example.com", strings.NewReader("body"))
	assert.Error(t, a.Apply(fail))
}

func TestHMACApplyBodies(t *testing.T) {
	body := hmacConfig(t, nil)
	a, err := newHMACAuth(body, time.Now)
	require.NoError(t, err)

	get, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://r.example.com", http.NoBody)
	assert.Error(t, a.Apply(get), "a GET with no body has nothing to sign when only the body is signed")

	ts := hmacConfig(t, map[string]any{"hmac_signed": "timestamp.body", "hmac_timestamp_header": "X-Ts"})
	b, err := newHMACAuth(ts, time.Now)
	require.NoError(t, err)
	get, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, "https://r.example.com", http.NoBody)
	require.NoError(t, b.Apply(get), "an empty body is signed when the timestamp is")
	assert.NotEmpty(t, get.Header.Get("X-Signature"))

	// A body with no GetBody is read once and put back.
	raw, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://r.example.com", http.NoBody)
	raw.Body = io.NopCloser(strings.NewReader("streamed"))
	raw.GetBody = nil
	require.NoError(t, a.Apply(raw))
	sent, _ := io.ReadAll(raw.Body)
	assert.Equal(t, "streamed", string(sent))
	rc, err := raw.GetBody()
	require.NoError(t, err)
	again, _ := io.ReadAll(rc)
	assert.Equal(t, "streamed", string(again))

	broken, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://r.example.com", http.NoBody)
	broken.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("gone") }
	assert.Error(t, a.Apply(broken))
	unreadable, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://r.example.com", http.NoBody)
	unreadable.Body = io.NopCloser(failingReader{})
	unreadable.GetBody = nil
	assert.Error(t, a.Apply(unreadable))
}

func TestHMACReservedHeaders(t *testing.T) {
	c := hmacConfig(t, map[string]any{"hmac_preset": "standard_webhooks"})
	assert.Error(t, c.ValidateCustomHeaders(map[string]string{"webhook-signature": "x"}))
	assert.Error(t, c.ValidateCustomHeaders(map[string]string{"Webhook-Timestamp": "1"}),
		"a caller may not choose the timestamp the platform signs")
	assert.NoError(t, c.ValidateCustomHeaders(map[string]string{"webhook-id": "order-1"}),
		"a caller may choose the delivery id")

	c.StaticHeaders = map[string]string{"webhook-id": "fixed"}
	assert.Error(t, c.ValidateStaticHeaders(), "a fixed id would make every delivery the same one")
	c.StaticHeaders = map[string]string{"webhook-timestamp": "1"}
	assert.Error(t, c.ValidateStaticHeaders())
	c.StaticHeaders = map[string]string{"X-Tenant": "acme"}
	assert.NoError(t, c.ValidateStaticHeaders())
}
