package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// generatedKeyBytes is the number of random bytes for generated API keys.
const generatedKeyBytes = 32

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

// APIKeySummary is a safe representation of a key (never exposes the key value).
type APIKeySummary struct {
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

// APIKeyAuthenticator authenticates using API keys.
type APIKeyAuthenticator struct {
	mu   sync.RWMutex
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

	a.mu.RLock()
	defer a.mu.RUnlock()

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
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys[key.Key] = &key
}

// RemoveKey removes an API key by its value.
func (a *APIKeyAuthenticator) RemoveKey(keyValue string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.keys, keyValue)
}

// ListKeys returns summaries of all registered keys (never exposes key values).
func (a *APIKeyAuthenticator) ListKeys() []APIKeySummary {
	a.mu.RLock()
	defer a.mu.RUnlock()

	summaries := make([]APIKeySummary, 0, len(a.keys))
	for _, k := range a.keys {
		summaries = append(summaries, APIKeySummary{
			Name:  k.Name,
			Roles: k.Roles,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})
	return summaries
}

// RemoveByName removes an API key by its display name.
// Returns true if a key was found and removed.
func (a *APIKeyAuthenticator) RemoveByName(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	for k, v := range a.keys {
		if v.Name == name {
			delete(a.keys, k)
			return true
		}
	}
	return false
}

// GenerateKey creates a new API key with server-generated value.
// Returns the key value (shown only once) or an error if the name is duplicate.
func (a *APIKeyAuthenticator) GenerateKey(name string, roles []string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check for duplicate name
	for _, v := range a.keys {
		if v.Name == name {
			return "", fmt.Errorf("key with name %q already exists", name)
		}
	}

	// Generate random key
	b := make([]byte, generatedKeyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random key: %w", err)
	}
	keyValue := hex.EncodeToString(b)

	a.keys[keyValue] = &APIKey{
		Key:   keyValue,
		Name:  name,
		Roles: roles,
	}

	return keyValue, nil
}

// Verify interface compliance.
var _ middleware.Authenticator = (*APIKeyAuthenticator)(nil)
