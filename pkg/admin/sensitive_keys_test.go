package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// An S3 session token and session_login headers are redacted on a connection
// read and in the config export, like every key encrypted at rest (#2359).
func TestSessionCredentialsAreRedacted(t *testing.T) {
	cfg := map[string]any{
		"endpoint":              "https://s3.example.com",
		"session_token":         "FwoGZXIvYXdz-session",
		"session_login_headers": map[string]any{"X-Login-Key": "lk-1", "Ref": "{{secret:login}}"},
		"static_headers":        map[string]any{"X-API-Key": "sk-1"},
	}
	got := redactConnectionConfig(cfg)
	assert.Equal(t, redactedValue, got["session_token"])
	assert.Equal(t, map[string]any{"X-Login-Key": redactedValue, "Ref": "{{secret:login}}"}, got["session_login_headers"])
	assert.Equal(t, "https://s3.example.com", got["endpoint"])

	exported, err := yaml.Marshal(map[string]any{"toolkits": map[string]any{"s3": map[string]any{"instances": map[string]any{"a": cfg}}}})
	require.NoError(t, err)
	redacted, err := redactYAML(exported)
	require.NoError(t, err)
	for _, secret := range []string{"FwoGZXIvYXdz-session", "lk-1", "sk-1"} {
		assert.NotContains(t, string(redacted), secret)
	}
	assert.Contains(t, string(redacted), "X-API-Key", "a header's name is kept")
}
