package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestManagePromptSchema(t *testing.T) {
	schema := managePromptSchema()
	assert.NotNil(t, schema)

	m, ok := schema.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "object", m["type"])

	props, ok := m["properties"].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, props, "command")
	assert.Contains(t, props, "name")
	assert.Contains(t, props, "content")
	assert.Contains(t, props, "scope")
	assert.Contains(t, props, "personas")
	assert.Contains(t, props, "search")

	required, ok := m["required"].([]string)
	assert.True(t, ok)
	assert.Contains(t, required, "command")
}

func TestPromptErrorResult(t *testing.T) {
	result := promptErrorResult("something went wrong")
	assert.True(t, result.IsError)
	assert.Len(t, result.Content, 1)
}

func TestPromptJSONResult(t *testing.T) {
	result, meta, err := promptJSONResult(map[string]string{"status": "ok"})
	assert.NoError(t, err)
	assert.Nil(t, meta)
	assert.False(t, result.IsError)
	assert.Len(t, result.Content, 1)
}

func TestResolveEmail_Anonymous(t *testing.T) {
	email := resolveEmail(t.Context())
	assert.Equal(t, "anonymous", email)
}

func TestIsAdminPersona_NoContext(t *testing.T) {
	p := &Platform{config: &Config{Admin: AdminConfig{Persona: "admin"}}}
	assert.False(t, p.isAdminPersona(t.Context()))
}

func TestIsBuiltinDisabled(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]bool
		prompt   string
		expected bool
	}{
		{"nil map", nil, "explore-available-data", false},
		{"not in map", map[string]bool{}, "explore-available-data", false},
		{"enabled", map[string]bool{"explore-available-data": true}, "explore-available-data", false},
		{"disabled", map[string]bool{"explore-available-data": false}, "explore-available-data", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Platform{config: &Config{Server: ServerConfig{BuiltinPrompts: tt.config}}}
			assert.Equal(t, tt.expected, p.isBuiltinDisabled(tt.prompt))
		})
	}
}
