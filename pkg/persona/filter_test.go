package persona

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

func TestToolFilter_IsAllowed(t *testing.T) {
	reg := NewRegistry()
	filter := NewToolFilter(reg)

	tests := []struct {
		name     string
		persona  *Persona
		toolName string
		want     bool
	}{
		{
			name:     "nil persona denies all",
			persona:  nil,
			toolName: "any_tool",
			want:     false, // SECURITY: fail closed - nil persona denies access
		},
		{
			name: "wildcard allow",
			persona: &Persona{
				Name:  "admin",
				Tools: ToolRules{Allow: []string{"*"}},
			},
			toolName: "any_tool",
			want:     true,
		},
		{
			name: "prefix allow",
			persona: &Persona{
				Name:  "analyst",
				Tools: ToolRules{Allow: []string{"trino_*"}},
			},
			toolName: "trino_query",
			want:     true,
		},
		{
			name: "prefix deny",
			persona: &Persona{
				Name:  "analyst",
				Tools: ToolRules{Allow: []string{"*"}, Deny: []string{"s3_delete_*"}},
			},
			toolName: "s3_delete_object",
			want:     false,
		},
		{
			name: "exact match allow",
			persona: &Persona{
				Name:  "exec",
				Tools: ToolRules{Allow: []string{"datahub_search"}},
			},
			toolName: "datahub_search",
			want:     true,
		},
		{
			name: "no match deny",
			persona: &Persona{
				Name:  "exec",
				Tools: ToolRules{Allow: []string{"datahub_search"}},
			},
			toolName: "trino_query",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filter.IsAllowed(tt.persona, tt.toolName)
			if got != tt.want {
				t.Errorf("IsAllowed(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

func TestToolFilter_FilterTools(t *testing.T) {
	reg := NewRegistry()
	filter := NewToolFilter(reg)

	persona := &Persona{
		Name: "analyst",
		Tools: ToolRules{
			Allow: []string{"trino_*", "datahub_*"},
			Deny:  []string{"trino_admin*"},
		},
	}

	tools := []string{
		"trino_query",
		"trino_describe",
		"trino_admin_users",
		"datahub_search",
		"s3_list_buckets",
	}

	allowed := filter.FilterTools(persona, tools)

	if len(allowed) != 3 {
		t.Errorf("FilterTools() returned %d tools, want 3", len(allowed))
	}

	// Check specific tools
	expected := map[string]bool{
		"trino_query":    true,
		"trino_describe": true,
		"datahub_search": true,
	}

	for _, tool := range allowed {
		if !expected[tool] {
			t.Errorf("unexpected tool in result: %s", tool)
		}
	}
}

func TestToolFilter_FilterTools_NilPersona(t *testing.T) {
	reg := NewRegistry()
	filter := NewToolFilter(reg)

	tools := []string{"tool1", "tool2", "tool3"}
	allowed := filter.FilterTools(nil, tools)

	// SECURITY: nil persona denies all tools (fail closed)
	if len(allowed) != 0 {
		t.Errorf("FilterTools(nil) should return no tools (fail closed), got %d", len(allowed))
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*", "anything", true},
		{"trino_*", "trino_query", true},
		{"trino_*", "datahub_search", false},
		{"exact_match", "exact_match", true},
		{"exact_match", "other", false},
		{"prefix_*_suffix", "prefix_middle_suffix", true},
		{"[invalid", "test", false}, // Invalid pattern
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.name, func(t *testing.T) {
			got := matchPattern(tt.pattern, tt.name)
			if got != tt.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

// mockRoleMapper implements RoleMapper for testing.
type mockRoleMapper struct {
	mapToPersonaFunc func(ctx context.Context, roles []string) (*Persona, error)
	mapToRolesFunc   func(claims map[string]any) ([]string, error)
}

func (m *mockRoleMapper) MapToPersona(ctx context.Context, roles []string) (*Persona, error) {
	if m.mapToPersonaFunc != nil {
		return m.mapToPersonaFunc(ctx, roles)
	}
	return nil, nil
}

func (m *mockRoleMapper) MapToRoles(claims map[string]any) ([]string, error) {
	if m.mapToRolesFunc != nil {
		return m.mapToRolesFunc(claims)
	}
	return nil, nil
}

func TestPersonaAuthorizer_IsAuthorized(t *testing.T) {
	reg := NewRegistry()

	t.Run("mapper error returns not authorized", func(t *testing.T) {
		mapper := &mockRoleMapper{
			mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
				return nil, errors.New("mapper error")
			},
		}
		auth := NewPersonaAuthorizer(reg, mapper)

		authorized, reason := auth.IsAuthorized(context.Background(), "user1", []string{"role1"}, "tool1")
		if authorized {
			t.Error("expected not authorized on mapper error")
		}
		if reason != "failed to determine persona" {
			t.Errorf("unexpected reason: %s", reason)
		}
	})

	t.Run("tool not allowed for persona", func(t *testing.T) {
		persona := &Persona{
			Name:  "analyst",
			Tools: ToolRules{Allow: []string{"trino_*"}},
		}
		mapper := &mockRoleMapper{
			mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
				return persona, nil
			},
		}
		auth := NewPersonaAuthorizer(reg, mapper)

		authorized, reason := auth.IsAuthorized(context.Background(), "user1", []string{"analyst"}, "s3_list_buckets")
		if authorized {
			t.Error("expected not authorized for disallowed tool")
		}
		if reason != "tool not allowed for persona: analyst" {
			t.Errorf("unexpected reason: %s", reason)
		}
	})

	t.Run("tool allowed for persona", func(t *testing.T) {
		persona := &Persona{
			Name:  "admin",
			Tools: ToolRules{Allow: []string{"*"}},
		}
		mapper := &mockRoleMapper{
			mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
				return persona, nil
			},
		}
		auth := NewPersonaAuthorizer(reg, mapper)

		authorized, reason := auth.IsAuthorized(context.Background(), "user1", []string{"admin"}, "any_tool")
		if !authorized {
			t.Error("expected authorized for admin persona")
		}
		if reason != "" {
			t.Errorf("unexpected reason: %s", reason)
		}
	})
}

// Verify interface compliance.
var _ RoleMapper = (*mockRoleMapper)(nil)
var _ middleware.Authorizer = (*PersonaAuthorizer)(nil)
