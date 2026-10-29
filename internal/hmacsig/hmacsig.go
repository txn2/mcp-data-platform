// Package hmacsig is the one definition of a webhook HMAC signature: which
// bytes are signed, how the signature is written into its header, and how a
// header is read back into the signatures it carries (#1996).
//
// The platform verifies these signatures on its inbound webhook sources
// (internal/webhook/whauth) and writes them on an outbound connection whose
// auth_mode is hmac (internal/upstreamauth). Both build on this package, so a
// connection and a source configured with the same values agree by
// construction rather than by two implementations being kept in step.
//
// It knows nothing about requests, sources or connections: a caller hands it
// a Scheme, the secret, the delivery id and timestamp it read or chose, and
// the body.
package hmacsig

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- sha1 is an HMAC a sender chooses, not a digest relied on for collision resistance
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"strings"
)

// Algorithms a scheme signs with.
const (
	AlgorithmSHA256 = "sha256"
	AlgorithmSHA1   = "sha1"
	AlgorithmSHA512 = "sha512"
)

// Encodings a signature is written in.
const (
	EncodingHex    = "hex"
	EncodingBase64 = "base64"
)

// What a scheme signs.
const (
	// SignedBody signs the raw body.
	SignedBody = "body"
	// SignedTimestampBody signs the timestamp, a ".", and the raw body,
	// which is what makes a replayed request with an old timestamp fail
	// even though its signature once verified.
	SignedTimestampBody = "timestamp.body"
	// SignedIDTimestampBody signs the delivery id, a ".", the timestamp, a
	// ".", and the raw body: the Standard Webhooks form.
	SignedIDTimestampBody = "id.timestamp.body"
)

// How the signature header is written.
const (
	// FormatPlain writes the prefix and the signature. A header may carry
	// several, separated by spaces, which is how a sender rotating its
	// secret signs with both (Standard Webhooks' "v1,<a> v1,<b>").
	FormatPlain = ""
	// FormatStripe writes "t=<timestamp>,v1=<signature>": the timestamp is
	// in the signature header rather than a header of its own, and the
	// signed bytes are always the timestamp, a ".", and the body.
	FormatStripe = "stripe"
)

// standardWebhooksSecretPrefix marks a Standard Webhooks secret, whose key is
// the base64 that follows it.
const standardWebhooksSecretPrefix = "whsec_"

// How a secret is read into the HMAC key.
const (
	// SecretRaw uses the secret's text as the key.
	SecretRaw = ""
	// SecretBase64 uses the bytes the secret's base64 decodes to, as a
	// Microsoft Teams outgoing webhook's security token is used.
	SecretBase64 = "base64"
)

// ErrMalformed is returned for a signature header that cannot be read as the
// scheme writes one.
var ErrMalformed = errors.New("hmacsig: the signature header is malformed")

// Scheme is how one sender signs.
type Scheme struct {
	Algorithm string
	Encoding  string
	Prefix    string
	// Signed is a named form (body, timestamp.body, id.timestamp.body) or a
	// template over {id}, {timestamp} and {body} (#2223).
	Signed string
	Format string
	// HeaderPattern lays the signature header out over {signature} and
	// {timestamp}, in place of a prefix or a format (#2223).
	HeaderPattern string
	// SecretEncoding is how the secret is read: raw text, or base64.
	SecretEncoding string
}

// Signature is what a signature header carries: the signatures, decoded, and,
// for a format that writes it there, the timestamp.
type Signature struct {
	MACs      [][]byte
	Timestamp string
}

// Compiled is a scheme with its template and header pattern compiled, which
// is what a source verifies every request with: nothing about the scheme is
// parsed per request.
type Compiled struct {
	scheme     Scheme
	tmpl       Template
	pattern    Pattern
	hasPattern bool
	newHash    func() hash.Hash
}

// Compile compiles the scheme. A Stripe-format scheme always signs the
// timestamp, a ".", and the body.
func (s Scheme) Compile() (Compiled, error) {
	signed := s.Signed
	if s.Format == FormatStripe {
		signed = SignedTimestampBody
	}
	if signed == "" {
		signed = SignedBody
	}
	tmpl, err := CompileTemplate(signed)
	if err != nil {
		return Compiled{}, err
	}
	c := Compiled{scheme: s, tmpl: tmpl, newHash: s.hash()}
	if s.HeaderPattern != "" {
		if c.pattern, err = CompilePattern(s.HeaderPattern); err != nil {
			return Compiled{}, err
		}
		c.hasPattern = true
	}
	return c, nil
}

