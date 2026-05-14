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
		{"blackbaud-renxt-2024-10", true, "typical slug"},
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
