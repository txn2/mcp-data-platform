package whauth

import (
	"bytes"
	"net/http"
	"strconv"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/hmacsig"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// benchSecret is the secret every benchmarked source verifies with.
const benchSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"

// benchSizes are the body sizes verification is measured at (#2223).
var benchSizes = []struct {
	name string
	n    int
}{{"1KiB", 1 << 10}, {"1MiB", 1 << 20}}

// presetSource is an hmac source configured as the named hmacsig preset.
func presetSource(name string) whsource.Source {
	p, _ := hmacsig.LookupPreset(name)
	return whsource.Source{Auth: whsource.Auth{
		Mode: whsource.AuthHMAC, Secret: benchSecret,
		Algorithm: p.Algorithm, Encoding: p.Encoding, Prefix: p.Prefix, Signed: p.Signed, HeaderFormat: p.Format,
		SignatureHeader: p.SignatureHeader, TimestampHeader: p.TimestampHeader, IDHeader: p.IDHeader,
	}}.WithDefaults()
}

// signedRequest is a request the source's scheme signed, at now.
func signedRequest(src whsource.Source, body []byte) Request {
	a := src.Auth
	ts := strconv.FormatInt(now.Unix(), 10)
	h := http.Header{}
	h.Set(a.SignatureHeader, a.Scheme().Header(a.Secret, "msg_1", ts, body))
	if a.TimestampHeader != "" {
		h.Set(a.TimestampHeader, ts)
	}
	if a.IDHeader != "" {
		h.Set(a.IDHeader, "msg_1")
	}
	return Request{Header: h, Body: body}
}

// BenchmarkVerify measures one request's verification for each preset and
// for a hand-configured body scheme, at each body size.
func BenchmarkVerify(b *testing.B) {
	sources := map[string]whsource.Source{
		hmacsig.PresetStandardWebhooks: presetSource(hmacsig.PresetStandardWebhooks),
		hmacsig.PresetGitHub:           presetSource(hmacsig.PresetGitHub),
		hmacsig.PresetStripe:           presetSource(hmacsig.PresetStripe),
		hmacsig.PresetPlatform:         presetSource(hmacsig.PresetPlatform),
		"body": whsource.Source{Auth: whsource.Auth{
			Mode: whsource.AuthHMAC, Secret: benchSecret, SignatureHeader: "X-Sig",
		}}.WithDefaults(),
	}
	for _, name := range []string{
		hmacsig.PresetStandardWebhooks, hmacsig.PresetGitHub, hmacsig.PresetStripe, hmacsig.PresetPlatform, "body",
	} {
		for _, size := range benchSizes {
			src := sources[name]
			req := signedRequest(src, bytes.Repeat([]byte("a"), size.n))
			verify := verifierFor(b, src)
			b.Run(name+"/"+size.name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size.n))
				for b.Loop() {
					if err := verify(req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkVerifyPattern measures a templated scheme read through a header
// pattern, the Verkada configuration #2223 names, beside the hardcoded
// timestamp.body scheme at the same sizes.
func BenchmarkVerifyPattern(b *testing.B) {
	for _, size := range benchSizes {
		src := verkadaSource()
		req := signedRequest(src, bytes.Repeat([]byte("a"), size.n))
		verify := verifierFor(b, src)
		b.Run("verkada/"+size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size.n))
			for b.Loop() {
				if err := verify(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// verkadaSource is the configuration #2223 gives for Verkada: the header is
// "<timestamp>|<signature>" and the signature covers "<body>|<timestamp>".
func verkadaSource() whsource.Source {
	return whsource.Source{Auth: whsource.Auth{
		Mode: whsource.AuthHMAC, Secret: benchSecret, Algorithm: whsource.AlgorithmSHA256, Encoding: whsource.EncodingHex,
		SignatureHeader: "Verkada-Signature", HeaderPattern: "{timestamp}|{signature}", Signed: "{body}|{timestamp}",
	}}.WithDefaults()
}

// verifierFor is how the receiver verifies a request for src: compiled once,
// when the source is loaded.
func verifierFor(b *testing.B, src whsource.Source) func(Request) error {
	b.Helper()
	v, err := Compile(src)
	if err != nil {
		b.Fatal(err)
	}
	return func(req Request) error { return v.Verify(req, now) }
}
