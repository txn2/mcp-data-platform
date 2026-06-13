package user

import (
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"lowercases and trims", "  Marcus.Johnson@Example.COM ", "marcus.johnson@example.com", false},
		{"already normalized", "a@b.io", "a@b.io", false},
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"no domain", "marcus", "", true},
		{"display name form rejected", "Marcus <marcus@example.com>", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeEmail(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplitFullName(t *testing.T) {
	tests := []struct {
		in          string
		first, last string
	}{
		{"", "", ""},
		{"Marcus", "Marcus", ""},
		{"Marcus Johnson", "Marcus", "Johnson"},
		{"  Mary  Jane  Watson ", "Mary", "Jane Watson"},
	}
	for _, tt := range tests {
		first, last := SplitFullName(tt.in)
		if first != tt.first || last != tt.last {
			t.Errorf("SplitFullName(%q) = (%q,%q), want (%q,%q)", tt.in, first, last, tt.first, tt.last)
		}
	}
}

func TestNameFromClaims(t *testing.T) {
	tests := []struct {
		name        string
		claims      map[string]any
		full        string
		first, last string
	}{
		{"prefers given/family", map[string]any{"given_name": "Marcus", "family_name": "Johnson", "name": "ignore me"}, "", "Marcus", "Johnson"},
		{"given/family over fullName arg", map[string]any{"given_name": "Marcus"}, "Ignore Me", "Marcus", ""},
		{"falls back to name claim", map[string]any{"name": "Dana Lee"}, "", "Dana", "Lee"},
		{"falls back to fullName arg", nil, "Dana Lee", "Dana", "Lee"},
		{"fullName arg beats name claim", map[string]any{"name": "Claim Name"}, "Arg Name", "Arg", "Name"},
		{"empty everything", nil, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, last := NameFromClaims(tt.claims, tt.full)
			if first != tt.first || last != tt.last {
				t.Errorf("got (%q,%q), want (%q,%q)", first, last, tt.first, tt.last)
			}
		})
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"trims and keeps plain", "  Marcus  ", "Marcus"},
		{"strips newline/tab", "Mar\ncus\t", "Marcus"},
		{"strips NUL", "Jo\x00hnson", "Johnson"},
		{"strips ESC (ANSI)", "Mar\x1b[31mcus", "Mar[31mcus"},
		{"strips DEL", "Mar\x7fcus", "Marcus"},
		{"keeps multibyte unicode", "José", "José"},
		{"truncates to MaxNameLen runes", strings.Repeat("é", MaxNameLen+10), strings.Repeat("é", MaxNameLen)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeName(tt.in); got != tt.want {
				t.Errorf("SanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidateName(t *testing.T) {
	if err := ValidateName(""); err != nil {
		t.Errorf("empty name should be allowed: %v", err)
	}
	if err := ValidateName("Marcus"); err != nil {
		t.Errorf("normal name should be allowed: %v", err)
	}
	if err := ValidateName(strings.Repeat("x", MaxNameLen)); err != nil {
		t.Errorf("max-length name should be allowed: %v", err)
	}
	if err := ValidateName(strings.Repeat("x", MaxNameLen+1)); err == nil {
		t.Error("over-length name should be rejected")
	}
}
