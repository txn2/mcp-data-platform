package persona

import "testing"

func TestRegistry_RegisterAndGet(t *testing.T) {
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
}

func TestRegistry_RegisterEmptyName(t *testing.T) {
	reg := NewRegistry()
	p := &Persona{Name: ""}

	if err := reg.Register(p); err == nil {
		t.Error("Register() expected error for empty name")
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	reg := NewRegistry()

	_, ok := reg.Get("nonexistent")
	if ok {
		t.Error("Get() returned true for nonexistent persona")
	}
}

func TestRegistry_SetDefaultAndGetDefault(t *testing.T) {
	reg := NewRegistry()
	p := &Persona{Name: personaTestDefault, DisplayName: "Default"}

	if err := reg.Register(p); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	reg.SetDefault(personaTestDefault)

	got, ok := reg.GetDefault()
	if !ok {
		t.Fatal("GetDefault() returned false")
	}
	if got.Name != personaTestDefault {
		t.Errorf("Name = %q, want %q", got.Name, personaTestDefault)
	}
}

func TestRegistry_All(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&Persona{Name: "p1"})
	_ = reg.Register(&Persona{Name: "p2"})

	all := reg.All()
	if len(all) != 2 {
		t.Errorf("All() returned %d personas, want 2", len(all))
	}
}

func TestRegistry_GetForRoles(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&Persona{Name: personaTestAnalyst, Roles: []string{personaTestAnalyst}, Priority: 10})
	_ = reg.Register(&Persona{Name: filterTestAdmin, Roles: []string{filterTestAdmin}, Priority: personaTestAdminPriority})
	reg.SetDefault(personaTestAnalyst)

	// Should match admin (higher priority)
	got, ok := reg.GetForRoles([]string{personaTestAnalyst, filterTestAdmin})
	if !ok {
		t.Fatal("GetForRoles() returned false")
	}
	if got.Name != filterTestAdmin {
		t.Errorf("Name = %q, want %q", got.Name, filterTestAdmin)
	}

	// Should match analyst
	got, ok = reg.GetForRoles([]string{personaTestAnalyst})
	if !ok {
		t.Fatal("GetForRoles() returned false")
	}
	if got.Name != personaTestAnalyst {
		t.Errorf("Name = %q, want %q", got.Name, personaTestAnalyst)
	}

	// Should fall back to default
	got, ok = reg.GetForRoles([]string{"unknown"})
	if !ok {
		t.Fatal("GetForRoles() returned false for fallback")
	}
	if got.Name != personaTestAnalyst {
		t.Errorf("Name = %q, want %q (default)", got.Name, personaTestAnalyst)
	}
}

func TestRegistry_GetForRolesNoMatchNoDefault(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&Persona{Name: personaTestAnalyst, Roles: []string{personaTestAnalyst}})

	_, ok := reg.GetForRoles([]string{"unknown"})
	if ok {
		t.Error("GetForRoles() returned true with no match and no default")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	t.Run("removes existing persona", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&Persona{Name: "test"})

		err := reg.Unregister("test")
		if err != nil {
			t.Fatalf("Unregister() error = %v", err)
		}

		_, ok := reg.Get("test")
		if ok {
			t.Error("Get() returned true after Unregister")
		}
	})

	t.Run("returns error for non-existent persona", func(t *testing.T) {
		reg := NewRegistry()

		err := reg.Unregister("nonexistent")
		if err == nil {
			t.Error("Unregister() expected error for non-existent persona")
		}
	})

	t.Run("clears default when unregistering default persona", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&Persona{Name: personaTestDefault})
		reg.SetDefault(personaTestDefault)

		err := reg.Unregister(personaTestDefault)
		if err != nil {
			t.Fatalf("Unregister() error = %v", err)
		}

		if reg.DefaultName() != "" {
			t.Errorf("DefaultName() = %q, want empty after unregistering default", reg.DefaultName())
		}
	})

	t.Run("does not clear default when unregistering other persona", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&Persona{Name: personaTestDefault})
		_ = reg.Register(&Persona{Name: "other"})
		reg.SetDefault(personaTestDefault)

		err := reg.Unregister("other")
		if err != nil {
			t.Fatalf("Unregister() error = %v", err)
		}

		if reg.DefaultName() != personaTestDefault {
			t.Errorf("DefaultName() = %q, want %q", reg.DefaultName(), personaTestDefault)
		}
	})
}

func TestRegistry_DefaultName(t *testing.T) {
	t.Run("returns empty when no default set", func(t *testing.T) {
		reg := NewRegistry()
		if reg.DefaultName() != "" {
			t.Errorf("DefaultName() = %q, want empty", reg.DefaultName())
		}
	})

	t.Run("returns default name", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&Persona{Name: personaTestDefault})
		reg.SetDefault(personaTestDefault)

		if reg.DefaultName() != personaTestDefault {
			t.Errorf("DefaultName() = %q, want %q", reg.DefaultName(), personaTestDefault)
		}
	})
}

func TestRegistry_GetDefaultNotSet(t *testing.T) {
	reg := NewRegistry()
	_, ok := reg.GetDefault()
	if ok {
		t.Error("GetDefault() returned true when no default set")
	}
}

func TestRegistry_LoadFromConfig(t *testing.T) {
	reg := NewRegistry()
	config := map[string]*Config{
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
			Priority: personaTestPriority50,
		},
		filterTestAdmin: {
			DisplayName: "Administrator",
			Roles:       []string{filterTestAdmin},
			Tools: ToolRulesConfig{
				Allow: []string{"*"},
			},
			Priority: personaTestAdminPriority,
		},
	}

	if err := reg.LoadFromConfig(config); err != nil {
		t.Fatalf("LoadFromConfig() error = %v", err)
	}

	verifyLoadedAnalystPersona(t, reg)
	verifyLoadedAdminPersona(t, reg)
}

func verifyLoadedAnalystPersona(t *testing.T, reg *Registry) {
	t.Helper()
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
	if analyst.Priority != personaTestPriority50 {
		t.Errorf("analyst Priority = %d, want 50", analyst.Priority)
	}
}

func verifyLoadedAdminPersona(t *testing.T, reg *Registry) {
	t.Helper()
	admin, ok := reg.Get(filterTestAdmin)
	if !ok {
		t.Fatal("admin persona not found")
	}
	if admin.Priority != personaTestAdminPriority {
		t.Errorf("admin Priority = %d, want %d", admin.Priority, personaTestAdminPriority)
	}
}

func TestRegistry_LoadFromConfigEmpty(t *testing.T) {
	reg := NewRegistry()
	config := map[string]*Config{}

	if err := reg.LoadFromConfig(config); err != nil {
		t.Errorf("LoadFromConfig() with empty config error = %v", err)
	}
}

func TestRegistry_LoadFromConfigEmptyName(t *testing.T) {
	reg := NewRegistry()
	config := map[string]*Config{
		"": {DisplayName: "Invalid"}, // Empty name should fail Register
	}

	if err := reg.LoadFromConfig(config); err == nil {
		t.Error("LoadFromConfig() expected error for empty persona name")
	}
}
