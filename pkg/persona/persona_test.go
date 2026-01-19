package persona

import "testing"

func TestDefaultPersona(t *testing.T) {
	p := DefaultPersona()

	if p.Name != "default" {
		t.Errorf("Name = %q, want %q", p.Name, "default")
	}
	if p.DisplayName != "Default User" {
		t.Errorf("DisplayName = %q, want %q", p.DisplayName, "Default User")
	}
	if len(p.Tools.Allow) != 1 || p.Tools.Allow[0] != "*" {
		t.Error("expected Allow to be [\"*\"]")
	}
	if len(p.Tools.Deny) != 0 {
		t.Error("expected Deny to be empty")
	}
}

func TestAdminPersona(t *testing.T) {
	p := AdminPersona()

	if p.Name != "admin" {
		t.Errorf("Name = %q, want %q", p.Name, "admin")
	}
	if p.Priority != 100 {
		t.Errorf("Priority = %d, want %d", p.Priority, 100)
	}
	if len(p.Roles) != 1 || p.Roles[0] != "admin" {
		t.Error("expected Roles to contain \"admin\"")
	}
}
