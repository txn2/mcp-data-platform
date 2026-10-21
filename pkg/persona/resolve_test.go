package persona

import "testing"

// One persona resolution and one admin rule everywhere (#2228).

func resolveRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	for _, p := range []*Persona{
		{Name: "admin", Roles: []string{"admin"}, Priority: 10},
		{Name: "ops", Roles: []string{"ops"}, Priority: 50},
		{Name: "beta", Roles: []string{"analyst"}, Priority: 5},
		{Name: "alpha", Roles: []string{"analyst"}, Priority: 5},
		{Name: "mapped", Roles: []string{"nobody-holds-this"}},
	} {
		if err := r.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestGetForRoles_ATieGoesToTheFirstNameOnEveryRequest(t *testing.T) {
	r := resolveRegistry(t)
	for range 50 {
		if p, _ := r.GetForRoles([]string{"analyst"}); p.Name != "alpha" {
			t.Fatalf("resolved %q, want alpha every time", p.Name)
		}
	}
}

func TestGetForRoles_AMappedRoleNamesItsPersonaFirst(t *testing.T) {
	r := resolveRegistry(t)
	r.SetRoleMapping(map[string]string{"sso-reader": "mapped", "sso-ghost": "missing"})
	if p, ok := r.GetForRoles([]string{"analyst", "sso-reader"}); !ok || p.Name != "mapped" {
		t.Errorf("resolved %v, want the mapped persona over role matching", p)
	}
	if p, _ := r.GetForRoles([]string{"sso-ghost", "analyst"}); p.Name != "alpha" {
		t.Errorf("a mapping to an unregistered persona resolved %q", p.Name)
	}
	var nilReg *Registry
	nilReg.SetRoleMapping(map[string]string{"a": "b"})
}

func TestAdminRule_IsTheResolvedPersona(t *testing.T) {
	r := resolveRegistry(t)
	isAdmin := r.AdminRule("admin")
	if !isAdmin([]string{"admin"}) {
		t.Error("a user resolving to the admin persona is not an administrator")
	}
	// Matching the admin persona's role is not enough when a higher-priority
	// persona also matches: the user resolves to ops.
	if isAdmin([]string{"admin", "ops"}) {
		t.Error("a user resolving to a higher-priority persona was made an administrator")
	}
	if isAdmin([]string{"analyst"}) || isAdmin(nil) {
		t.Error("a non-admin was made an administrator")
	}
	r.SetRoleMapping(map[string]string{"sso-root": "admin"})
	if !isAdmin([]string{"sso-root"}) {
		t.Error("a role mapped to the admin persona is not an administrator")
	}
	if r.AdminRule("")([]string{"admin"}) {
		t.Error("an empty admin persona made someone an administrator")
	}
	var nilReg *Registry
	if nilReg.AdminRule("admin")([]string{"admin"}) {
		t.Error("a nil registry made someone an administrator")
	}
}

func TestSharingAllowed_DefaultsOnAndReadsTheRegistry(t *testing.T) {
	var nilPersona *Persona
	if !nilPersona.SharingAllowed() || !(&Persona{}).SharingAllowed() {
		t.Error("an unset permission withheld sharing")
	}
	denied := false
	r := NewRegistry()
	if err := r.Register(&Persona{Name: "viewer", Roles: []string{"v"}, AllowSharing: &denied}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&Persona{Name: "analyst", Roles: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if r.SharingAllowed("viewer") || !r.SharingAllowed("analyst") || r.SharingAllowed("unknown") {
		t.Error("the registry read the permission wrong")
	}
	var nilReg *Registry
	if nilReg.SharingAllowed("analyst") {
		t.Error("a nil registry allowed sharing")
	}
}
