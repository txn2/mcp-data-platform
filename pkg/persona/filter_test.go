package persona

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const (
	filterTestAnalyst       = "analyst"
	filterTestAdmin         = "admin"
	filterTestDatahubSearch = "datahub_search"
	filterTestTrinoQuery    = "trino_query"
	filterTestTrinoWild     = "trino_*"
	filterTestFilterCount   = 3
	filterTestWildcard      = "*"
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
				Name:  filterTestAdmin,
				Tools: ToolRules{Allow: []string{filterTestWildcard}},
			},
			toolName: "any_tool",
			want:     true,
		},
		{
			name: "prefix allow",
			persona: &Persona{
				Name:  filterTestAnalyst,
				Tools: ToolRules{Allow: []string{filterTestTrinoWild}},
			},
			toolName: filterTestTrinoQuery,
			want:     true,
		},
		{
			name: "prefix deny",
			persona: &Persona{
				Name:  filterTestAnalyst,
				Tools: ToolRules{Allow: []string{filterTestWildcard}, Deny: []string{"s3_delete_*"}},
			},
			toolName: "s3_delete_object",
			want:     false,
		},
		{
			name: "exact match allow",
			persona: &Persona{
				Name:  "exec",
				Tools: ToolRules{Allow: []string{filterTestDatahubSearch}},
			},
			toolName: filterTestDatahubSearch,
			want:     true,
		},
		{
			name: "no match deny",
			persona: &Persona{
				Name:  "exec",
				Tools: ToolRules{Allow: []string{filterTestDatahubSearch}},
			},
			toolName: filterTestTrinoQuery,
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
			Allow: []string{filterTestTrinoWild, "datahub_*"},
			Deny:  []string{"trino_admin*"},
		},
	}

	tools := []string{
		filterTestTrinoQuery,
		"trino_describe",
		"trino_admin_users",
		filterTestDatahubSearch,
		"s3_list_buckets",
	}

	allowed := filter.FilterTools(persona, tools)

	if len(allowed) != filterTestFilterCount {
		t.Errorf("FilterTools() returned %d tools, want %d", len(allowed), filterTestFilterCount)
	}

	// Check specific tools
	expected := map[string]bool{
		filterTestTrinoQuery:    true,
		"trino_describe":        true,
		filterTestDatahubSearch: true,
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
		{filterTestTrinoWild, filterTestTrinoQuery, true},
		{filterTestTrinoWild, filterTestDatahubSearch, false},
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
	return nil, nil //nolint:nilnil // test mock: nil means no persona found
}

func (m *mockRoleMapper) MapToRoles(claims map[string]any) ([]string, error) {
	if m.mapToRolesFunc != nil {
		return m.mapToRolesFunc(claims)
	}
	return nil, nil //nolint:nilnil // test mock: nil means no roles found
}

