package totp

import "testing"

// FuzzParse holds the seed reader to an administrator's paste (#2713): a seed
// that parses yields a code of exactly the digits its parameters name, and
// parsing the canonical seed it returns gives the same seed.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"JBSWY3DPEHPK3PXP",
		"otpauth://totp/ACME:jane@example.com?secret=JBSWY3DPEHPK3PXP&issuer=ACME&digits=8&period=60&algorithm=SHA256",
		"jbsw y3dp ehpk 3pxp",
		"otpauth://hotp/x?secret=AAAA", "", "!!!!",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		seed, p, err := Parse(input)
		if err != nil {
			return
		}
		code, err := Code(seed, p, 1)
		if err != nil {
			t.Fatalf("a parsed seed %q gives no code: %v", seed, err)
		}
		if len(code) != p.Digits {
			t.Fatalf("code %q has %d digits, want %d", code, len(code), p.Digits)
		}
		again, _, err := Parse(seed)
		if err != nil || again != seed {
			t.Fatalf("the canonical seed %q parses to %q, %v", seed, again, err)
		}
	})
}
