package resource

import (
	"testing"
)

func TestBuildURI(t *testing.T) {
	tests := []struct {
		scheme   string
		scope    Scope
		scopeID  string
		category string
		filename string
		want     string
	}{
		{"mcp", ScopeGlobal, "", "playbooks", "readme.md", "mcp://global/playbooks/readme.md"},
		{"mcp", ScopePersona, "finance", "templates", "spec.yaml", "mcp://persona/finance/templates/spec.yaml"},
		{"mcp", ScopeUser, "user-123", "samples", "data.csv", "mcp://user/user-123/samples/data.csv"},
		{"acme", ScopeGlobal, "", "references", "guide.md", "acme://global/references/guide.md"},
		{"", ScopeGlobal, "", "samples", "test.csv", "mcp://global/samples/test.csv"}, // default scheme
	}
	for _, tt := range tests {
		got := BuildURI(tt.scheme, tt.scope, tt.scopeID, tt.category, tt.filename)
		if got != tt.want {
			t.Errorf("BuildURI(%q, %q, %q, %q, %q) = %q, want %q",
				tt.scheme, tt.scope, tt.scopeID, tt.category, tt.filename, got, tt.want)
		}
	}
}

func TestBuildS3Key(t *testing.T) {
	got := BuildS3Key(ScopeGlobal, "", "res-id", "file.txt")
	want := "resources/global/global/res-id/file.txt"
	if got != want {
		t.Errorf("BuildS3Key global = %q, want %q", got, want)
	}

	got = BuildS3Key(ScopePersona, "finance", "res-id", "spec.yaml")
	want = "resources/persona/finance/res-id/spec.yaml"
	if got != want {
		t.Errorf("BuildS3Key persona = %q, want %q", got, want)
	}

	got = BuildS3Key(ScopeUser, "u-123", "res-id", "data.csv")
	want = "resources/user/u-123/res-id/data.csv"
	if got != want {
		t.Errorf("BuildS3Key user = %q, want %q", got, want)
	}
}

func TestParseURI(t *testing.T) {
	tests := []struct {
		scheme  string
		uri     string
		scope   Scope
		scopeID string
		path    string
		ok      bool
	}{
		{"mcp", "mcp://global/playbooks/readme.md", ScopeGlobal, "", "playbooks/readme.md", true},
		{"mcp", "mcp://persona/finance/templates/spec.yaml", ScopePersona, "finance", "templates/spec.yaml", true},
		{"mcp", "mcp://user/u-123/samples/data.csv", ScopeUser, "u-123", "samples/data.csv", true},
		{"acme", "acme://global/refs/guide.md", ScopeGlobal, "", "refs/guide.md", true},
		{"mcp", "other://global/test", "", "", "", false},        // wrong scheme
		{"mcp", "mcp://unknown/test", "", "", "", false},         // unknown scope
		{"mcp", "mcp://persona", "", "", "", false},              // missing path
		{"mcp", "mcp://user/", "", "", "", false},                // missing scope_id path
	}
	for _, tt := range tests {
		scope, scopeID, path, err := ParseURI(tt.scheme, tt.uri)
		if tt.ok {
			if err != nil {
				t.Errorf("ParseURI(%q, %q) unexpected error: %v", tt.scheme, tt.uri, err)
				continue
			}
			if scope != tt.scope || scopeID != tt.scopeID || path != tt.path {
				t.Errorf("ParseURI(%q, %q) = (%q, %q, %q), want (%q, %q, %q)",
					tt.scheme, tt.uri, scope, scopeID, path, tt.scope, tt.scopeID, tt.path)
			}
		} else if err == nil {
			t.Errorf("ParseURI(%q, %q) expected error", tt.scheme, tt.uri)
		}
	}
}
