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
// File-loaded keys (plaintext) are stored in fileKeys for O(1) lookup.
// DB-loaded keys (bcrypt hashed) are stored in hashedKeys as a slice,
// checked via bcrypt only when no file key matches — limiting DoS surface.
type APIKeyAuthenticator struct {
	mu         sync.RWMutex
	fileKeys   map[string]*APIKey // indexed by raw key value
	hashedKeys []*APIKey          // DB-loaded keys, checked via bcrypt
}

// NewAPIKeyAuthenticator creates a new API key authenticator.
func NewAPIKeyAuthenticator(cfg APIKeyConfig) *APIKeyAuthenticator {
	fileKeys := make(map[string]*APIKey)
	for i := range cfg.Keys {
		key := &cfg.Keys[i]
		fileKeys[key.Key] = key
	}
	return &APIKeyAuthenticator{
		fileKeys:   fileKeys,
		hashedKeys: nil,
	}
}

// Authenticate validates the API key and returns user info.
// It first checks file keys via constant-time compare (fast O(1) path),
// then falls back to bcrypt comparison against hashed keys (slow path).
func (a *APIKeyAuthenticator) Authenticate(ctx context.Context) (*middleware.UserInfo, error) {
	token := GetToken(ctx)
	if token == "" {
		return nil, fmt.Errorf("no API key found in context")
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	var matchedKey *APIKey

	// Fast path: O(1) map lookup for file-loaded keys.
	if candidate, ok := a.fileKeys[token]; ok {
		if subtle.ConstantTimeCompare([]byte(candidate.Key), []byte(token)) == 1 {
			matchedKey = candidate
		}
	}

	// Slow path: bcrypt comparison for DB-loaded hashed keys.
	// Only attempted when no file key matched, limiting DoS surface.
	if matchedKey == nil {
		for _, v := range a.hashedKeys {
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

// AddKey adds a plaintext API key at runtime.
// The KeyHash field is ignored to prevent accidental misuse;
// use AddHashedKey for bcrypt-hashed keys.
func (a *APIKeyAuthenticator) AddKey(key APIKey) {
	key.KeyHash = "" // guard: plaintext keys must not carry a hash
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fileKeys[key.Key] = &key
}

// AddHashedKey adds a bcrypt-hashed API key at runtime.
// The Key (plaintext) field is cleared to prevent accidental exposure.
// These keys are stored in a separate slice and authenticated via bcrypt.
func (a *APIKeyAuthenticator) AddHashedKey(key APIKey) {
	key.Key = "" // guard: hashed keys must not carry plaintext
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hashedKeys = append(a.hashedKeys, &key)
}

// RemoveKey removes a plaintext API key by its value.
func (a *APIKeyAuthenticator) RemoveKey(keyValue string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fileKeys, keyValue)
}

// ListKeys returns summaries of all registered keys (never exposes key values).
func (a *APIKeyAuthenticator) ListKeys() []APIKeySummary {
	a.mu.RLock()
	defer a.mu.RUnlock()

	summaries := make([]APIKeySummary, 0, len(a.fileKeys)+len(a.hashedKeys))
	for _, k := range a.fileKeys {
		summaries = append(summaries, APIKeySummary{
			Name:        k.Name,
			Email:       apiKeyEmail(*k),
			Description: k.Description,
			Roles:       k.Roles,
			ExpiresAt:   k.ExpiresAt,
			Expired:     k.IsExpired(),
		})
	}
	for _, k := range a.hashedKeys {
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
// Searches both file keys and hashed keys. Returns true if a key was found
// and removed.
func (a *APIKeyAuthenticator) RemoveByName(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check file keys first.
	for k, v := range a.fileKeys {
		if v.Name == name {
			delete(a.fileKeys, k)
			return true
		}
	}

	// Check hashed keys.
	for i, v := range a.hashedKeys {
		if v.Name == name {
			// Remove by swapping with last element (order doesn't matter).
			a.hashedKeys[i] = a.hashedKeys[len(a.hashedKeys)-1]
			a.hashedKeys[len(a.hashedKeys)-1] = nil // avoid memory leak
			a.hashedKeys = a.hashedKeys[:len(a.hashedKeys)-1]
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

	// Check for duplicate name across both collections.
	for _, v := range a.fileKeys {
		if v.Name == def.Name {
			return "", fmt.Errorf("key with name %q already exists", def.Name)
		}
	}
	for _, v := range a.hashedKeys {
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
	def.KeyHash = "" // generated keys are plaintext (value is known)
	a.fileKeys[keyValue] = &def

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
