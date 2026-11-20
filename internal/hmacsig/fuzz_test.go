package hmacsig

import (
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

// FuzzPatternRoundTrip holds the signature header layout a source configures
// to what senders write (#2713): a value laid out by a pattern is read back to
// the signature and timestamp written, and reading any value never panics.
func FuzzPatternRoundTrip(f *testing.F) {
	for _, s := range []string{"{timestamp}|{signature}", "HMAC {signature}", "sig={signature};", "t={timestamp},v1={signature}"} {
		f.Add(s, []byte{0xde, 0xad, 0xbe, 0xef}, int64(1700000000), "t=1,v1=00")
	}
	f.Fuzz(func(t *testing.T, text string, mac []byte, ts int64, value string) {
		p, err := CompilePattern(text)
		if err != nil {
			return
		}
		_, _, _ = p.Read(value)
		sig, stamp := hex.EncodeToString(mac), strconv.FormatInt(ts, 10)
		if len(mac) == 0 {
			return
		}
		if !p.UsesTimestamp() {
			stamp = ""
		}
		// A literal between the two values cannot be told from a value that
		// holds it; real values (hex or base64 signatures, decimal timestamps)
		// never hold the separators a preset uses.
		for i, seg := range p.segs {
			if seg.kind == segLiteral && i > 0 && i < len(p.segs)-1 &&
				(strings.Contains(sig, seg.text) || strings.Contains(stamp, seg.text)) {
				return
			}
		}
		gotSig, gotTS, ok := p.Read(p.Write(sig, stamp))
		if !ok || gotSig != sig || gotTS != stamp {
			t.Fatalf("pattern %q: Read(Write(%q, %q)) = %q, %q, %t", text, sig, stamp, gotSig, gotTS, ok)
		}
	})
}

// FuzzSchemeParse: every preset reads back the header it writes for a body,
// and the MAC it reads is the one it computed; reading any header never panics.
func FuzzSchemeParse(f *testing.F) {
	f.Add("standard_webhooks", []byte(`{"id":1}`), "v1,abc")
	f.Add("stripe", []byte(""), "t=1,v1=00")
	f.Add("github", []byte("x"), "sha256=zz")
	f.Fuzz(func(t *testing.T, name string, body []byte, header string) {
		p, ok := LookupPreset(name)
		if !ok {
			return
		}
		s := p.Scheme
		if _, err := s.Compile(); err != nil {
			return
		}
		_, _ = s.Parse(header)
		const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
		written := s.Header(secret, "msg_1", "1700000000", body)
		if _, err := s.Parse(written); err != nil {
			t.Fatalf("preset %s does not read the header it wrote, %q: %v", name, written, err)
		}
	})
}