// compiled compiles a scheme its caller has already validated. A scheme that
// does not compile signs the body alone, which no receiver of the scheme it
// was meant to be would accept.
func (s Scheme) compiled() Compiled {
	c, err := s.Compile()
	if err != nil {
		c, _ = Scheme{Algorithm: s.Algorithm, Encoding: s.Encoding, Prefix: s.Prefix}.Compile()
	}
	return c
}

// UsesTimestamp reports whether the signed bytes include a timestamp.
func (s Scheme) UsesTimestamp() bool { return s.compiled().UsesTimestamp() }

// UsesID reports whether the signed bytes include a delivery id.
func (s Scheme) UsesID() bool { return s.compiled().UsesID() }

// Payload is the bytes the scheme signs for one request.
func (s Scheme) Payload(id, timestamp string, body []byte) []byte {
	return s.compiled().tmpl.Bytes(id, timestamp, body)
}

// MAC computes the signature of the payload under secret.
func (s Scheme) MAC(secret, id, timestamp string, body []byte) []byte {
	return s.compiled().MAC(secret, id, timestamp, body)
}

// Header is the signature header's value for one request.
func (s Scheme) Header(secret, id, timestamp string, body []byte) string {
	return s.compiled().Header(secret, id, timestamp, body)
}

// Parse reads a signature header's value. A value none of whose signatures
// can be read is ErrMalformed.
func (s Scheme) Parse(value string) (Signature, error) { return s.compiled().Parse(value) }

// Matches reports whether any of the signatures is the scheme's signature of
// the payload under any of the secrets. Every comparison is constant-time.
func (s Scheme) Matches(sig Signature, secrets []string, id, timestamp string, body []byte) bool {
	return s.compiled().Matches(sig, secrets, id, timestamp, body)
}

// Template is the signed bytes' template.
func (c Compiled) Template() Template { return c.tmpl }

// UsesTimestamp reports whether the signed bytes include a timestamp.
func (c Compiled) UsesTimestamp() bool { return c.tmpl.UsesTimestamp() }

// UsesID reports whether the signed bytes include a delivery id.
func (c Compiled) UsesID() bool { return c.tmpl.UsesID() }

// TimestampInHeader reports whether the signature header carries the
// timestamp: the Stripe format, or a pattern naming {timestamp}.
func (c Compiled) TimestampInHeader() bool {
	return c.scheme.Format == FormatStripe || (c.hasPattern && c.pattern.UsesTimestamp())
}

// MAC computes the signature of the payload under secret, writing the signed
// bytes into the HMAC segment by segment.
func (c Compiled) MAC(secret, id, timestamp string, body []byte) []byte {
	mac := hmac.New(c.newHash, c.key(secret))
	c.tmpl.Hash(mac, id, timestamp, body)
	return mac.Sum(nil)
}

// Header is the signature header's value for one request.
func (c Compiled) Header(secret, id, timestamp string, body []byte) string {
	sig := c.scheme.encode(c.MAC(secret, id, timestamp, body))
	switch {
	case c.hasPattern:
		return c.pattern.Write(sig, timestamp)
	case c.scheme.Format == FormatStripe:
		return "t=" + timestamp + ",v1=" + sig
	}
	return c.scheme.Prefix + sig
}

// Parse reads a signature header's value. A value none of whose signatures
// can be read is ErrMalformed.
func (c Compiled) Parse(value string) (Signature, error) {
	value = strings.TrimSpace(value)
	switch {
	case c.hasPattern:
		return c.parsePattern(value)
	case c.scheme.Format == FormatStripe:
		return c.scheme.parseStripe(value)
	}
	var out Signature
	for field := range strings.FieldsSeq(value) {
		trimmed, ok := strings.CutPrefix(field, c.scheme.Prefix)
		if !ok {
			continue
		}
		if mac, err := c.scheme.decode(trimmed); err == nil {
			out.MACs = append(out.MACs, mac)
		}
	}
	if len(out.MACs) == 0 {
		return Signature{}, ErrMalformed
	}
	return out, nil
}

