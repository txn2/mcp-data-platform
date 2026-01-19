package persona

import "testing"

func TestRegistry(t *testing.T) {
	t.Run("Register and Get", func(t *testing.T) {
		reg := NewRegistry()
		p := &Persona{Name: "test", DisplayName: "Test Persona"}

		if err := reg.Register(p); err != nil {
			t.Fatalf("Register() error = %v", err)
		}

		got, ok := reg.Get("test")
		if !ok {
			t.Fatal("Get() returned false")
		}
		if got.DisplayName != "Test Persona" {
			t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Test Persona")
		}
	})

	t.Run("Register empty name", func(t *testing.T) {
		reg := NewRegistry()
		p := &Persona{Name: ""}

		if err := reg.Register(p); err == nil {
			t.Error("Register() expected error for empty name")
		}
	})

	t.Run("Get not found", func(t *testing.T) {
		reg := NewRegistry()

		_, ok := reg.Get("nonexistent")
		if ok {
			t.Error("Get() returned true for nonexistent persona")
		}
	})

	t.Run("SetDefault and GetDefault", func(t *testing.T) {
		reg := NewRegistry()
		p := &Persona{Name: "default", DisplayName: "Default"}

		if err := reg.Register(p); err != nil {
			t.Fatalf("Register() error = %v", err)
		}

		reg.SetDefault("default")

		got, ok := reg.GetDefault()
		if !ok {
			t.Fatal("GetDefault() returned false")
		}
		if got.Name != "default" {
			t.Errorf("Name = %q, want %q", got.Name, "default")
		}
	})

	t.Run("All", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&Persona{Name: "p1"})
		_ = reg.Register(&Persona{Name: "p2"})

		all := reg.All()
		if len(all) != 2 {
			t.Errorf("All() returned %d personas, want 2", len(all))
		}
	})

	t.Run("GetForRoles", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&Persona{Name: "analyst", Roles: []string{"analyst"}, Priority: 10})
		_ = reg.Register(&Persona{Name: "admin", Roles: []string{"admin"}, Priority: 100})
		reg.SetDefault("analyst")

		// Should match admin (higher priority)
		got, ok := reg.GetForRoles([]string{"analyst", "admin"})
		if !ok {
			t.Fatal("GetForRoles() returned false")
		}
		if got.Name != "admin" {
			t.Errorf("Name = %q, want %q", got.Name, "admin")
		}

		// Should match analyst
		got, ok = reg.GetForRoles([]string{"analyst"})
		if !ok {
			t.Fatal("GetForRoles() returned false")
		}
		if got.Name != "analyst" {
			t.Errorf("Name = %q, want %q", got.Name, "analyst")
		}

		// Should fall back to default
		got, ok = reg.GetForRoles([]string{"unknown"})
		if !ok {
			t.Fatal("GetForRoles() returned false for fallback")
		}
		if got.Name != "analyst" {
			t.Errorf("Name = %q, want %q (default)", got.Name, "analyst")
		}
	})
}
