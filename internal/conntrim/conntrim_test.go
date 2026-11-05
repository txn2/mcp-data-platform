package conntrim

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValues(t *testing.T) {
	cfg := map[string]any{
		"base_url":            " https://api.example.com\n",
		"oauth_token_url":     "\thttps://idp.example.com/token ",
		"oauth_client_id":     " client\r\n",
		"oauth_scope":         " read write ",
		"call_timeout":        " 30s",
		"connect_timeout":     10,
		"oauth_client_secret": " keep me ",
		"credential":          "token ",
		"session_login_body":  " {\"a\": 1} ",
		"static_headers":      map[string]any{"X-Key": " v "},
	}
	Values(cfg)
	assert.Equal(t, "https://api.example.com", cfg["base_url"])
	assert.Equal(t, "https://idp.example.com/token", cfg["oauth_token_url"])
	assert.Equal(t, "client", cfg["oauth_client_id"])
	assert.Equal(t, "read write", cfg["oauth_scope"], "inner spaces are the value")
	assert.Equal(t, "30s", cfg["call_timeout"])
	assert.Equal(t, 10, cfg["connect_timeout"], "a number is left as it is")
	assert.Equal(t, " keep me ", cfg["oauth_client_secret"], "a secret is stored as sent")
	assert.Equal(t, "token ", cfg["credential"])
	assert.Equal(t, " {\"a\": 1} ", cfg["session_login_body"])
	assert.Equal(t, map[string]any{"X-Key": " v "}, cfg["static_headers"])
	assert.NotContains(t, Values(map[string]any{}), "base_url", "an absent key is not added")
}
