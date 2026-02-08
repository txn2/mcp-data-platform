package persona

import "testing"

const (
	personaTestAdminPriority = 100
	personaTestDefault       = "default"
	personaTestAnalyst       = "analyst"
	personaTestPriority50    = 50
)

func TestDefaultPersona(t *testing.T) {
	p := DefaultPersona()

	if p.Name != "default" {
		t.Errorf("Name = %q, want %q", p.Name, "default")
	}
	if p.DisplayName != "Default User (No Access)" {
		t.Errorf("DisplayName = %q, want %q", p.DisplayName, "Default User (No Access)")
	}
	// SECURITY: DefaultPersona now denies all access (fail closed)
	if len(p.Tools.Allow) != 0 {
		t.Error("expected Allow to be empty (deny by default)")
	}
	if len(p.Tools.Deny) != 1 || p.Tools.Deny[0] != "*" {
		t.Error("expected Deny to be [\"*\"] (explicit deny all)")
	}
}

func TestAdminPersona(t *testing.T) {
	p := AdminPersona()

	if p.Name != "admin" {
		t.Errorf("Name = %q, want %q", p.Name, "admin")
	}
	if p.Priority != personaTestAdminPriority {
		t.Errorf("Priority = %d, want %d", p.Priority, personaTestAdminPriority)
	}
	if len(p.Roles) != 1 || p.Roles[0] != "admin" {
		t.Error("expected Roles to contain \"admin\"")
	}
}

func TestGetFullSystemPrompt(t *testing.T) {
	tests := []struct {
		name     string
		prompts  PromptConfig
		expected string
	}{
		{
			name:     "empty prompts",
			prompts:  PromptConfig{},
			expected: "",
		},
		{
			name: "only prefix",
			prompts: PromptConfig{
				SystemPrefix: "You are a helpful assistant.",
			},
			expected: "You are a helpful assistant.",
		},
		{
			name: "prefix and suffix",
			prompts: PromptConfig{
				SystemPrefix: "You are a helpful assistant.",
				SystemSuffix: "Be concise.",
			},
			expected: "You are a helpful assistant.\n\nBe concise.",
		},
		{
			name: "all three parts",
			prompts: PromptConfig{
				SystemPrefix: "You are a data analyst.",
				Instructions: "Always check DataHub first.",
				SystemSuffix: "Format output as JSON.",
			},
			expected: "You are a data analyst.\n\nAlways check DataHub first.\n\nFormat output as JSON.",
		},
		{
			name: "only instructions",
			prompts: PromptConfig{
				Instructions: "Check metadata before queries.",
			},
			expected: "Check metadata before queries.",
		},
		{
			name: "instructions and suffix",
			prompts: PromptConfig{
				Instructions: "Check metadata before queries.",
				SystemSuffix: "Return structured data.",
			},
			expected: "Check metadata before queries.\n\nReturn structured data.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Persona{Prompts: tt.prompts}
			result := p.GetFullSystemPrompt()
			if result != tt.expected {
				t.Errorf("GetFullSystemPrompt() = %q, want %q", result, tt.expected)
			}
		})
	}
}
