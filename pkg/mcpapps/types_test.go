package mcpapps

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
				Name:        "test-app",
				ResourceURI: "ui://test-app",
				ToolNames:   []string{"test_tool"},
				AssetsPath:  testdata,
				EntryPoint:  "index.html",
			},
			wantErr: nil,
		},
		{
			name: "missing name",
			app: &AppDefinition{
				ResourceURI: "ui://test-app",
				ToolNames:   []string{"test_tool"},
				AssetsPath:  testdata,
				EntryPoint:  "index.html",
			},
			wantErr: ErrMissingName,
		},
		{
			name: "missing resource URI",
			app: &AppDefinition{
				Name:       "test-app",
				ToolNames:  []string{"test_tool"},
				AssetsPath: testdata,
				EntryPoint: "index.html",
			},
			wantErr: ErrMissingResourceURI,
		},
		{
			name: "missing tool names",
			app: &AppDefinition{
				Name:        "test-app",
				ResourceURI: "ui://test-app",
				ToolNames:   []string{},
				AssetsPath:  testdata,
				EntryPoint:  "index.html",
			},
			wantErr: ErrMissingToolNames,
		},
		{
			name: "nil tool names",
			app: &AppDefinition{
				Name:        "test-app",
				ResourceURI: "ui://test-app",
				ToolNames:   nil,
				AssetsPath:  testdata,
				EntryPoint:  "index.html",
			},
			wantErr: ErrMissingToolNames,
		},
		{
			name: "missing assets path",
			app: &AppDefinition{
				Name:        "test-app",
				ResourceURI: "ui://test-app",
				ToolNames:   []string{"test_tool"},
				EntryPoint:  "index.html",
			},
			wantErr: ErrMissingAssetsPath,
		},
		{
			name: "missing entry point",
			app: &AppDefinition{
				Name:        "test-app",
				ResourceURI: "ui://test-app",
				ToolNames:   []string{"test_tool"},
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
				EntryPoint: "index.html",
			},
			wantErr: nil,
		},
		{
			name: "relative path",
			app: &AppDefinition{
				AssetsPath: "testdata",
				EntryPoint: "index.html",
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
