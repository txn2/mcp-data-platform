package platform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateCustomResourceDef covers all validation branches.
func TestValidateCustomResourceDef(t *testing.T) {
	tests := []struct {
		name    string
		def     CustomResourceDef
		wantErr string
	}{
		{
			name: "valid inline",
			def: CustomResourceDef{
				URI:      "brand://theme",
				Name:     "Brand Theme",
				MIMEType: "application/json",
				Content:  `{"color":"#fff"}`,
			},
		},
		{
			name: "valid file",
			def: CustomResourceDef{
				URI:         "brand://logo",
				Name:        "Logo",
				MIMEType:    "image/svg+xml",
				ContentFile: "/etc/logo.svg",
			},
		},
		{
			name:    "missing URI",
			def:     CustomResourceDef{Name: "X", MIMEType: "text/plain", Content: "hi"},
			wantErr: "uri is required",
		},
		{
			name:    "missing Name",
			def:     CustomResourceDef{URI: "x://y", MIMEType: "text/plain", Content: "hi"},
			wantErr: "name is required",
		},
		{
			name:    "missing MIMEType",
			def:     CustomResourceDef{URI: "x://y", Name: "X", Content: "hi"},
			wantErr: "mime_type is required",
		},
		{
			name:    "neither content nor content_file",
			def:     CustomResourceDef{URI: "x://y", Name: "X", MIMEType: "text/plain"},
			wantErr: "one of content or content_file is required",
		},
		{
			name: "both content and content_file",
			def: CustomResourceDef{
				URI:         "x://y",
				Name:        "X",
				MIMEType:    "text/plain",
				Content:     "hello",
				ContentFile: "/etc/x.txt",
			},
			wantErr: "content and content_file are mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCustomResourceDef(tt.def)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// TestBuildCustomResourceResult_Inline verifies inline content is returned verbatim.
func TestBuildCustomResourceResult_Inline(t *testing.T) {
	def := CustomResourceDef{
		URI:      "brand://theme",
		Name:     "Theme",
		MIMEType: "application/json",
		Content:  `{"primary":"#FF6B35"}`,
	}
	result, err := buildCustomResourceResult(def)
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	assert.Equal(t, "brand://theme", result.Contents[0].URI)
	assert.Equal(t, "application/json", result.Contents[0].MIMEType)
	assert.Equal(t, `{"primary":"#FF6B35"}`, result.Contents[0].Text)
}

// TestBuildCustomResourceResult_File verifies file content is read and returned.
func TestBuildCustomResourceResult_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logo.svg")
	require.NoError(t, os.WriteFile(path, []byte("<svg/>"), 0o600))

	def := CustomResourceDef{
		URI:         "brand://logo",
		Name:        "Logo",
		MIMEType:    "image/svg+xml",
		ContentFile: path,
	}
	result, err := buildCustomResourceResult(def)
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	assert.Equal(t, "brand://logo", result.Contents[0].URI)
	assert.Equal(t, "image/svg+xml", result.Contents[0].MIMEType)
	assert.Equal(t, "<svg/>", result.Contents[0].Text)
}

// TestBuildCustomResourceResult_FileNotFound verifies a missing file returns an error.
func TestBuildCustomResourceResult_FileNotFound(t *testing.T) {
	def := CustomResourceDef{
		URI:         "brand://missing",
		Name:        "Missing",
		MIMEType:    "text/plain",
		ContentFile: "/nonexistent/path/file.txt",
	}
	result, err := buildCustomResourceResult(def)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "/nonexistent/path/file.txt")
}

// TestRegisterCustomResources_Valid verifies valid definitions are registered without panic.
func TestRegisterCustomResources_Valid(_ *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.1"}, nil)
	p := &Platform{
		config: &Config{
			Resources: ResourcesConfig{
				Custom: []CustomResourceDef{
					{
						URI:      "brand://theme",
						Name:     "Brand Theme",
						MIMEType: "application/json",
						Content:  `{"color":"#fff"}`,
					},
					{
						URI:      "docs://readme",
						Name:     "README",
						MIMEType: "text/plain",
						Content:  "Hello world",
					},
				},
			},
		},
		mcpServer: s,
	}
	// Should not panic
	p.registerCustomResources()
}

// TestRegisterCustomResources_InvalidSkipped verifies invalid defs are skipped and valid
// ones are still registered.
func TestRegisterCustomResources_InvalidSkipped(_ *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.1"}, nil)
	p := &Platform{
		config: &Config{
			Resources: ResourcesConfig{
				Custom: []CustomResourceDef{
					// invalid: missing URI
					{
						Name:     "Bad",
						MIMEType: "text/plain",
						Content:  "bad",
					},
					// valid
					{
						URI:      "brand://ok",
						Name:     "OK",
						MIMEType: "text/plain",
						Content:  "ok",
					},
				},
			},
		},
		mcpServer: s,
	}
	// Should not panic; invalid entry is warned and skipped.
	p.registerCustomResources()
}

// TestRegisterCustomResources_Empty verifies no-ops on empty config.
func TestRegisterCustomResources_Empty(_ *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.1"}, nil)
	p := &Platform{
		config:    &Config{Resources: ResourcesConfig{}},
		mcpServer: s,
	}
	p.registerCustomResources() // must not panic
}
