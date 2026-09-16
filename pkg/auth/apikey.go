package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/singleflight"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// generatedKeyBytes is the number of random bytes for generated API keys.
const generatedKeyBytes = 32

// hashedKeySyncTimeout bounds the store read a refused key shares with every
// other refused key arriving at the same moment. It is detached from the one
// request that started it, so that request going away does not refuse the
// others.
const hashedKeySyncTimeout = 5 * time.Second

// errInvalidAPIKey is the refusal for a token no key matches.
var errInvalidAPIKey = errors.New("invalid API key")

// ErrKeyNameTaken is GenerateKey's refusal for a name already held. It is a
// sentinel so a caller can tell it from the other way minting fails -- a
// shortage of entropy -- which is not the requester's doing and must not be
// reported to them as a name they chose being unavailable (#1759).
var ErrKeyNameTaken = errors.New("key name already exists")

// APIKeyConfig holds API key configuration.
type APIKeyConfig struct {
	Keys []APIKey
}

// API key provenance, surfaced in APIKeySummary.Source. Config-file keys reappear
// on restart and are protected from admin-API deletion; database keys, which
// include every key the admin API generates, are deletable.
const (
	sourceFile     = "file"
	sourceDatabase = "database"
	sourceBoth     = "both"
)

// APIKey represents an API key entry.
type APIKey struct {
	Key         string     // The API key value (plaintext; used for file-loaded keys)
	KeyHash     string     // bcrypt hash of the key value (used for DB-loaded keys)
	Name        string     // Display name for the key
	Email       string     // Email address for this key (optional; defaults to the synthetic name@apikey.local, which receives no mail)
	Description string     // Human-readable description of what this key is for
	Roles       []string   // Roles assigned to this key; empty on a bound key means the roles its person holds
	ExpiresAt   *time.Time // Optional expiration time (nil = never expires)
	// UserEmail is the account this key is issued against, or "" for the
	// standalone service identity a key has always been. A bound key
	// authenticates as that person: their subject, their address, and their
	// roles unless Roles narrows them (#1759).
	UserEmail string
}

// IsExpired returns true if the key has an expiration date that has passed.
func (k *APIKey) IsExpired() bool {
	return k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now())
}

