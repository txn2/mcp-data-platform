package resource

import (
	"strings"
	"testing"
)

func TestValidateCategory(t *testing.T) {
	tests := []struct {
		input string
		ok    bool
	}{
		{"samples", true},
		{"playbooks", true},
		{"my-cat", true},
		{"a", true},
		{"a0b", true},
		{"", false},
		{"0bad", false},
		{"-bad", false},
		{"UPPER", false},
		{"has space", false},
		{strings.Repeat("a", 32), false}, // too long
	}
	for _, tt := range tests {
		err := ValidateCategory(tt.input)
		if tt.ok && err != nil {
			t.Errorf("ValidateCategory(%q) unexpected error: %v", tt.input, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ValidateCategory(%q) expected error", tt.input)
		}
	}
}

func TestValidateDisplayName(t *testing.T) {
	tests := []struct {
		input string
		ok    bool
	}{
		{"My Resource", true},
		{"x", true},
		{"", false},
		{"   ", false},
		{strings.Repeat("a", 201), false},
		{strings.Repeat("a", 200), true},
	}
	for _, tt := range tests {
		err := ValidateDisplayName(tt.input)
		if tt.ok && err != nil {
			t.Errorf("ValidateDisplayName(%q) unexpected error: %v", tt.input, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ValidateDisplayName(%q) expected error", tt.input)
		}
	}
}

func TestValidateDescription(t *testing.T) {
	tests := []struct {
		input string
		ok    bool
	}{
		{"A description", true},
		{"x", true},
		{"", false},
		{"   ", false},
		{strings.Repeat("a", 2001), false},
		{strings.Repeat("a", 2000), true},
	}
	for _, tt := range tests {
		err := ValidateDescription(tt.input)
		if tt.ok && err != nil {
			t.Errorf("ValidateDescription(%q) unexpected error: %v", tt.input, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ValidateDescription(%q) expected error", tt.input)
		}
	}
}

func TestValidateTags(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		ok   bool
	}{
		{"empty", []string{}, true},
		{"valid", []string{"finance", "q4"}, true},
		{"with-hyphens", []string{"my-tag"}, true},
		{"too-many", make([]string, 21), false},
		{"invalid-chars", []string{"Bad Tag"}, false},
		{"uppercase", []string{"UPPER"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Fill dummy valid tags for too-many test
			if tt.name == "too-many" {
				for i := range tt.tags {
					tt.tags[i] = "tag"
				}
			}
			err := ValidateTags(tt.tags)
			if tt.ok && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.ok && err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestValidateMIMEType(t *testing.T) {
	allowed := []string{
		"text/plain",
		"application/json",
		"application/json; charset=utf-8",
		"text/csv",
		"image/png",
		"application/octet-stream",
	}
	for _, mt := range allowed {
		if err := ValidateMIMEType(mt); err != nil {
			t.Errorf("ValidateMIMEType(%q) should be allowed: %v", mt, err)
		}
	}

	denied := []string{
		"application/x-executable",
		"application/x-sh",
		"application/x-shellscript",
		"application/x-bat",
	}
	for _, mt := range denied {
		if err := ValidateMIMEType(mt); err == nil {
			t.Errorf("ValidateMIMEType(%q) should be denied", mt)
		}
	}
}

func TestValidateScope(t *testing.T) {
	tests := []struct {
		scope   Scope
		scopeID string
		ok      bool
	}{
		{ScopeGlobal, "", true},
		{ScopeGlobal, "bad", false},
		{ScopePersona, "analyst", true},
		{ScopePersona, "", false},
		{ScopeUser, "user-123", true},
		{ScopeUser, "", false},
		{Scope("invalid"), "", false},
	}
	for _, tt := range tests {
		err := ValidateScope(tt.scope, tt.scopeID)
		if tt.ok && err != nil {
			t.Errorf("ValidateScope(%q, %q) unexpected error: %v", tt.scope, tt.scopeID, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ValidateScope(%q, %q) expected error", tt.scope, tt.scopeID)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		ok       bool
	}{
		{"My File.csv", "my-file.csv", true},
		{"path/to/file.txt", "file.txt", true},
		{"UPPER.JSON", "upper.json", true},
		{"has spaces.md", "has-spaces.md", true},
		{"bad;chars|here.txt", "badcharshere.txt", true},
		{"", "", false},
		{"...", "...", true},
		{"###", "", false},
	}
	for _, tt := range tests {
		result, err := SanitizeFilename(tt.input)
		if tt.ok {
			if err != nil {
				t.Errorf("SanitizeFilename(%q) unexpected error: %v", tt.input, err)
			}
			if result != tt.expected {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		} else if err == nil {
			t.Errorf("SanitizeFilename(%q) expected error", tt.input)
		}
	}
}
