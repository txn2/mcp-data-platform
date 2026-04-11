package middleware

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// mockResourceStore implements ResourceListProvider for testing.
type mockResourceStore struct {
	resources []resource.Resource
	byURI     map[string]*resource.Resource
}

func (m *mockResourceStore) List(_ context.Context, _ resource.Filter) ([]resource.Resource, int, error) {
	return m.resources, len(m.resources), nil
}

func (m *mockResourceStore) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	if r, ok := m.byURI[uri]; ok {
		return r, nil
	}
	return nil, &notFoundError{uri: uri}
}

type notFoundError struct{ uri string }

func (e *notFoundError) Error() string { return "not found: " + e.uri }

// mockBlobReader implements ResourceBlobReader for testing.
type mockBlobReader struct {
	data        map[string][]byte
	contentType string
}

func (m *mockBlobReader) GetObject(_ context.Context, _, key string) ([]byte, string, error) {
	if d, ok := m.data[key]; ok {
		return d, m.contentType, nil
	}
	return nil, "", &notFoundError{uri: key}
}

func TestIsTextMIME(t *testing.T) {
	tests := []struct {
		mime string
		want bool
	}{
		{"text/plain", true},
		{"text/csv", true},
		{"text/html", true},
		{"application/json", true},
		{"application/xml", true},
		{"application/yaml", true},
		{"application/sql", true},
		{"image/png", false},
		{"application/pdf", false},
		{"application/octet-stream", false},
	}
	for _, tt := range tests {
		got := isTextMIME(tt.mime)
		if got != tt.want {
			t.Errorf("isTextMIME(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}

func TestExtractResourceURI(t *testing.T) {
	// Build a mock request matching resources/read format.
	reqData := map[string]any{
		"params": map[string]any{
			"uri": "mcp://global/samples/test.csv",
		},
	}
	raw, _ := json.Marshal(reqData)
	var req mcp.ReadResourceRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	uri, err := extractResourceURI(&req)
	if err != nil {
		t.Fatalf("extractResourceURI error: %v", err)
	}
	if uri != "mcp://global/samples/test.csv" {
		t.Errorf("got %q, want %q", uri, "mcp://global/samples/test.csv")
	}
}

func TestScopesFromContext_NoPlatformContext(t *testing.T) {
	cfg := ManagedResourceConfig{}
	scopes := scopesFromContext(context.Background(), cfg)
	if len(scopes) != 1 || scopes[0].Scope != resource.ScopeGlobal {
		t.Errorf("expected global-only scope, got %v", scopes)
	}
}

func TestScopesFromContext_WithPlatformContext(t *testing.T) {
	pc := &PlatformContext{
		UserID:      "user-1",
		Roles:       []string{"analyst"},
		PersonaName: "analyst",
	}
	ctx := WithPlatformContext(context.Background(), pc)

	cfg := ManagedResourceConfig{
		PersonasForRoles: func(roles []string) []string {
			return []string{"analyst", "viewer"}
		},
	}
	scopes := scopesFromContext(ctx, cfg)

	// Should have: global + user/user-1 + persona/analyst + persona/viewer = 4
	if len(scopes) != 4 {
		t.Errorf("expected 4 scopes, got %d: %v", len(scopes), scopes)
	}
}

func TestClaimsFromPC(t *testing.T) {
	pc := &PlatformContext{
		UserID:      "u1",
		Roles:       []string{"admin"},
		PersonaName: "admin",
	}

	// Without PersonasForRoles, falls back to PersonaName
	cfg := ManagedResourceConfig{}
	claims := claimsFromPC(pc, cfg)
	if claims.Sub != "u1" {
		t.Errorf("Sub = %q, want %q", claims.Sub, "u1")
	}
	if len(claims.Personas) != 1 || claims.Personas[0] != "admin" {
		t.Errorf("Personas = %v, want [admin]", claims.Personas)
	}

	// With PersonasForRoles
	cfg.PersonasForRoles = func(_ []string) []string { return []string{"a", "b"} }
	claims = claimsFromPC(pc, cfg)
	if len(claims.Personas) != 2 {
		t.Errorf("Personas = %v, want [a b]", claims.Personas)
	}
}
