package auth

import (
	"context"
	"crypto/subtle"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// APIKeyConfig holds API key configuration.
type APIKeyConfig struct {
	Keys []APIKey
}

// APIKey represents an API key entry.
type APIKey struct {
	Key   string   // The API key value
	Name  string   // Display name for the key
	Roles []string // Roles assigned to this key
}

// APIKeyAuthenticator authenticates using API keys.
type APIKeyAuthenticator struct {
	keys map[string]*APIKey
}

// NewAPIKeyAuthenticator creates a new API key authenticator.
func NewAPIKeyAuthenticator(cfg APIKeyConfig) *APIKeyAuthenticator {
	keys := make(map[string]*APIKey)
	for i := range cfg.Keys {
		key := &cfg.Keys[i]
		keys[key.Key] = key
	}
	return &APIKeyAuthenticator{keys: keys}
}

// Authenticate validates the API key and returns user info.
func (a *APIKeyAuthenticator) Authenticate(ctx context.Context) (*middleware.UserInfo, error) {
	token := GetToken(ctx)
	if token == "" {
		return nil, fmt.Errorf("no API key found in context")
	}

	// Look up the key (constant-time comparison)
	var matchedKey *APIKey
	for k, v := range a.keys {
		if subtle.ConstantTimeCompare([]byte(k), []byte(token)) == 1 {
			matchedKey = v
			break
		}
	}

	if matchedKey == nil {
		return nil, fmt.Errorf("invalid API key")
	}

	return &middleware.UserInfo{
		UserID:   "apikey:" + matchedKey.Name,
		Email:    matchedKey.Name + "@apikey.local",
		Claims:   make(map[string]any),
		Roles:    matchedKey.Roles,
		AuthType: "apikey",
	}, nil
}

// AddKey adds an API key at runtime.
func (a *APIKeyAuthenticator) AddKey(key APIKey) {
	a.keys[key.Key] = &key
}

// RemoveKey removes an API key.
func (a *APIKeyAuthenticator) RemoveKey(keyValue string) {
	delete(a.keys, keyValue)
}

// Verify interface compliance.
var _ middleware.Authenticator = (*APIKeyAuthenticator)(nil)
