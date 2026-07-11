package mwchain

import (
	"strings"
	"testing"
)

func TestValidate_ValidOrderingAccepted(t *testing.T) {
	// Any order that satisfies the declared dependencies passes, so the
	// validator flags genuine ordering bugs rather than every edit.
	specs := []Spec{
		{Name: "tool_call"},
		{Name: "session_gate", Requires: []Name{"tool_call"}},
		{Name: "workflow_gate", Requires: []Name{"tool_call", "session_gate"}},
		{Name: "audit", Requires: []Name{"tool_call"}},
	}
	if err := Validate(specs); err != nil {
		t.Fatalf("valid ordering rejected: %v", err)
	}
}

func TestValidate_Violations(t *testing.T) {
	tests := []struct {
		name    string
		specs   []Spec
		wantErr string
	}{
		{
			name: "required dependency is inner (reader outer to its writer)",
			specs: []Spec{
				{Name: "audit", Requires: []Name{"tool_call"}},
				{Name: "tool_call"},
			},
			wantErr: `requires "tool_call" to be outer`,
		},
		{
			name: "required dependency at same position (self-reference)",
			specs: []Spec{
				{Name: "audit", Requires: []Name{"audit"}},
			},
			wantErr: "to be outer",
		},
		{
			name: "requires unknown middleware",
			specs: []Spec{
				{Name: "tool_call"},
				{Name: "audit", Requires: []Name{"does_not_exist"}},
			},
			wantErr: "unknown middleware",
		},
		{
			name: "duplicate middleware name",
			specs: []Spec{
				{Name: "tool_call"},
				{Name: "tool_call"},
			},
			wantErr: "declared more than once",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.specs)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidate_Empty(t *testing.T) {
	if err := Validate(nil); err != nil {
		t.Fatalf("empty chain should validate, got %v", err)
	}
}
