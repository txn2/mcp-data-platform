package persona

import "testing"

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
			name:     "nil persona allows all",
			persona:  nil,
			toolName: "any_tool",
			want:     true,
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
