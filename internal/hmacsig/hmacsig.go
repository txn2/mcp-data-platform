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

// ErrMalformed is returned for a signature header that cannot be read as the
// scheme writes one.
var ErrMalformed = errors.New("hmacsig: the signature header is malformed")

// Scheme is how one sender signs.
type Scheme struct {
	Algorithm string
	Encoding  string
	Prefix    string
	Signed    string
	Format    string
}

// Signature is what a signature header carries: the signatures, decoded, and,
// for a format that writes it there, the timestamp.
type Signature struct {
	MACs      [][]byte
	Timestamp string
}

// UsesTimestamp reports whether the signed bytes include a timestamp.
func (s Scheme) UsesTimestamp() bool {
	return s.Format == FormatStripe || s.Signed == SignedTimestampBody || s.Signed == SignedIDTimestampBody
}

// UsesID reports whether the signed bytes include a delivery id.
func (s Scheme) UsesID() bool {
	return s.Format != FormatStripe && s.Signed == SignedIDTimestampBody
}

// Payload is the bytes the scheme signs for one request.
func (s Scheme) Payload(id, timestamp string, body []byte) []byte {
	var head string
	switch {
	case s.Format == FormatStripe, s.Signed == SignedTimestampBody:
		head = timestamp + "."
	case s.Signed == SignedIDTimestampBody:
		head = id + "." + timestamp + "."
	default:
		return body
	}
	return append([]byte(head), body...)
}

// MAC computes the signature of the payload under secret.
func (s Scheme) MAC(secret, id, timestamp string, body []byte) []byte {
	mac := hmac.New(s.hash(), s.key(secret))
	_, _ = mac.Write(s.Payload(id, timestamp, body))
	return mac.Sum(nil)
}

// Header is the signature header's value for one request.
func (s Scheme) Header(secret, id, timestamp string, body []byte) string {
	sig := s.encode(s.MAC(secret, id, timestamp, body))
	if s.Format == FormatStripe {
		return "t=" + timestamp + ",v1=" + sig
	}
	return s.Prefix + sig
}

// Parse reads a signature header's value. A value none of whose signatures
// can be read is ErrMalformed.
func (s Scheme) Parse(value string) (Signature, error) {
	value = strings.TrimSpace(value)
	if s.Format == FormatStripe {
		return s.parseStripe(value)
	}
	var out Signature
	for field := range strings.FieldsSeq(value) {
		trimmed, ok := strings.CutPrefix(field, s.Prefix)
		if !ok {
			continue
		}
		if mac, err := s.decode(trimmed); err == nil {
			out.MACs = append(out.MACs, mac)
		}
	}
	if len(out.MACs) == 0 {
		return Signature{}, ErrMalformed
	}
	return out, nil
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
func (s Scheme) Matches(sig Signature, secrets []string, id, timestamp string, body []byte) bool {
	for _, secret := range secrets {
		want := s.MAC(secret, id, timestamp, body)
		for _, got := range sig.MACs {
			if hmac.Equal(want, got) {
				return true
			}
		}
	}
	return false
}

// key is the HMAC key a secret stands for. A Standard Webhooks secret,
// "whsec_" and base64, is the bytes the base64 decodes to; the prefix is
// read that way only for the id.timestamp.body form that standard defines, so
// a secret of any other scheme that happens to begin "whsec_" is used as
// written.
func (s Scheme) key(secret string) []byte {
	if s.Signed == SignedIDTimestampBody {
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

// ValidSigned reports whether v names what a scheme signs.
func ValidSigned(v string) bool {
	return v == SignedBody || v == SignedTimestampBody || v == SignedIDTimestampBody
}

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
)

// presets are the conventions LookupPreset knows.
var presets = map[string]Preset{
	PresetStandardWebhooks: {
		Scheme:          Scheme{Algorithm: AlgorithmSHA256, Encoding: EncodingBase64, Prefix: "v1,", Signed: SignedIDTimestampBody},
		SignatureHeader: "webhook-signature", TimestampHeader: "webhook-timestamp", IDHeader: "webhook-id",
	},
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
}

// LookupPreset returns the named convention.
func LookupPreset(name string) (Preset, bool) {
	p, ok := presets[name]
	return p, ok
}
