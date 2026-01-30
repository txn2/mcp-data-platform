package mcpapps

import (
	"embed"
	"testing"
)

//go:embed testdata/*
var testAssets embed.FS

func TestAppDefinition_Validate(t *testing.T) {
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
				EntryPoint:  "index.html",
			},
			wantErr: nil,
		},
		{
			name: "missing name",
			app: &AppDefinition{
				ResourceURI: "ui://test-app",
				ToolNames:   []string{"test_tool"},
				EntryPoint:  "index.html",
			},
			wantErr: ErrMissingName,
		},
		{
			name: "missing resource URI",
			app: &AppDefinition{
				Name:       "test-app",
				ToolNames:  []string{"test_tool"},
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
				EntryPoint:  "index.html",
			},
			wantErr: ErrMissingToolNames,
		},
		{
			name: "missing entry point",
			app: &AppDefinition{
				Name:        "test-app",
				ResourceURI: "ui://test-app",
				ToolNames:   []string{"test_tool"},
			},
			wantErr: ErrMissingEntryPoint,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.app.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
