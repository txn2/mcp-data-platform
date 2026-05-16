package catalog

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id  string
		ok  bool
		why string
	}{
		{"salesforce-rest-2024-10", true, "typical slug"},
		{"a", true, "minimum length"},
		{"0", true, "starts with digit"},
		{"a1b2-c3", true, "mixed alphanumeric with hyphen"},
		{strings.Repeat("a", 100), true, "max length 100"},
		{"", false, "empty"},
		{"-leading-hyphen", false, "leading hyphen"},
		{"trailing-hyphen-", false, "trailing hyphen"},
		{"UPPER", false, "uppercase"},
		{"has space", false, "contains space"},
		{"has.dot", false, "contains dot"},
		{"has_underscore", false, "underscore not allowed in id"},
		{strings.Repeat("a", 101), false, "exceeds max length"},
	}
	for _, c := range cases {
		t.Run(c.why, func(t *testing.T) {
			err := ValidateID(c.id)
			gotOK := err == nil
			if gotOK != c.ok {
				t.Fatalf("ValidateID(%q) ok=%v want %v (err=%v)", c.id, gotOK, c.ok, err)
			}
			if !c.ok && !errors.Is(err, ErrInvalidID) {
				t.Fatalf("ValidateID(%q) err=%v want ErrInvalidID", c.id, err)
			}
		})
	}
}

func TestValidateSpecName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ok   bool
	}{
		{"constituent", true},
		{"gift_v2", true},
		{"a-b-c", true},
		{"a", true},
		{"0", true},
		{"", false},
		{"-bad", false},
		{"bad-", false},
		{"UPPER", false},
		{"with space", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSpecName(c.name)
			gotOK := err == nil
			if gotOK != c.ok {
				t.Fatalf("ValidateSpecName(%q) ok=%v want %v", c.name, gotOK, c.ok)
			}
			if !c.ok && !errors.Is(err, ErrInvalidSpecName) {
				t.Fatalf("ValidateSpecName(%q) err=%v want ErrInvalidSpecName", c.name, err)
			}
		})
	}
}

func TestValidateSourceKind(t *testing.T) {
	t.Parallel()
	if err := ValidateSourceKind(SourceInline); err != nil {
		t.Fatalf("inline rejected: %v", err)
	}
	if err := ValidateSourceKind(SourceUpload); err != nil {
		t.Fatalf("upload rejected: %v", err)
	}
	if err := ValidateSourceKind(SourceURL); err != nil {
		t.Fatalf("url rejected: %v", err)
	}
	if err := ValidateSourceKind("bogus"); err == nil {
		t.Fatal("bogus accepted")
	}
	if err := ValidateSourceKind(""); err == nil {
		t.Fatal("empty accepted")
	}
}

func TestNormalizeBasePath(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty stays empty", "", "", false},
		{"valid leading slash", "/v1", "/v1", false},
		{"nested valid", "/api/v2", "/api/v2", false},
		{"trailing slash stripped", "/v1/", "/v1", false},
		{"root preserved", "/", "/", false},
		{"missing leading slash", "v1", "", true},
		{"CR injected", "/v1\r/admin", "", true},
		{"LF injected", "/v1\n/admin", "", true},
		{"NUL injected", "/v1\x00/admin", "", true},
		{"query string component", "/v1?x=1", "", true},
		{"fragment component", "/v1#anchor", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeBasePath(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}
