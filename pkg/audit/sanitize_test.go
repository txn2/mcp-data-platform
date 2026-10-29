package audit

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
)

// Every secret in a call's arguments is replaced, however deep, and in a body
// sent as JSON text; what names or describes a secret is kept (#2356).
func TestSanitizeParametersRedactsAtAnyDepth(t *testing.T) {
	got := SanitizeParameters(map[string]any{
		"connection": "platform-admin",
		"method":     "POST",
		"path":       "/api/v1/admin/webhooks/sources",
		"headers":    map[string]any{"Authorization": "Bearer abc", "X-API-Key": "k1", "Ocp-Apim-Subscription-Key": "k2", "Accept": "application/json"},
		"body": map[string]any{
			"name": "orders",
			"auth": map[string]any{"preset": "mattermost", "secret": "s1", "previous_secret": "s0", "token_path": "token", "secret_name": "named"},
			"items": []any{
				map[string]any{"password": "p1", "username": "u"},
			},
		},
		"query_params": map[string]any{"api_key": "q1", "page": "2"},
		"key":          "objects/report.csv",
	})
	text, err := json.Marshal(got)
	require.NoError(t, err)
	for _, secret := range []string{"Bearer abc", "k1", "k2", "s1", "s0", "p1", "q1"} {
		assert.NotContains(t, string(text), secret)
	}
	for _, kept := range []string{"platform-admin", "application/json", "orders", "mattermost", `"token_path":"token"`, "named", `"username":"u"`, `"page":"2"`, "objects/report.csv"} {
		assert.Contains(t, string(text), kept)
	}
}

func TestSanitizeParametersReadsJSONText(t *testing.T) {
	got := SanitizeParameters(map[string]any{
		"body":  `{"config":{"password":"p1","host":"db"},"list":[{"client_secret":"c1"}]}`,
		"sql":   "SELECT 'password' FROM t",
		"plain": "{not json",
		"array": `[{"token":"t1"}]`,
	})
	body, _ := got["body"].(string)
	array, _ := got["array"].(string)
	assert.JSONEq(t, `{"config":{"host":"db","password":"[REDACTED]"},"list":[{"client_secret":"[REDACTED]"}]}`, body)
	assert.JSONEq(t, `[{"token":"[REDACTED]"}]`, array)
	assert.Equal(t, "SELECT 'password' FROM t", got["sql"], "a string that is not JSON is kept")
	assert.Equal(t, "{not json", got["plain"])
}

// A stored secret's value is sent under "value", which is only a secret on
// the route that sets one.
func TestSanitizeParametersStoredSecretValue(t *testing.T) {
	set := SanitizeParameters(map[string]any{
		"path": "/api/v1/admin/secrets/github-token", "method": "PUT",
		"body": `{"value":"ghp_secret","allow_connections":["github"]}`,
	})
	setBody, _ := set["body"].(string)
	assert.JSONEq(t, `{"allow_connections":["github"],"value":"[REDACTED]"}`, setBody)

	byOperation := SanitizeParameters(map[string]any{
		"operation_id": "PUT /admin/secrets/{name}", "path_params": map[string]any{"name": "gh"},
		"body": map[string]any{"value": "ghp_secret"},
	})
	assert.Equal(t, map[string]any{"value": "[REDACTED]"}, byOperation["body"], "a call naming the route by operation_id")

	other := SanitizeParameters(map[string]any{"path": "/api/v1/admin/personas/x", "body": map[string]any{"value": "kept"}})
	assert.Equal(t, map[string]any{"value": "kept"}, other["body"])
}

// A credential written in a query string, in a path or a form-encoded body,
// is redacted; prose and SQL holding "=" are kept as written.
func TestSanitizeParametersReadsQueryText(t *testing.T) {
	got := SanitizeParameters(map[string]any{
		"path": "/v1/items?page=2&api_key=q1",
		"body": "user=ann&password=p%40ss&remember=1",
		"sql":  "SELECT * FROM t WHERE token = 'abc'",
		"url":  "https://api.example.com/x?client_secret=c1",
		"note": "a=b",
	})
	assert.Equal(t, "/v1/items?page=2&api_key=[REDACTED]", got["path"])
	assert.Equal(t, "user=ann&password=[REDACTED]&remember=1", got["body"])
	assert.Equal(t, "SELECT * FROM t WHERE token = 'abc'", got["sql"])
	assert.Equal(t, "https://api.example.com/x?client_secret=[REDACTED]", got["url"])
	assert.Equal(t, "a=b", got["note"])
}

// Every key the platform encrypts at rest, and every key whose value is a map
// of secrets, is redacted from an audit row's arguments (#2359), so a
// connection saved through a tool call never leaves one in audit_logs.
func TestSanitizeParametersCoversEveryEncryptedKey(t *testing.T) {
	for _, k := range fieldcrypt.SensitiveConfigKeyList() {
		got := SanitizeParameters(map[string]any{"body": map[string]any{"config": map[string]any{k: "v4lue"}}})
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "v4lue", "key %s", k)
	}
	for _, k := range fieldcrypt.SensitiveNestedMapKeyList() {
		got := SanitizeParameters(map[string]any{"body": map[string]any{k: map[string]any{"X-Anything": "v4lue"}}})
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "v4lue", "nested key %s", k)
	}
}

func TestSanitizeParametersSecretMaps(t *testing.T) {
	got := SanitizeParameters(map[string]any{"config": map[string]any{
		"static_headers": map[string]any{"X-Quota": "q1"}, "session_login_headers": "not-a-map",
	}})
	cfg, _ := got["config"].(map[string]any)
	assert.Equal(t, map[string]any{"X-Quota": "[REDACTED]"}, cfg["static_headers"], "names kept, values replaced")
	assert.Equal(t, "[REDACTED]", cfg["session_login_headers"])
}
