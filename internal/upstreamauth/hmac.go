package upstreamauth

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
	"github.com/txn2/mcp-data-platform/internal/hmacsig"
)

// AuthModeHMAC signs every request with an HMAC of its body, the way webhook
// receivers verify a sender: Standard Webhooks, GitHub, Stripe, and the
// platform's own inbound sources (#1996). The credential is the signing
// secret. The signing convention is hmacsig's, the one the inbound sources
// verify with, so a connection and a source configured with the same values
// agree.
const AuthModeHMAC = "hmac"

// The keys an hmac connection is configured with. They mirror an inbound
// source's auth settings, prefixed.
const (
	cfgKeyHMACPreset          = "hmac_preset"
	cfgKeyHMACAlgorithm       = "hmac_algorithm"
	cfgKeyHMACEncoding        = "hmac_encoding"
	cfgKeyHMACSignatureHeader = "hmac_signature_header"
	cfgKeyHMACPrefix          = "hmac_prefix"
	cfgKeyHMACSigned          = "hmac_signed"
	cfgKeyHMACHeaderFormat    = "hmac_header_format"
	cfgKeyHMACTimestampHeader = "hmac_timestamp_header"
	cfgKeyHMACTimestampUnit   = "hmac_timestamp_unit"
	cfgKeyHMACIDHeader        = "hmac_id_header"
)

// Units an hmac timestamp is written in.
const (
	HMACTimestampSeconds      = "seconds"
	HMACTimestampMilliseconds = "milliseconds"
)

// DefaultHMACSignatureHeader is the header a signature is written to when
// neither a preset nor the connection names one.
const DefaultHMACSignatureHeader = "X-Signature"

// deliveryIDBytes is the randomness in a delivery id the platform generates.
const deliveryIDBytes = 16

// decimal is the base a Unix timestamp is written in.
const decimal = 10

// HMACConfig is how an hmac connection signs.
type HMACConfig struct {
	// Preset is the convention the settings started from, empty for none.
	Preset string
	// Algorithm is sha256, sha1 or sha512.
	Algorithm string
	// Encoding is hex or base64.
	Encoding string
	// Prefix is written before the signature.
	Prefix string
	// Signed is body, timestamp.body or id.timestamp.body.
	Signed string
	// HeaderFormat is empty, or stripe for t=<timestamp>,v1=<signature>.
	HeaderFormat    string
	SignatureHeader string
	// TimestampHeader is where the timestamp is written; empty for a
	// scheme that signs none, and for the stripe format, which writes it
	// into the signature header.
	TimestampHeader string
	TimestampUnit   string
	// IDHeader carries the delivery id. A caller that sets it chooses the
	// id; otherwise one is generated per request.
	IDHeader string
}

// parseHMAC reads an hmac connection's settings: the preset first, then each
// key the connection sets over it, then the defaults for what is still unset.
func parseHMAC(cfg map[string]any) HMACConfig {
	h := HMACConfig{Preset: cfgmap.String(cfg, cfgKeyHMACPreset)}
	if p, ok := hmacsig.LookupPreset(h.Preset); ok {
		h.Algorithm, h.Encoding, h.Prefix, h.Signed, h.HeaderFormat = p.Algorithm, p.Encoding, p.Prefix, p.Signed, p.Format
		h.SignatureHeader, h.TimestampHeader, h.IDHeader = p.SignatureHeader, p.TimestampHeader, p.IDHeader
	}
	h.Algorithm = cfgmap.StringDefault(cfg, cfgKeyHMACAlgorithm, or(h.Algorithm, hmacsig.AlgorithmSHA256))
	h.Encoding = cfgmap.StringDefault(cfg, cfgKeyHMACEncoding, or(h.Encoding, hmacsig.EncodingHex))
	h.Signed = cfgmap.StringDefault(cfg, cfgKeyHMACSigned, or(h.Signed, hmacsig.SignedBody))
	h.Prefix = cfgmap.StringDefault(cfg, cfgKeyHMACPrefix, h.Prefix)
	h.HeaderFormat = cfgmap.StringDefault(cfg, cfgKeyHMACHeaderFormat, h.HeaderFormat)
	h.SignatureHeader = cfgmap.StringDefault(cfg, cfgKeyHMACSignatureHeader, or(h.SignatureHeader, DefaultHMACSignatureHeader))
	h.TimestampHeader = cfgmap.StringDefault(cfg, cfgKeyHMACTimestampHeader, h.TimestampHeader)
	h.TimestampUnit = cfgmap.StringDefault(cfg, cfgKeyHMACTimestampUnit, HMACTimestampSeconds)
	h.IDHeader = cfgmap.StringDefault(cfg, cfgKeyHMACIDHeader, h.IDHeader)
	if h.HeaderFormat == hmacsig.FormatStripe {
		h.Signed = hmacsig.SignedTimestampBody
	}
	return h
}

