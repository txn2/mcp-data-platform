package whauth

import "testing"

// FuzzVerifyRefusesWhatWasNotSigned holds inbound webhook authentication to an
// unauthenticated sender's request (#2713): for every preset, a request whose
// signature header is not the one the source's secret signs is never
// accepted, and the one it signs always is.
func FuzzVerifyRefusesWhatWasNotSigned(f *testing.F) {
	f.Add([]byte(`{"id":"evt_1"}`), "v1,abc", 0)
	f.Add([]byte(""), "", 1)
	f.Add([]byte("x"), "t=1,v1=00", 2)
	presets := []string{"standard_webhooks", "github", "stripe", "platform"}
	f.Fuzz(func(t *testing.T, body []byte, header string, which int) {
		if which < 0 {
			which = -which
		}
		src := presetSource(presets[which%len(presets)])
		signed := signedRequest(src, body)
		if err := Verify(src, signed, now); err != nil {
			t.Fatalf("a request the source's own secret signed was refused: %v", err)
		}
		forged := Request{Header: signed.Header.Clone(), Body: body}
		forged.Header.Set(src.Auth.SignatureHeader, header)
		if header == signed.Header.Get(src.Auth.SignatureHeader) {
			return
		}
		if err := Verify(src, forged, now); err == nil {
			t.Fatalf("preset %s accepted signature header %q for a body it signed as %q",
				presets[which%len(presets)], header, signed.Header.Get(src.Auth.SignatureHeader))
		}
	})
}
