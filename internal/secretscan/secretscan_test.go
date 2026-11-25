package secretscan

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

const planted = "s3cr3t-Value_with/odd+chars&="

func known(context.Context) ([]string, error) { return []string{planted, "short"}, nil }

func TestScan_FindsAKnownValueInEveryEncoding(t *testing.T) {
	d := Detector{Known: known}
	js, _ := json.Marshal(planted)
	for name, form := range map[string]string{
		"as written":   planted,
		"base64":       base64.StdEncoding.EncodeToString([]byte(planted)),
		"base64url":    base64.RawURLEncoding.EncodeToString([]byte(planted)),
		"hex":          hex.EncodeToString([]byte(planted)),
		"url-escaped":  url.QueryEscape(planted),
		"json-escaped": strings.Trim(string(js), `"`),
	} {
		got, err := d.Scan(context.Background(), []Field{{Name: "blocked_by", Value: "the call failed with " + form + " in it"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Field != "blocked_by" || got[0].Kind != KindKnown {
			t.Errorf("%s: findings = %+v", name, got)
		}
		for _, f := range got {
			if strings.Contains(f.Field+f.Kind, planted) {
				t.Errorf("%s: a finding repeats the value", name)
			}
		}
	}
	if got, _ := d.Scan(context.Background(), []Field{{Name: "x", Value: "a short word"}}); len(got) != 0 {
		t.Errorf("a short stored value matched prose: %+v", got)
	}
}

func TestScan_Shapes(t *testing.T) {
	d := Detector{}
	for kind, text := range map[string]string{
		KindPrivateKey: "-----BEGIN RSA PRIVATE KEY-----\nMIIE...",
		KindBearer:     "sent Authorization: Bearer abcdefghijklmnopqrstuvwx",
		KindToken:      "token ghp_abcdefghijklmnopqrstuvwxyz0123",
		KindJWT:        "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		KindURLCreds:   "postgres://admin:hunter22@db:5432/x",
		KindAssignment: "set password=hunter22x and retry",
	} {
		got, err := d.Scan(context.Background(), []Field{{Name: "f", Value: text}})
		if err != nil || len(got) != 1 || got[0].Kind != kind {
			t.Errorf("%s: findings = %+v %v", kind, got, err)
		}
	}
	for _, clean := range []string{"", "list the assets shared with me", "the password field is required", "https://example.com/docs"} {
		if got, _ := d.Scan(context.Background(), []Field{{Name: "f", Value: clean}}); len(got) != 0 {
			t.Errorf("%q flagged: %+v", clean, got)
		}
	}
}

func TestScan_KnownErrorRefuses(t *testing.T) {
	d := Detector{Known: func(context.Context) ([]string, error) { return nil, errors.New("store down") }}
	if _, err := d.Scan(context.Background(), []Field{{Name: "f", Value: "x"}}); err == nil {
		t.Error("a failed read of the known values was not reported")
	}
}