func TestAuthorizer_IsAuthorized_MapperError(t *testing.T) {
	reg := NewRegistry()
	mapper := &mockRoleMapper{
		mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
			return nil, errors.New("mapper error")
		},
	}
	auth := NewAuthorizer(reg, mapper)

	authorized, personaName, reason := auth.IsAuthorized(context.Background(), "user1", []string{"role1"}, "tool1")
	if authorized {
		t.Error("expected not authorized on mapper error")
	}
	if personaName != "" {
		t.Errorf("expected empty persona name on mapper error, got %q", personaName)
	}
	if reason != "failed to determine persona" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestAuthorizer_IsAuthorized_ToolNotAllowed(t *testing.T) {
	reg := NewRegistry()
	persona := &Persona{Name: filterTestAnalyst, Tools: ToolRules{Allow: []string{filterTestTrinoWild}}}
	mapper := &mockRoleMapper{
		mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
			return persona, nil
		},
	}
	auth := NewAuthorizer(reg, mapper)

	authorized, personaName, reason := auth.IsAuthorized(context.Background(), "user1", []string{filterTestAnalyst}, "s3_list_buckets")
	if authorized {
		t.Error("expected not authorized for disallowed tool")
	}
	if personaName != filterTestAnalyst {
		t.Errorf("expected persona name 'analyst', got %q", personaName)
	}
	if reason != "tool not allowed for persona: "+filterTestAnalyst {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestAuthorizer_IsAuthorized_ToolAllowed(t *testing.T) {
	reg := NewRegistry()
	persona := &Persona{Name: filterTestAdmin, Tools: ToolRules{Allow: []string{filterTestWildcard}}}
	mapper := &mockRoleMapper{
		mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
			return persona, nil
		},
	}
	auth := NewAuthorizer(reg, mapper)

	authorized, personaName, reason := auth.IsAuthorized(context.Background(), "user1", []string{filterTestAdmin}, "any_tool")
	if !authorized {
		t.Error("expected authorized for admin persona")
	}
	if personaName != filterTestAdmin {
		t.Errorf("expected persona name 'admin', got %q", personaName)
	}
	if reason != "" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestToolFilter_IsReadOnly(t *testing.T) {
	reg := NewRegistry()
	filter := NewToolFilter(reg)

	tests := []struct {
		name     string
		persona  *Persona
		toolName string
		want     bool
	}{
		{
			name:     "nil persona returns false",
			persona:  nil,
			toolName: filterTestTrinoQuery,
			want:     false,
		},
		{
			name: "empty read_only list",
			persona: &Persona{
				Name:  filterTestAnalyst,
				Tools: ToolRules{Allow: []string{filterTestTrinoWild}},
			},
			toolName: filterTestTrinoQuery,
			want:     false,
		},
		{
			name: "exact match",
			persona: &Persona{
				Name:  "viewer",
				Tools: ToolRules{Allow: []string{filterTestTrinoWild}, ReadOnly: []string{filterTestTrinoQuery}},
			},
			toolName: filterTestTrinoQuery,
			want:     true,
		},
		{
			name: "wildcard match",
			persona: &Persona{
				Name:  "viewer",
				Tools: ToolRules{Allow: []string{filterTestWildcard}, ReadOnly: []string{filterTestTrinoWild}},
			},
			toolName: filterTestTrinoQuery,
			want:     true,
		},
		{
			name: "no match",
			persona: &Persona{
				Name:  "viewer",
				Tools: ToolRules{Allow: []string{filterTestWildcard}, ReadOnly: []string{filterTestTrinoWild}},
			},
			toolName: filterTestDatahubSearch,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filter.IsReadOnly(tt.persona, tt.toolName)
			if got != tt.want {
				t.Errorf("IsReadOnly(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

func TestAuthorizer_IsToolReadOnly(t *testing.T) {
	reg := NewRegistry()

	t.Run("read_only match returns true", func(t *testing.T) {
		persona := &Persona{
			Name: "viewer",
			Tools: ToolRules{
				Allow:    []string{filterTestTrinoWild},
				ReadOnly: []string{filterTestTrinoQuery},
			},
		}
		mapper := &mockRoleMapper{
			mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
				return persona, nil
			},
		}
		auth := NewAuthorizer(reg, mapper)

		got := auth.IsToolReadOnly(context.Background(), []string{"viewer"}, filterTestTrinoQuery)
		if !got {
			t.Error("expected IsToolReadOnly to return true for matching tool")
		}
	})

	t.Run("no read_only match returns false", func(t *testing.T) {
		persona := &Persona{
			Name:  filterTestAnalyst,
			Tools: ToolRules{Allow: []string{filterTestTrinoWild}},
		}
		mapper := &mockRoleMapper{
			mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
				return persona, nil
			},
		}
		auth := NewAuthorizer(reg, mapper)

		got := auth.IsToolReadOnly(context.Background(), []string{filterTestAnalyst}, filterTestTrinoQuery)
		if got {
			t.Error("expected IsToolReadOnly to return false when no read_only rules")
		}
	})

	t.Run("mapper error returns false", func(t *testing.T) {
		mapper := &mockRoleMapper{
			mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
				return nil, errors.New("mapper error")
			},
		}
		auth := NewAuthorizer(reg, mapper)

		got := auth.IsToolReadOnly(context.Background(), []string{"role"}, filterTestTrinoQuery)
		if got {
			t.Error("expected IsToolReadOnly to return false on mapper error")
		}
	})

	t.Run("nil persona returns false", func(t *testing.T) {
		mapper := &mockRoleMapper{
			mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
				return nil, nil //nolint:nilnil // test: nil means no persona found
			},
		}
		auth := NewAuthorizer(reg, mapper)

		got := auth.IsToolReadOnly(context.Background(), []string{"unknown"}, filterTestTrinoQuery)
		if got {
			t.Error("expected IsToolReadOnly to return false for nil persona")
		}
	})
}

// Verify interface compliance.
var (
	_ RoleMapper                 = (*mockRoleMapper)(nil)
	_ middleware.Authorizer      = (*Authorizer)(nil)
	_ middleware.ReadOnlyChecker = (*Authorizer)(nil)
)
