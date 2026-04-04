package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

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
	Key         string     // The API key value (plaintext; used for file-loaded keys)
	KeyHash     string     // bcrypt hash of the key value (used for DB-loaded keys)
	Name        string     // Display name for the key
	Email       string     // Email address for this key (optional; defaults to name@apikey.local)
	Description string     // Human-readable description of what this key is for
	Roles       []string   // Roles assigned to this key
	ExpiresAt   *time.Time // Optional expiration time (nil = never expires)
}

// IsExpired returns true if the key has an expiration date that has passed.
func (k *APIKey) IsExpired() bool {
	return k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now())
}

// APIKeySummary is a safe representation of a key (never exposes the key value).
type APIKeySummary struct {
	Name        string     `json:"name"`
	Email       string     `json:"email,omitempty"`
	Description string     `json:"description,omitempty"`
	Roles       []string   `json:"roles"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Expired     bool       `json:"expired,omitempty"`
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

	// Look up the key: use constant-time compare for plaintext keys,
	// bcrypt compare for hashed keys (DB-loaded).
	var matchedKey *APIKey
	for k, v := range a.keys {
		if v.Key != "" {
			// File-loaded key: constant-time comparison on raw value.
			if subtle.ConstantTimeCompare([]byte(k), []byte(token)) == 1 {
				matchedKey = v
				break
			}
		} else if v.KeyHash != "" {
			// DB-loaded key: bcrypt comparison on hash.
			if bcrypt.CompareHashAndPassword([]byte(v.KeyHash), []byte(token)) == nil {
				matchedKey = v
				break
			}
		}
	}

	if matchedKey == nil {
		return nil, fmt.Errorf("invalid API key")
	}

	if matchedKey.IsExpired() {
		return nil, fmt.Errorf("api key %q has expired", matchedKey.Name)
	}

	return &middleware.UserInfo{
		UserID:   "apikey:" + matchedKey.Name,
		Email:    apiKeyEmail(*matchedKey),
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

// AddHashedKey adds a bcrypt-hashed API key at runtime.
// The key is indexed by name (prefixed with "hash:") since we don't have
// the raw value. Authentication uses bcrypt comparison for these keys.
func (a *APIKeyAuthenticator) AddHashedKey(key APIKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Use a synthetic map key since we don't have the raw key value.
	mapKey := "hash:" + key.Name
	a.keys[mapKey] = &key
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
			Name:        k.Name,
			Email:       apiKeyEmail(*k),
			Description: k.Description,
			Roles:       k.Roles,
			ExpiresAt:   k.ExpiresAt,
			Expired:     k.IsExpired(),
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
func (a *APIKeyAuthenticator) GenerateKey(def APIKey) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check for duplicate name
	for _, v := range a.keys {
		if v.Name == def.Name {
			return "", fmt.Errorf("key with name %q already exists", def.Name)
		}
	}

	// Generate random key
	b := make([]byte, generatedKeyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random key: %w", err)
	}
	keyValue := hex.EncodeToString(b)

	def.Key = keyValue
	a.keys[keyValue] = &def

	return keyValue, nil
}

// apiKeyEmail returns the email for an API key, falling back to name@apikey.local.
func apiKeyEmail(key APIKey) string {
	if key.Email != "" {
		return key.Email
	}
	return key.Name + "@apikey.local"
}

// Verify interface compliance.
var _ middleware.Authenticator = (*APIKeyAuthenticator)(nil)