// scheme is the signing convention the settings describe.
func (h HMACConfig) scheme() hmacsig.Scheme {
	return hmacsig.Scheme{
		Algorithm: h.Algorithm, Encoding: h.Encoding, Prefix: h.Prefix, Signed: h.Signed, Format: h.HeaderFormat,
	}
}

// or returns v, or def when v is empty.
func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// validateHMACAuth refuses an hmac connection that could not sign a request
// a receiver would verify.
func (c Config) validateHMACAuth() error {
	h := c.HMAC
	if c.Credential == "" {
		return c.err("credential (the signing secret) is required when auth_mode is \"hmac\"")
	}
	if h.Preset != "" {
		if _, ok := hmacsig.LookupPreset(h.Preset); !ok {
			return c.errf("invalid %s %q (want standard_webhooks, github, stripe or platform)", cfgKeyHMACPreset, h.Preset)
		}
	}
	if err := c.validateHMACScheme(); err != nil {
		return err
	}
	return c.validateHMACHeaders()
}

// validateHMACScheme checks the algorithm, encoding, signed bytes and format.
func (c Config) validateHMACScheme() error {
	h := c.HMAC
	switch {
	case !hmacsig.ValidAlgorithm(h.Algorithm):
		return c.errf("invalid %s %q (want sha256, sha1 or sha512)", cfgKeyHMACAlgorithm, h.Algorithm)
	case !hmacsig.ValidEncoding(h.Encoding):
		return c.errf("invalid %s %q (want hex or base64)", cfgKeyHMACEncoding, h.Encoding)
	case !hmacsig.ValidSigned(h.Signed):
		return c.errf("invalid %s %q (want body, timestamp.body or id.timestamp.body)", cfgKeyHMACSigned, h.Signed)
	case !hmacsig.ValidFormat(h.HeaderFormat):
		return c.errf("invalid %s %q (want empty or stripe)", cfgKeyHMACHeaderFormat, h.HeaderFormat)
	case h.TimestampUnit != HMACTimestampSeconds && h.TimestampUnit != HMACTimestampMilliseconds:
		return c.errf("invalid %s %q (want seconds or milliseconds)", cfgKeyHMACTimestampUnit, h.TimestampUnit)
	case strings.ContainsAny(h.Prefix, "\r\n\x00"):
		return c.errf("%s contains CR/LF/NUL", cfgKeyHMACPrefix)
	}
	return nil
}

// validateHMACHeaders checks the headers the signature, the timestamp and
// the id are written to.
func (c Config) validateHMACHeaders() error {
	h := c.HMAC
	for key, name := range map[string]string{
		cfgKeyHMACSignatureHeader: h.SignatureHeader,
		cfgKeyHMACTimestampHeader: h.TimestampHeader,
		cfgKeyHMACIDHeader:        h.IDHeader,
	} {
		if name != "" && !isValidHeaderName(name) {
			return c.errf("%s %q is not a valid header name", key, name)
		}
	}
	if h.HeaderFormat == hmacsig.FormatStripe {
		if h.TimestampHeader != "" {
			return c.errf("%s must be empty when %s is stripe: the timestamp is written into the signature header",
				cfgKeyHMACTimestampHeader, cfgKeyHMACHeaderFormat)
		}
		return nil
	}
	if h.scheme().UsesTimestamp() && h.TimestampHeader == "" {
		return c.errf("%s is required when %s is %q", cfgKeyHMACTimestampHeader, cfgKeyHMACSigned, h.Signed)
	}
	if h.scheme().UsesID() && h.IDHeader == "" {
		return c.errf("%s is required when %s is %q", cfgKeyHMACIDHeader, cfgKeyHMACSigned, h.Signed)
	}
	return nil
}