// parsePattern reads a value laid out by the scheme's header pattern.
func (c Compiled) parsePattern(value string) (Signature, error) {
	sig, ts, ok := c.pattern.Read(value)
	if !ok {
		return Signature{}, ErrMalformed
	}
	mac, err := c.scheme.decode(sig)
	if err != nil {
		return Signature{}, ErrMalformed
	}
	return Signature{MACs: [][]byte{mac}, Timestamp: ts}, nil
}

// parseStripe reads "t=<timestamp>,v1=<sig>[,v1=<sig>...]". Entries of any
// other scheme (v0) are skipped.
func (s Scheme) parseStripe(value string) (Signature, error) {
	var out Signature
	for part := range strings.SplitSeq(value, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			out.Timestamp = v
		case "v1":
			if mac, err := s.decode(v); err == nil {
				out.MACs = append(out.MACs, mac)
			}
		}
	}
	if out.Timestamp == "" || len(out.MACs) == 0 {
		return Signature{}, ErrMalformed
	}
	return out, nil
}

// Matches reports whether any of the signatures is the scheme's signature of
// the payload under any of the secrets. Every comparison is constant-time.
func (c Compiled) Matches(sig Signature, secrets []string, id, timestamp string, body []byte) bool {
	for _, secret := range secrets {
		want := c.MAC(secret, id, timestamp, body)
		for _, got := range sig.MACs {
			if hmac.Equal(want, got) {
				return true
			}
		}
	}
	return false
}

// key is the HMAC key a secret stands for. A base64 secret encoding reads the
// key as the bytes the secret decodes to. A Standard Webhooks secret,
// "whsec_" and base64, is the bytes the base64 decodes to; the prefix is read
// that way only for the id.timestamp.body form that standard defines, so a
// secret of any other scheme that happens to begin "whsec_" is used as
// written.
func (c Compiled) key(secret string) []byte {
	if c.scheme.SecretEncoding == SecretBase64 {
		if b, err := base64.StdEncoding.DecodeString(secret); err == nil {
			return b
		}
		return []byte(secret)
	}
	if c.tmpl.text == templateIDTimestampBody {
		if rest, ok := strings.CutPrefix(secret, standardWebhooksSecretPrefix); ok {
			if b, err := base64.StdEncoding.DecodeString(rest); err == nil {
				return b
			}
		}
	}
	return []byte(secret)
}

// hash returns the hash the scheme's algorithm names.
func (s Scheme) hash() func() hash.Hash {
	switch s.Algorithm {
	case AlgorithmSHA1:
		return sha1.New
	case AlgorithmSHA512:
		return sha512.New
	default:
		return sha256.New
	}
}

// encode writes a signature in the scheme's encoding.
func (s Scheme) encode(mac []byte) string {
	if s.Encoding == EncodingBase64 {
		return base64.StdEncoding.EncodeToString(mac)
	}
	return hex.EncodeToString(mac)
}

// decode reads a signature in the scheme's encoding. Base64 is read in the
// standard and the URL alphabets, since senders use both.
func (s Scheme) decode(v string) ([]byte, error) {
	if s.Encoding == EncodingBase64 {
		if b, err := base64.StdEncoding.DecodeString(v); err == nil {
			return b, nil
		}
		b, err := base64.URLEncoding.DecodeString(v)
		if err != nil {
			return nil, ErrMalformed
		}
		return b, nil
	}
	b, err := hex.DecodeString(strings.ToLower(v))
	if err != nil {
		return nil, ErrMalformed
	}
	return b, nil
}

// ValidAlgorithm reports whether a is an algorithm a scheme signs with.
func ValidAlgorithm(a string) bool {
	return a == AlgorithmSHA256 || a == AlgorithmSHA1 || a == AlgorithmSHA512
}

// ValidEncoding reports whether e is an encoding a signature is written in.
func ValidEncoding(e string) bool { return e == EncodingHex || e == EncodingBase64 }

// ValidSigned reports whether v names what a scheme signs: a named form or a
// template that compiles.
func ValidSigned(v string) bool {
	_, err := CompileTemplate(v)
	return err == nil
}

