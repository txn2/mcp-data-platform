package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Explicit session handle constants (issue #792). A handle is minted by the
// platform_info tool and threaded by the model as the session_id argument on
// every subsequent tool call — the pattern the MCP 2026-07-28 spec recommends
// after removing the protocol-level session and the Mcp-Session-Id header
// (SEP-2567).
const (
	// HandlePrefix is the recognizable, unguessable prefix on every minted
	// handle. It distinguishes explicit handles from legacy transport session
	// IDs (bare hex) in logs and audit rows.
	HandlePrefix = "dps_"

	// PortalSessionPrefix is the recognizable prefix on a portal-initiated tool
	// run's session id (issue #859). Portal runs (the admin "Try It" / replay
	// tool runner) drive a fresh in-memory MCP session per HTTP request that has
	// no transport session id; assigning a prefixed id keeps their audit,
	// search-first gate, provenance, and dedup state attributable and isolated
	// from the operator's own agent session. It is deliberately distinct from
	// HandlePrefix so IsHandle never matches a portal id (a portal run mints no
	// platform_info handle) and the prefix distinguishes the two populations in
	// audit rows and logs.
	PortalSessionPrefix = "dpp_"

	// handleRandomBytes is the number of random bytes in a handle (128 bits).
	handleRandomBytes = 16

	// MintedByPlatformInfo marks a session row as an explicit handle minted by
	// platform_info, distinguishing it from a transport session created by the
	// AwareHandler initialize path.
	MintedByPlatformInfo = "platform_info"

	// StateKeyMintedBy is the session State key holding the mint marker.
	StateKeyMintedBy = "minted_by"

	// StateKeyPersona is the session State key holding the persona the handle
	// was minted under, for session-scoped analysis.
	StateKeyPersona = "persona"
)

// randomPrefixedID returns prefix followed by 128 bits of cryptographically
// random hex. It is the single entropy source behind both GenerateHandle and
// GeneratePortalSessionID, so hardening the scheme (entropy size, encoding)
// updates every session-identity kind at once rather than letting them drift.
// purpose names the id kind for the error message.
func randomPrefixedID(prefix, purpose string) (string, error) {
	b := make([]byte, handleRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating %s: %w", purpose, err)
	}
	return prefix + hex.EncodeToString(b), nil
}

// GenerateHandle returns a new explicit session handle: the HandlePrefix
// followed by 128 bits of cryptographically random hex.
func GenerateHandle() (string, error) {
	return randomPrefixedID(HandlePrefix, "session handle")
}

// IsHandle reports whether id has the explicit-handle prefix. It is a cheap
// syntactic check, not a validity check: a well-formed handle may still be
// expired or unknown to the store.
func IsHandle(id string) bool {
	return strings.HasPrefix(id, HandlePrefix)
}

// GeneratePortalSessionID returns a new portal-run session id: the
// PortalSessionPrefix followed by 128 bits of cryptographically random hex.
// It shares GenerateHandle's entropy but not its prefix, so a portal id is
// never mistaken for a platform_info handle (see PortalSessionPrefix). Unlike
// MintHandle it persists nothing: a portal run needs only an attributable,
// isolated session identity for the life of one HTTP request, not a durable
// row an agent threads across calls.
func GeneratePortalSessionID() (string, error) {
	return randomPrefixedID(PortalSessionPrefix, "portal session id")
}

// MintHandle generates a new handle and persists a session for it, owned by
// userID and marked as minted by platform_info, expiring ttl from now. It
// returns the stored session so callers can surface its ID and expiry (#792).
func MintHandle(ctx context.Context, store Store, userID, persona string, ttl time.Duration) (*Session, error) {
	handle, err := GenerateHandle()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sess := &Session{
		ID:           handle,
		UserID:       userID,
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    now.Add(ttl),
		State: map[string]any{
			StateKeyMintedBy: MintedByPlatformInfo,
			StateKeyPersona:  persona,
		},
	}
	if err := store.Create(ctx, sess); err != nil {
		return nil, fmt.Errorf("persisting session handle: %w", err)
	}
	return sess, nil
}