// APIKeySummary is a safe representation of a key (never exposes the key value).
type APIKeySummary struct {
	Name        string     `json:"name" example:"ci-pipeline"`
	Email       string     `json:"email,omitempty" example:"ci@example.com"`
	Description string     `json:"description,omitempty" example:"CI/CD pipeline integration"`
	Roles       []string   `json:"roles" example:"analyst"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Expired     bool       `json:"expired,omitempty" example:"false"`
	Source      string     `json:"source,omitempty" example:"database"` // "file", "database", or "both"
	// UserEmail is the account the key is issued against, absent on a service
	// key bound to nobody. A key listing is how an administrator sees whose a
	// key is, including one a person issued for themselves.
	UserEmail string `json:"user_email,omitempty" example:"analyst@example.com"`
}

// HashedKeySource is the store the database-managed keys live in. Every
// replica of a deployment reads the one store, so when a source is attached it
// is the record of which of those keys exist, and the keys the authenticator
// holds in memory are a copy of it that another replica's write can make stale
// (#1715).
type HashedKeySource interface {
	// HashedKeys returns every key the store holds, each with its bcrypt hash.
	HashedKeys(ctx context.Context) ([]APIKey, error)
	// HoldsKey reports whether the store holds the key named name with the
	// bcrypt hash keyHash. A key deleted and created again under the same name
	// carries a different hash, so the old one is not held.
	HoldsKey(ctx context.Context, name, keyHash string) (bool, error)
}

// APIKeyAuthenticator authenticates using API keys.
// File-loaded keys (plaintext) are stored in fileKeys for O(1) lookup.
// DB-loaded keys (bcrypt hashed) are stored in hashedKeys as a slice,
// checked via bcrypt only when no file key matches — limiting DoS surface.
//
// With a HashedKeySource attached, a DB-loaded key is accepted only while the
// store still holds it, and a token that matches none of them re-reads the
// store once before it is refused, so a key written through another replica
// takes effect here the moment that write returns.
type APIKeyAuthenticator struct {
	mu         sync.RWMutex
	fileKeys   map[string]*APIKey // indexed by raw key value
	hashedKeys []*APIKey          // DB-loaded keys, checked via bcrypt
	source     HashedKeySource
	principals PrincipalSource
	syncs      singleflight.Group
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
		return nil, errors.New("no API key found in context")
	}

	a.mu.RLock()
	fileKey := a.matchFileKey(token)
	// A copy: dropHashedKey rewrites the slice in place, and the bcrypt
	// comparisons below run without the lock.
	hashed := slices.Clone(a.hashedKeys)
	source := a.source
	a.mu.RUnlock()

	if fileKey != nil {
		return a.userInfo(ctx, fileKey)
	}

	// Slow path: bcrypt comparison for DB-loaded hashed keys.
	// Only attempted when no file key matched, limiting DoS surface.
	matched := matchHashedKey(hashed, token)
	if source == nil {
		if matched == nil {
			return nil, errInvalidAPIKey
		}
		return a.userInfo(ctx, matched)
	}
	if matched == nil {
		matched = a.matchAfterSync(ctx, hashed, token)
		if matched == nil {
			return nil, errInvalidAPIKey
		}
	}
	if err := a.confirmHeld(ctx, source, matched); err != nil {
		return nil, err
	}
	return a.userInfo(ctx, matched)
}

// matchFileKey returns the file key token is, or nil. The caller holds a.mu.
func (a *APIKeyAuthenticator) matchFileKey(token string) *APIKey {
	candidate, ok := a.fileKeys[token]
	if !ok || subtle.ConstantTimeCompare([]byte(candidate.Key), []byte(token)) != 1 {
		return nil
	}
	return candidate
}

// matchHashedKey returns the key in keys whose hash token matches, or nil.
func matchHashedKey(keys []*APIKey, token string) *APIKey {
	for _, k := range keys {
		if bcrypt.CompareHashAndPassword([]byte(k.KeyHash), []byte(token)) == nil {
			return k
		}
	}
	return nil
}

// matchAfterSync is the second look for a token no held key matched: it
// re-reads the store and compares the token with the keys that read added.
// Only a token shaped like a key the admin API generates can be one of those,
// so any other token (a JWT another authenticator refused, a config-file key
// mistyped) never reaches the store.
func (a *APIKeyAuthenticator) matchAfterSync(ctx context.Context, seen []*APIKey, token string) *APIKey {
	if !isGeneratedKeyShape(token) {
		return nil
	}
	if err := a.syncShared(ctx); err != nil {
		slog.Warn("api key: re-reading the key store for an unrecognized key failed", "error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	seenHashes := make(map[string]bool, len(seen))
	for _, k := range seen {
		seenHashes[k.KeyHash] = true
	}
	a.mu.RLock()
	added := make([]*APIKey, 0, len(a.hashedKeys))
	for _, k := range a.hashedKeys {
		if !seenHashes[k.KeyHash] {
			added = append(added, k)
		}
	}
	a.mu.RUnlock()
	return matchHashedKey(added, token)
}

// syncShared re-reads the store once for every caller waiting on it at the same
// moment, so a burst of refused keys costs one query rather than one each.
func (a *APIKeyAuthenticator) syncShared(ctx context.Context) error {
	_, err, _ := a.syncs.Do("sync", func() (any, error) {
		syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hashedKeySyncTimeout)
		defer cancel()
		return nil, a.SyncHashedKeys(syncCtx)
	})
	return err //nolint:wrapcheck // SyncHashedKeys wraps its own error
}

// confirmHeld refuses a matched key the store no longer holds, and drops it
// from memory so the next request with it is refused without a comparison. A
// store that cannot answer refuses the key too: a deleted key must not be
// accepted because the one place that records its deletion was unreachable.
func (a *APIKeyAuthenticator) confirmHeld(ctx context.Context, source HashedKeySource, key *APIKey) error {
	held, err := source.HoldsKey(ctx, key.Name, key.KeyHash)
	if err != nil {
		slog.Warn("api key: confirming a key against the key store failed",
			"name", logsan.SanitizeForLog(key.Name), "error", logsan.SanitizeForLog(err.Error()))
		return fmt.Errorf("api key %q could not be confirmed against the key store: %w", key.Name, err)
	}
	if !held {
		a.dropHashedKey(key)
		return errInvalidAPIKey
	}
	return nil
}

// dropHashedKey removes the entry key points at, if memory still holds it.
func (a *APIKeyAuthenticator) dropHashedKey(key *APIKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hashedKeys = slices.DeleteFunc(a.hashedKeys, func(k *APIKey) bool { return k == key })
}

// isGeneratedKeyShape reports whether token has the form GenerateKey produces:
// the hex encoding of generatedKeyBytes random bytes.
func isGeneratedKeyShape(token string) bool {
	if len(token) != hex.EncodedLen(generatedKeyBytes) {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

// keyUserInfo is the standalone identity a key bound to nobody authenticates
// as. Expiry is checked by userInfo, which is the only caller.
func keyUserInfo(key *APIKey) *middleware.UserInfo {
	return &middleware.UserInfo{
		UserID:   "apikey:" + key.Name,
		Email:    apiKeyEmail(*key),
		Claims:   make(map[string]any),
		Roles:    key.Roles,
		AuthType: middleware.AuthTypeAPIKey,
	}
}

// SetHashedKeySource attaches the store the database-managed keys live in. It
// may be called while requests are being authenticated.
func (a *APIKeyAuthenticator) SetHashedKeySource(source HashedKeySource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.source = source
}

// SyncHashedKeys replaces the database-managed keys held in memory with the
// ones the attached store holds. With no store attached it changes nothing:
// the keys held are all there are.
func (a *APIKeyAuthenticator) SyncHashedKeys(ctx context.Context) error {
	a.mu.RLock()
	source := a.source
	a.mu.RUnlock()
	if source == nil {
		return nil
	}
	keys, err := source.HashedKeys(ctx)
	if err != nil {
		return fmt.Errorf("reading api keys from the key store: %w", err)
	}
	a.ReplaceHashedKeys(keys)
	return nil
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

// ReplaceHashedKeys atomically replaces the full set of DB-loaded
// (bcrypt-hashed) keys. Used to reconcile the in-memory key set after an
// admin add or revoke, including across replicas (issue #501): unlike
// AddHashedKey, this DROPS keys no longer present in the new set, so a
// revoked key stops authenticating. File-config keys (fileKeys) are
// untouched.
func (a *APIKeyAuthenticator) ReplaceHashedKeys(keys []APIKey) {
	cleaned := make([]*APIKey, 0, len(keys))
	for i := range keys {
		k := keys[i]
		k.Key = "" // guard: hashed keys must not carry plaintext
		cleaned = append(cleaned, &k)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hashedKeys = cleaned
}

// RemoveKey removes a plaintext API key by its value.
func (a *APIKeyAuthenticator) RemoveKey(keyValue string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fileKeys, keyValue)
}

// ListKeys returns summaries of all registered keys (never exposes key values).
// Keys that exist in both file config and the database are deduplicated and
// reported with Source "both".
func (a *APIKeyAuthenticator) ListKeys() []APIKeySummary {
	a.mu.RLock()
	defer a.mu.RUnlock()

	byName := make(map[string]APIKeySummary, len(a.fileKeys)+len(a.hashedKeys))
	for _, k := range a.fileKeys {
		byName[k.Name] = APIKeySummary{
			Name:        k.Name,
			Email:       apiKeyEmail(*k),
			Description: k.Description,
			Roles:       k.Roles,
			ExpiresAt:   k.ExpiresAt,
			Expired:     k.IsExpired(),
			Source:      sourceFile,
			UserEmail:   k.UserEmail,
		}
	}
	for _, k := range a.hashedKeys {
		if existing, ok := byName[k.Name]; ok {
			existing.Source = sourceBoth
			byName[k.Name] = existing
		} else {
			byName[k.Name] = APIKeySummary{
				Name:        k.Name,
				Email:       apiKeyEmail(*k),
				Description: k.Description,
				Roles:       k.Roles,
				ExpiresAt:   k.ExpiresAt,
				Expired:     k.IsExpired(),
				Source:      sourceDatabase,
				UserEmail:   k.UserEmail,
			}
		}
	}

	summaries := make([]APIKeySummary, 0, len(byName))
	for _, s := range byName {
		// Roles is a list in the published contract, so a key carrying none
		// is listed as an empty one. A bound key deliberately holds none (it
		// follows its person), and a reader that walked a null here would
		// fail on exactly the key this exists to serve.
		if s.Roles == nil {
			s.Roles = []string{}
		}
		summaries = append(summaries, s)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})
	return summaries
}

// GenerateKey returns a new server-generated value for a key named def.Name,
// shown only once, or an error when a key already held carries that name. It
// holds nothing: the caller stores the key's hash, and the key authenticates
// once it is in the store and reaches memory through SyncHashedKeys. A value
// held in plaintext here as well would be a second copy that deleting the
// stored key does not remove.
func (a *APIKeyAuthenticator) GenerateKey(def APIKey) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Check for duplicate name across both collections.
	for _, v := range a.fileKeys {
		if v.Name == def.Name {
			return "", fmt.Errorf("api key name %q is already held: %w", def.Name, ErrKeyNameTaken)
		}
	}
	for _, v := range a.hashedKeys {
		if v.Name == def.Name {
			return "", fmt.Errorf("api key name %q is already held: %w", def.Name, ErrKeyNameTaken)
		}
	}

	// Generate random key
	b := make([]byte, generatedKeyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// SyntheticEmailDomain is the domain the platform mints an address in for an
// API key that configured none. It is an identity, not a mailbox: nothing
// resolves it, so anything that would deliver to an address in this domain must
// recognize it and decline (#1345). Exported so the sites that mint one and the
// notification path that refuses one cannot drift apart.
const SyntheticEmailDomain = "apikey.local"

// SyntheticEmail builds the address for an API key that configured none.
func SyntheticEmail(name string) string {
	return name + "@" + SyntheticEmailDomain
}

// apiKeyEmail returns the email for an API key, falling back to the synthetic
// address built from its name.
func apiKeyEmail(key APIKey) string {
	if key.Email != "" {
		return key.Email
	}
	return SyntheticEmail(key.Name)
}

// Verify interface compliance.
var _ middleware.Authenticator = (*APIKeyAuthenticator)(nil)
