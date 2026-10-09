package totp

import (
	"encoding/base32"
	"errors"
	"strings"
	"testing"
	"time"
)

// The RFC 6238 Appendix B seeds: the ASCII "12345678901234567890" repeated
// to the hash's block-relevant length for each algorithm.
var (
	seed1   = b32("12345678901234567890")
	seed256 = b32("12345678901234567890123456789012")
	seed512 = b32("1234567890123456789012345678901234567890123456789012345678901234")
)

func b32(s string) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(s))
}

// TestRFC6238Vectors is Appendix B of RFC 6238: eight digits, thirty-second
// periods, each algorithm with its own seed.
func TestRFC6238Vectors(t *testing.T) {
	cases := []struct {
		unix int64
		sha1 string
		s256 string
		s512 string
	}{
		{59, "94287082", "46119246", "90693936"},
		{1111111109, "07081804", "68084774", "25091201"},
		{1111111111, "14050471", "67062674", "99943326"},
		{1234567890, "89005924", "91819424", "93441116"},
		{2000000000, "69279037", "90698825", "38618901"},
		{20000000000, "65353130", "77737706", "47863826"},
	}
	for _, c := range cases {
		for _, v := range []struct {
			seed, alg, want string
		}{{seed1, AlgorithmSHA1, c.sha1}, {seed256, AlgorithmSHA256, c.s256}, {seed512, AlgorithmSHA512, c.s512}} {
			p := Params{Algorithm: v.alg, Digits: 8, Period: 30}
			got, err := Code(v.seed, p, p.Counter(time.Unix(c.unix, 0)))
			if err != nil {
				t.Fatal(err)
			}
			if got != v.want {
				t.Errorf("%s at %d = %s, want %s", v.alg, c.unix, got, v.want)
			}
		}
	}
}

func TestSixDigitsIsTheLowSixOfTheSameNumber(t *testing.T) {
	p := Defaults()
	got, err := Code(seed1, p, p.Counter(time.Unix(59, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if got != "287082" {
		t.Errorf("code = %s", got)
	}
}

func TestParse(t *testing.T) {
	seed, p, err := Parse("otpauth://totp/Vendor:ops@example.com?secret=" + strings.ToLower(seed1) + "&issuer=Vendor&algorithm=sha256&digits=8&period=60")
	if err != nil {
		t.Fatal(err)
	}
	if seed != seed1 || p != (Params{Algorithm: AlgorithmSHA256, Digits: 8, Period: 60}) {
		t.Errorf("Parse = %s %+v", seed, p)
	}
	// A bare seed, spaced the way providers print one, takes the defaults.
	spaced := strings.ToLower(seed1[:4] + " " + seed1[4:8] + " " + seed1[8:])
	seed, p, err = Parse(spaced)
	if err != nil {
		t.Fatal(err)
	}
	if seed != seed1 || p != Defaults() {
		t.Errorf("Parse(bare) = %s %+v", seed, p)
	}
	if p.Start(p.Counter(time.Unix(65, 0))) != time.Unix(60, 0) {
		t.Error("Start is not the period's first second")
	}
}

func TestParseRefusals(t *testing.T) {
	for input, want := range map[string]string{
		"otpauth://hotp/V:x?secret=" + seed1 + "&counter=1": "hotp",
		"otpauth://other/V?secret=" + seed1:                 `type is "other"`,
		"not!base32":                                        "not base32",
		"":                                                  "empty",
		"MFRGG":                                             "shorter than",
		"otpauth://totp/V?secret=" + seed1 + "&digits=7":      "digits is 7",
		"otpauth://totp/V?secret=" + seed1 + "&digits=x":      "not a number",
		"otpauth://totp/V?secret=" + seed1 + "&period=0":      "period is 0",
		"otpauth://totp/V?secret=" + seed1 + "&period=x":      "not a number",
		"otpauth://totp/V?secret=" + seed1 + "&algorithm=MD5": `algorithm "MD5"`,
		"otpauth://to%zz/V": "does not parse",
	} {
		_, _, err := Parse(input)
		if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", input, err, want)
		}
	}
	if _, err := Code("!!", Defaults(), 1); err == nil {
		t.Error("a stored seed that is not base32 computed a code")
	}
	if _, err := Code(seed1, Params{Algorithm: "MD5", Digits: 6, Period: 30}, 1); err == nil {
		t.Error("an invalid algorithm computed a code")
	}
}
