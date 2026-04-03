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

func TestApplyDescription(t *testing.T) {
	tests := []struct {
		name     string
		context  ContextOverrides
		base     string
		expected string
	}{
		{
			name:     "no overrides returns base",
			context:  ContextOverrides{},
			base:     "Base description",
			expected: "Base description",
		},
		{
			name: "prefix prepends to base",
			context: ContextOverrides{
				DescriptionPrefix: "You are a data analyst.",
			},
			base:     "Base description",
			expected: "You are a data analyst.\n\nBase description",
		},
		{
			name: "override replaces base",
			context: ContextOverrides{
				DescriptionOverride: "Completely custom description",
			},
			base:     "Base description",
			expected: "Completely custom description",
		},
		{
			name: "override wins over prefix",
			context: ContextOverrides{
				DescriptionPrefix:   "This prefix is ignored",
				DescriptionOverride: "Override wins",
			},
			base:     "Base description",
			expected: "Override wins",
		},
		{
			name: "empty base with prefix returns prefix only",
			context: ContextOverrides{
				DescriptionPrefix: "Just the prefix",
			},
			base:     "",
			expected: "Just the prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Persona{Context: tt.context}
			result := p.ApplyDescription(tt.base)
			if result != tt.expected {
				t.Errorf("ApplyDescription(%q) = %q, want %q", tt.base, result, tt.expected)
			}
		})
	}
}

func TestApplyAgentInstructions(t *testing.T) {
	tests := []struct {
		name     string
		context  ContextOverrides
		base     string
		expected string
	}{
		{
			name:     "no overrides returns base",
			context:  ContextOverrides{},
			base:     "Base instructions",
			expected: "Base instructions",
		},
		{
			name: "suffix appends to base",
			context: ContextOverrides{
				AgentInstructionsSuffix: "Always check DataHub first.",
			},
			base:     "Base instructions",
			expected: "Base instructions\n\nAlways check DataHub first.",
		},
		{
			name: "override replaces base",
			context: ContextOverrides{
				AgentInstructionsOverride: "Completely custom instructions",
			},
			base:     "Base instructions",
			expected: "Completely custom instructions",
		},
		{
			name: "override wins over suffix",
			context: ContextOverrides{
				AgentInstructionsSuffix:   "This suffix is ignored",
				AgentInstructionsOverride: "Override wins",
			},
			base:     "Base instructions",
			expected: "Override wins",
		},
		{
			name: "empty base with suffix returns suffix only",
			context: ContextOverrides{
				AgentInstructionsSuffix: "Just the suffix",
			},
			base:     "",
			expected: "Just the suffix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Persona{Context: tt.context}
			result := p.ApplyAgentInstructions(tt.base)
			if result != tt.expected {
				t.Errorf("ApplyAgentInstructions(%q) = %q, want %q", tt.base, result, tt.expected)
			}
		})
	}
}