// hmacAuth signs each request as it is sent.
type hmacAuth struct {
	cfg    Config
	secret string
	now    func() time.Time
	newID  func() (string, error)
}

func newHMACAuth(c Config, now func() time.Time) (hmacAuth, error) {
	if c.Credential == "" {
		return hmacAuth{}, c.err("hmac signing secret is empty")
	}
	return hmacAuth{cfg: c, secret: c.Credential, now: now, newID: newDeliveryID}, nil
}

// newDeliveryID is a delivery id for a request whose caller set none.
func newDeliveryID() (string, error) {
	b := make([]byte, deliveryIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err //nolint:wrapcheck // Apply wraps it in the kind's voice
	}
	return "msg_" + hex.EncodeToString(b), nil
}

// Apply signs the exact bytes the request will send. The timestamp is taken
// now, when the request is built to be sent, so a request built again for a
// retry is signed afresh rather than refused as stale.
func (a hmacAuth) Apply(req *http.Request) error {
	h := a.cfg.HMAC
	scheme := h.scheme()
	body, err := requestBody(req)
	if err != nil {
		return a.cfg.errf("hmac: reading the request body to sign: %w", err)
	}
	if h.Signed == hmacsig.SignedBody && len(body) == 0 &&
		(req.Method == http.MethodGet || req.Method == http.MethodHead) {
		return a.cfg.errf("hmac: a %s with no body has nothing to sign when %s is body; "+
			"send a body, or sign a timestamp with %s timestamp.body", req.Method, cfgKeyHMACSigned, cfgKeyHMACSigned)
	}
	var ts string
	if scheme.UsesTimestamp() {
		ts = a.timestamp()
		if h.TimestampHeader != "" {
			req.Header.Set(h.TimestampHeader, ts)
		}
	}
	id, err := a.deliveryID(req)
	if err != nil {
		return err
	}
	req.Header.Set(h.SignatureHeader, scheme.Header(a.secret, id, ts, body))
	return nil
}

// deliveryID is the request's delivery id, written to the id header: the
// caller's when the call set that header, a generated one otherwise, and
// empty for a connection that names no id header.
func (a hmacAuth) deliveryID(req *http.Request) (string, error) {
	header := a.cfg.HMAC.IDHeader
	if header == "" {
		return "", nil
	}
	id := strings.TrimSpace(req.Header.Get(header))
	if id == "" {
		var err error
		if id, err = a.newID(); err != nil {
			return "", a.cfg.errf("hmac: generating a delivery id: %w", err)
		}
	}
	req.Header.Set(header, id)
	return id, nil
}

// timestamp is the current Unix time in the connection's unit.
func (a hmacAuth) timestamp() string {
	t := a.now()
	if a.cfg.HMAC.TimestampUnit == HMACTimestampMilliseconds {
		return strconv.FormatInt(t.UnixMilli(), decimal)
	}
	return strconv.FormatInt(t.Unix(), decimal)
}

// requestBody returns the bytes the request will send, leaving the request
// able to send them. A request built with a bytes body carries GetBody, which
// is read without touching Body; any other body is read and replaced.
func requestBody(req *http.Request) ([]byte, error) {
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			return nil, err //nolint:wrapcheck // Apply wraps it in the kind's voice
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(rc) //nolint:wrapcheck // Apply wraps it in the kind's voice
	}
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err //nolint:wrapcheck // Apply wraps it in the kind's voice
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return body, nil
}