// ValidSecretEncoding reports whether e names how a secret is read.
func ValidSecretEncoding(e string) bool { return e == SecretRaw || e == SecretBase64 }

// ValidFormat reports whether f names a signature header format.
func ValidFormat(f string) bool { return f == FormatPlain || f == FormatStripe }

// Preset names a sender's whole signing convention in one setting.
type Preset struct {
	Scheme
	SignatureHeader string
	TimestampHeader string
	IDHeader        string
}

// Presets by name.
const (
	PresetStandardWebhooks = "standard_webhooks"
	PresetGitHub           = "github"
	PresetStripe           = "stripe"
	// PresetPlatform is what the platform's own inbound webhook
	// documentation configures a source with, so a deployment's script can
	// deliver to another deployment's source with one setting.
	PresetPlatform = "platform"

	// The senders #2223 added whose proof is an HMAC.
	PresetSlack   = "slack"
	PresetZoom    = "zoom"
	PresetTeams   = "teams"
	PresetShopify = "shopify"
	PresetLinear  = "linear"
	PresetGitLab  = "gitlab"
	PresetJira    = "jira"
)

// slackStyle is the convention Slack and Zoom share: "v0=" and the hex
// HMAC-SHA256 of "v0:{timestamp}:{body}".
var slackStyle = Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingHex, Prefix: "v0=", Signed: "v0:{timestamp}:{body}"}

// standardWebhooks is the Standard Webhooks convention, which GitLab's
// signing token follows.
var standardWebhooks = Preset{
	Algorithm: AlgorithmSHA256, Encoding: EncodingBase64, Prefix: "v1,", Signed: SignedIDTimestampBody,
	SignatureHeader: "webhook-signature", TimestampHeader: "webhook-timestamp", IDHeader: "webhook-id",
}

// presets are the conventions LookupPreset knows.
var presets = map[string]Preset{
	PresetStandardWebhooks: standardWebhooks,
	PresetGitHub: {
		Scheme:          Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingHex, Prefix: "sha256=", Signed: SignedBody},
		SignatureHeader: "X-Hub-Signature-256",
	},
	PresetStripe: {
		Scheme:          Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingHex, Signed: SignedTimestampBody, Format: FormatStripe},
		SignatureHeader: "Stripe-Signature",
	},
	PresetPlatform: {
		Scheme:          Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingHex, Prefix: "sha256=", Signed: SignedTimestampBody},
		SignatureHeader: "X-Signature", TimestampHeader: "X-Timestamp",
	},
	PresetSlack: {Scheme: slackStyle, SignatureHeader: "X-Slack-Signature", TimestampHeader: "X-Slack-Request-Timestamp"},
	PresetZoom:  {Scheme: slackStyle, SignatureHeader: "x-zm-signature", TimestampHeader: "x-zm-request-timestamp"},
	// Teams signs the body with the base64-decoded security token and
	// writes "HMAC <base64>" into Authorization.
	PresetTeams: {
		Scheme: Scheme{
			Algorithm: AlgorithmSHA256, Encoding: EncodingBase64, Signed: SignedBody,
			HeaderPattern: "HMAC {signature}", SecretEncoding: SecretBase64,
		},
		SignatureHeader: "Authorization",
	},
	PresetShopify: {
		Scheme:          Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingBase64, Signed: SignedBody},
		SignatureHeader: "X-Shopify-Hmac-SHA256",
	},
	PresetLinear: {
		Scheme:          Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingHex, Signed: SignedBody},
		SignatureHeader: "Linear-Signature",
	},
	PresetGitLab: standardWebhooks,
	PresetJira: {
		Scheme:          Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingHex, Prefix: "sha256=", Signed: SignedBody},
		SignatureHeader: "X-Hub-Signature",
	},
}

// PresetNames lists the presets LookupPreset knows, in a fixed order.
func PresetNames() []string {
	return []string{
		PresetStandardWebhooks, PresetGitHub, PresetStripe, PresetPlatform,
		PresetSlack, PresetZoom, PresetTeams, PresetShopify, PresetLinear, PresetGitLab, PresetJira,
	}
}

// LookupPreset returns the named convention.
func LookupPreset(name string) (Preset, bool) {
	p, ok := presets[name]
	return p, ok
}
