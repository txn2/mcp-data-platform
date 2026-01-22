package persona

import "testing"

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
	if p.Priority != 100 {
		t.Errorf("Priority = %d, want %d", p.Priority, 100)
	}
	if len(p.Roles) != 1 || p.Roles[0] != "admin" {
		t.Error("expected Roles to contain \"admin\"")
	}
}
