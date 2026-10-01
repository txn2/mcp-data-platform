package whauth

import (
	"bytes"
	"context"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// interopCase is one signing convention, written once as an outbound
// connection's keys and once as an inbound source's auth, with the same
// values (#1996).
type interopCase struct {
	connection map[string]any
	source     whsource.Auth
}

func interopCases() map[string]interopCase {
	return map[string]interopCase{
		"preset standard_webhooks": {
			connection: map[string]any{"hmac_preset": "standard_webhooks"},
			source: whsource.Auth{
				SignatureHeader: "webhook-signature", Encoding: "base64", Prefix: "v1,",
				Signed: "id.timestamp.body", TimestampHeader: "webhook-timestamp", IDHeader: "webhook-id",
			},
		},
		"preset github": {
			connection: map[string]any{"hmac_preset": "github"},
			source:     whsource.Auth{SignatureHeader: "X-Hub-Signature-256", Prefix: "sha256="},
		},
		"preset stripe": {
			connection: map[string]any{"hmac_preset": "stripe"},
			source:     whsource.Auth{SignatureHeader: "Stripe-Signature", HeaderFormat: "stripe"},
		},
		"preset platform": {
			connection: map[string]any{"hmac_preset": "platform"},
			source: whsource.Auth{
				SignatureHeader: "X-Signature", Prefix: "sha256=", Signed: "timestamp.body", TimestampHeader: "X-Timestamp",
			},
		},
		"signed body, sha512 base64": {
			connection: map[string]any{"hmac_algorithm": "sha512", "hmac_encoding": "base64"},
			source:     whsource.Auth{SignatureHeader: "X-Signature", Algorithm: "sha512", Encoding: "base64"},
		},
		"signed timestamp.body in milliseconds, sha1": {
			connection: map[string]any{
				"hmac_algorithm": "sha1", "hmac_signed": "timestamp.body",
				"hmac_timestamp_header": "X-Ts", "hmac_timestamp_unit": "milliseconds",
			},
			source: whsource.Auth{SignatureHeader: "X-Signature", Algorithm: "sha1", Signed: "timestamp.body", TimestampHeader: "X-Ts"},
		},
		"signed id.timestamp.body": {
			connection: map[string]any{
				"hmac_signed": "id.timestamp.body", "hmac_timestamp_header": "X-Ts", "hmac_id_header": "X-Id",
			},
			source: whsource.Auth{SignatureHeader: "X-Signature", Signed: "id.timestamp.body", TimestampHeader: "X-Ts", IDHeader: "X-Id"},
		},
	}
}

// signed sends body through an hmac connection's authenticator and returns the
// request it signed.
func signed(t *testing.T, keys map[string]any, secret string, body []byte) *http.Request {
	t.Helper()
	cfg := map[string]any{"auth_mode": "hmac", "credential": secret}
	maps.Copy(cfg, keys)
	c, err := upstreamauth.Parse("api", "apigateway", "https://receiver.example.com", cfg)
	require.NoError(t, err)
	require.NoError(t, c.Validate())
	auth, err := upstreamauth.NewAuthenticator(c)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://receiver.example.com/hooks/x", bytes.NewReader(body))
	require.NoError(t, err)
	require.NoError(t, auth.Apply(req))
	return req
}

// TestOutboundSignerAgreesWithInboundVerifier is #1996's unit acceptance: for
// each preset and each signed form, what an hmac connection signs verifies
// with whauth.Verify configured with the same values, and stops verifying
// with a one-byte change to the body, the timestamp or the id.
func TestOutboundSignerAgreesWithInboundVerifier(t *testing.T) {
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	body := []byte(`{"order":"A-1001","total":42}`)
	for name, tc := range interopCases() {
		t.Run(name, func(t *testing.T) {
			auth := tc.source
			auth.Mode, auth.Secret = whsource.AuthHMAC, secret
			src := whsource.Source{Auth: auth}.WithDefaults()
			valid := whsource.Source{Name: "x", Connection: "c", Auth: src.Auth}.WithDefaults()
			require.NoError(t, whsource.Validate(valid))

			req := signed(t, tc.connection, secret, body)
			at := time.Now()
			verify := func(h http.Header, b []byte) error {
				return Verify(src, Request{Header: h, Body: b}, at)
			}
			require.NoError(t, verify(req.Header, body))

			tampered := append([]byte{}, body...)
			tampered[len(tampered)-2]++
			assert.ErrorIs(t, verify(req.Header, tampered), ErrBadSignature, "a one-byte change to the body")

			wrong := Verify(whsource.Source{Auth: withSecret(src.Auth, "other")}, Request{Header: req.Header, Body: body}, at)
			assert.ErrorIs(t, wrong, ErrBadSignature, "another secret")

			if h := src.Auth.TimestampHeader; h != "" {
				ts := req.Header.Get(h)
				n, err := strconv.ParseInt(ts, 10, 64)
				require.NoError(t, err)
				moved := req.Header.Clone()
				moved.Set(h, strconv.FormatInt(n+1, 10))
				assert.ErrorIs(t, verify(moved, body), ErrBadSignature, "a one-unit change to the timestamp")
			}
			if src.Auth.HeaderFormat == whsource.HeaderFormatStripe {
				moved := req.Header.Clone()
				ts, rest, ok := strings.Cut(strings.TrimPrefix(moved.Get(src.Auth.SignatureHeader), "t="), ",")
				require.True(t, ok)
				n, err := strconv.ParseInt(ts, 10, 64)
				require.NoError(t, err)
				moved.Set(src.Auth.SignatureHeader, "t="+strconv.FormatInt(n+1, 10)+","+rest)
				assert.ErrorIs(t, verify(moved, body), ErrBadSignature, "a one-second change to the timestamp in the signature header")
			}
			if h := src.Auth.IDHeader; h != "" {
				assert.NotEmpty(t, req.Header.Get(h), "a delivery id is generated when the call sets none")
				moved := req.Header.Clone()
				moved.Set(h, req.Header.Get(h)+"x")
				assert.ErrorIs(t, verify(moved, body), ErrBadSignature, "a one-byte change to the id")
				moved.Del(h)
				assert.ErrorIs(t, verify(moved, body), ErrMissingID)
			}
		})
	}
}

func withSecret(a whsource.Auth, secret string) whsource.Auth {
	a.Secret = secret
	return a
}

// TestReplayOutsideTheTolerance signs a request now and verifies it ten
// minutes later: the signature is right, and the request is refused as stale.
func TestReplayOutsideTheTolerance(t *testing.T) {
	for _, preset := range []string{"platform", "stripe", "standard_webhooks"} {
		tc := interopCases()["preset "+preset]
		auth := tc.source
		auth.Mode, auth.Secret = whsource.AuthHMAC, "s"
		src := whsource.Source{Auth: auth}.WithDefaults()
		req := signed(t, tc.connection, "s", []byte(`{}`))
		assert.NoError(t, Verify(src, Request{Header: req.Header, Body: []byte(`{}`)}, time.Now()), preset)
		assert.ErrorIs(t, Verify(src, Request{Header: req.Header, Body: []byte(`{}`)}, time.Now().Add(10*time.Minute)),
			ErrStaleTimestamp, preset)
	}
}
