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

	t.Run("GetForRoles no match no default", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&Persona{Name: "analyst", Roles: []string{"analyst"}})

		_, ok := reg.GetForRoles([]string{"unknown"})
		if ok {
			t.Error("GetForRoles() returned true with no match and no default")
		}
	})

	t.Run("GetDefault not set", func(t *testing.T) {
		reg := NewRegistry()
		_, ok := reg.GetDefault()
		if ok {
			t.Error("GetDefault() returned true when no default set")
		}
	})

	t.Run("LoadFromConfig", func(t *testing.T) {
		reg := NewRegistry()
		config := map[string]*PersonaConfig{
			"analyst": {
				DisplayName: "Data Analyst",
				Description: "Analyst persona",
				Roles:       []string{"analyst", "data_engineer"},
				Tools: ToolRulesConfig{
					Allow: []string{"trino_*", "datahub_*"},
					Deny:  []string{"*_admin_*"},
				},
				Prompts: PromptConfigYAML{
					SystemPrefix: "You are a data analyst.",
					SystemSuffix: "Be helpful.",
					Instructions: "Focus on data quality",
				},
				Hints: map[string]string{
					"default_catalog": "hive",
				},
				Priority: 50,
			},
			"admin": {
				DisplayName: "Administrator",
				Roles:       []string{"admin"},
				Tools: ToolRulesConfig{
					Allow: []string{"*"},
				},
				Priority: 100,
			},
		}

		if err := reg.LoadFromConfig(config); err != nil {
			t.Fatalf("LoadFromConfig() error = %v", err)
		}

		// Verify analyst persona
		analyst, ok := reg.Get("analyst")
		if !ok {
			t.Fatal("analyst persona not found")
		}
		if analyst.DisplayName != "Data Analyst" {
			t.Errorf("analyst DisplayName = %q", analyst.DisplayName)
		}
		if analyst.Description != "Analyst persona" {
			t.Errorf("analyst Description = %q", analyst.Description)
		}
		if len(analyst.Roles) != 2 {
			t.Errorf("analyst has %d roles, want 2", len(analyst.Roles))
		}
		if len(analyst.Tools.Allow) != 2 {
			t.Errorf("analyst has %d allow rules, want 2", len(analyst.Tools.Allow))
		}
		if analyst.Prompts.SystemPrefix != "You are a data analyst." {
			t.Errorf("analyst SystemPrefix = %q", analyst.Prompts.SystemPrefix)
		}
		if analyst.Priority != 50 {
			t.Errorf("analyst Priority = %d, want 50", analyst.Priority)
		}

		// Verify admin persona
		admin, ok := reg.Get("admin")
		if !ok {
			t.Fatal("admin persona not found")
		}
		if admin.Priority != 100 {
			t.Errorf("admin Priority = %d, want 100", admin.Priority)
		}
	})

	t.Run("LoadFromConfig empty", func(t *testing.T) {
		reg := NewRegistry()
		config := map[string]*PersonaConfig{}

		if err := reg.LoadFromConfig(config); err != nil {
			t.Errorf("LoadFromConfig() with empty config error = %v", err)
		}
	})
}
