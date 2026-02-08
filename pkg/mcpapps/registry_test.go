package mcpapps

import (
	"errors"
	"testing"
)

const (
	regTestAppName     = "test-app"
	regTestResourceURI = "ui://test-app"
	regTestToolName    = "test_tool"
	regTestEntryPoint  = "index.html"
	regTestToolA       = "tool_a"
	regTestToolB       = "tool_b"
	regTestRegFailed   = "Register() failed: %v"
	regTestToolCount   = 3
)

func TestRegistry_Register(t *testing.T) {
	testdata := testdataDir(t)

	tests := []struct {
		name    string
		app     *AppDefinition
		wantErr error
	}{
		{
			name: "valid registration",
			app: &AppDefinition{
				Name:        regTestAppName,
				ResourceURI: regTestResourceURI,
				ToolNames:   []string{regTestToolName},
				AssetsPath:  testdata,
				EntryPoint:  regTestEntryPoint,
			},
			wantErr: nil,
		},
		{
			name: "invalid app - missing name",
			app: &AppDefinition{
				ResourceURI: regTestResourceURI,
				ToolNames:   []string{regTestToolName},
				AssetsPath:  testdata,
				EntryPoint:  regTestEntryPoint,
			},
			wantErr: ErrMissingName,
		},
		{
			name: "invalid app - missing assets path",
			app: &AppDefinition{
				Name:        regTestAppName,
				ResourceURI: regTestResourceURI,
				ToolNames:   []string{regTestToolName},
				EntryPoint:  regTestEntryPoint,
			},
			wantErr: ErrMissingAssetsPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewRegistry()
			err := reg.Register(tt.app)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Register() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	testdata := testdataDir(t)
	reg := NewRegistry()

	app := &AppDefinition{
		Name:        regTestAppName,
		ResourceURI: regTestResourceURI,
		ToolNames:   []string{regTestToolName},
		AssetsPath:  testdata,
		EntryPoint:  regTestEntryPoint,
	}

	// First registration should succeed
	if err := reg.Register(app); err != nil {
		t.Fatalf("First Register() failed: %v", err)
	}

	// Second registration should fail
	err := reg.Register(app)
	if !errors.Is(err, ErrAppAlreadyRegistered) {
		t.Errorf("Second Register() error = %v, want %v", err, ErrAppAlreadyRegistered)
	}
}

func TestRegistry_Get(t *testing.T) {
	testdata := testdataDir(t)
	reg := NewRegistry()

	app := &AppDefinition{
		Name:        regTestAppName,
		ResourceURI: regTestResourceURI,
		ToolNames:   []string{regTestToolName},
		AssetsPath:  testdata,
		EntryPoint:  regTestEntryPoint,
	}

	if err := reg.Register(app); err != nil {
		t.Fatalf(regTestRegFailed, err)
	}

	// Test getting existing app
	got := reg.Get(regTestAppName)
	if got == nil {
		t.Fatal("Get() returned nil for existing app")
	}
	if got.Name != regTestAppName {
		t.Errorf("Get() returned app with wrong name: %s", got.Name)
	}

	// Test getting non-existent app
	got = reg.Get("non-existent")
	if got != nil {
		t.Error("Get() should return nil for non-existent app")
	}
}

func TestRegistry_GetForTool(t *testing.T) {
	testdata := testdataDir(t)
	reg := NewRegistry()

	app := &AppDefinition{
		Name:        regTestAppName,
		ResourceURI: regTestResourceURI,
		ToolNames:   []string{regTestToolA, regTestToolB},
		AssetsPath:  testdata,
		EntryPoint:  regTestEntryPoint,
	}

	if err := reg.Register(app); err != nil {
		t.Fatalf(regTestRegFailed, err)
	}

	// Test getting app by tool name
	tests := []struct {
		toolName string
		wantApp  bool
	}{
		{regTestToolA, true},
		{regTestToolB, true},
		{"tool_c", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			got := reg.GetForTool(tt.toolName)
			if (got != nil) != tt.wantApp {
				t.Errorf("GetForTool(%q) = %v, wantApp %v", tt.toolName, got, tt.wantApp)
			}
		})
	}
}

func TestRegistry_HasApps(t *testing.T) {
	testdata := testdataDir(t)
	reg := NewRegistry()

	// Empty registry
	if reg.HasApps() {
		t.Error("HasApps() should return false for empty registry")
	}

	// Register an app
	app := &AppDefinition{
		Name:        regTestAppName,
		ResourceURI: regTestResourceURI,
		ToolNames:   []string{regTestToolName},
		AssetsPath:  testdata,
		EntryPoint:  regTestEntryPoint,
	}

	if err := reg.Register(app); err != nil {
		t.Fatalf(regTestRegFailed, err)
	}

	if !reg.HasApps() {
		t.Error("HasApps() should return true after registration")
	}
}

func TestRegistry_Apps(t *testing.T) {
	testdata := testdataDir(t)
	reg := NewRegistry()

	// Empty registry
	apps := reg.Apps()
	if len(apps) != 0 {
		t.Errorf("Apps() returned %d apps for empty registry", len(apps))
	}

	// Register apps
	app1 := &AppDefinition{
		Name:        "app-1",
		ResourceURI: "ui://app-1",
		ToolNames:   []string{"tool_1"},
		AssetsPath:  testdata,
		EntryPoint:  regTestEntryPoint,
	}
	app2 := &AppDefinition{
		Name:        "app-2",
		ResourceURI: "ui://app-2",
		ToolNames:   []string{"tool_2"},
		AssetsPath:  testdata,
		EntryPoint:  regTestEntryPoint,
	}

	if err := reg.Register(app1); err != nil {
		t.Fatalf("Register(app1) failed: %v", err)
	}
	if err := reg.Register(app2); err != nil {
		t.Fatalf("Register(app2) failed: %v", err)
	}

	apps = reg.Apps()
	if len(apps) != 2 {
		t.Errorf("Apps() returned %d apps, want 2", len(apps))
	}
}

func TestRegistry_ToolNames(t *testing.T) {
	testdata := testdataDir(t)
	reg := NewRegistry()

	app := &AppDefinition{
		Name:        regTestAppName,
		ResourceURI: regTestResourceURI,
		ToolNames:   []string{regTestToolA, regTestToolB, "tool_c"},
		AssetsPath:  testdata,
		EntryPoint:  regTestEntryPoint,
	}

	if err := reg.Register(app); err != nil {
		t.Fatalf(regTestRegFailed, err)
	}

	names := reg.ToolNames()
	if len(names) != regTestToolCount {
		t.Errorf("ToolNames() returned %d names, want 3", len(names))
	}

	// Verify all tools are present
	nameSet := make(map[string]bool)
	for _, name := range names {
		nameSet[name] = true
	}

	for _, want := range []string{regTestToolA, regTestToolB, "tool_c"} {
		if !nameSet[want] {
			t.Errorf("ToolNames() missing %q", want)
		}
	}
}
