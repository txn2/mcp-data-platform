package mcpapps

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// testdataDir returns the absolute path to the testdata directory.
func testdataDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	return filepath.Join(wd, "testdata")
}

func TestAppDefinition_Validate(t *testing.T) {
	testdata := testdataDir(t)

	tests := []struct {
		name    string
		app     *AppDefinition
		wantErr error
	}{
		{
			name: "valid app",
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
			name: "missing name",
			app: &AppDefinition{
				ResourceURI: regTestResourceURI,
				ToolNames:   []string{regTestToolName},
				AssetsPath:  testdata,
				EntryPoint:  regTestEntryPoint,
			},
			wantErr: ErrMissingName,
		},
		{
			name: "missing resource URI",
			app: &AppDefinition{
				Name:       regTestAppName,
				ToolNames:  []string{regTestToolName},
				AssetsPath: testdata,
				EntryPoint: regTestEntryPoint,
			},
			wantErr: ErrMissingResourceURI,
		},
		{
			name: "missing tool names",
			app: &AppDefinition{
				Name:        regTestAppName,
				ResourceURI: regTestResourceURI,
				ToolNames:   []string{},
				AssetsPath:  testdata,
				EntryPoint:  regTestEntryPoint,
			},
			wantErr: ErrMissingToolNames,
		},
		{
			name: "nil tool names",
			app: &AppDefinition{
				Name:        regTestAppName,
				ResourceURI: regTestResourceURI,
				ToolNames:   nil,
				AssetsPath:  testdata,
				EntryPoint:  regTestEntryPoint,
			},
			wantErr: ErrMissingToolNames,
		},
		{
			name: "missing assets path",
			app: &AppDefinition{
				Name:        regTestAppName,
				ResourceURI: regTestResourceURI,
				ToolNames:   []string{regTestToolName},
				EntryPoint:  regTestEntryPoint,
			},
			wantErr: ErrMissingAssetsPath,
		},
		{
			name: "content FS satisfies assets path requirement",
			app: &AppDefinition{
				Name:        regTestAppName,
				ResourceURI: regTestResourceURI,
				ToolNames:   []string{regTestToolName},
				Content:     fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
				EntryPoint:  regTestEntryPoint,
			},
			wantErr: nil,
		},
		{
			name: "missing entry point",
			app: &AppDefinition{
				Name:        regTestAppName,
				ResourceURI: regTestResourceURI,
				ToolNames:   []string{regTestToolName},
				AssetsPath:  testdata,
			},
			wantErr: ErrMissingEntryPoint,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.app.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAppDefinition_ValidateAssets(t *testing.T) {
	testdata := testdataDir(t)

	tests := []struct {
		name    string
		app     *AppDefinition
		wantErr error
	}{
		{
			name: "valid assets",
			app: &AppDefinition{
				AssetsPath: testdata,
				EntryPoint: regTestEntryPoint,
			},
			wantErr: nil,
		},
		{
			name: "content FS skips filesystem checks",
			app: &AppDefinition{
				Content:    fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
				EntryPoint: regTestEntryPoint,
			},
			wantErr: nil,
		},
		{
			name: "relative path",
			app: &AppDefinition{
				AssetsPath: "testdata",
				EntryPoint: regTestEntryPoint,
			},
			wantErr: ErrAssetsPathNotAbsolute,
		},
		{
			name: "missing entry point",
			app: &AppDefinition{
				AssetsPath: testdata,
				EntryPoint: "nonexistent.html",
			},
			wantErr: ErrEntryPointNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.app.ValidateAssets()
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateAssets() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
