// Package session provides session management for the MCP data platform.
// It defines the Store interface for session persistence and the Session type
// that represents an active client connection.
package session

import (
	"context"
	"time"
)

// Session represents an active client session.
type Session struct {
	// ID is the unique session identifier.
	ID string

	// UserID identifies the session owner. For authenticated sessions this is
	// a hash of the bearer token; for anonymous sessions it is empty.
	UserID string

	// CreatedAt is when the session was established.
	CreatedAt time.Time

	// LastActiveAt is the most recent activity timestamp.
	LastActiveAt time.Time

	// ExpiresAt is when the session expires if not touched.
	ExpiresAt time.Time

	// State holds extensible session data (e.g. enrichment dedup state).
	State map[string]any
}

// Store defines the interface for session persistence.
type Store interface {
	// Create persists a new session.
	Create(ctx context.Context, s *Session) error

	// Get retrieves a session by ID. Returns nil, nil if not found or expired.
	Get(ctx context.Context, id string) (*Session, error)

	// Touch updates LastActiveAt and extends ExpiresAt by the store's TTL.
	Touch(ctx context.Context, id string) error

	// Delete removes a session.
	Delete(ctx context.Context, id string) error

	// List returns all non-expired sessions.
	List(ctx context.Context) ([]*Session, error)

	// UpdateState merges state into the session's State map.
	UpdateState(ctx context.Context, id string, state map[string]any) error

	// Cleanup removes expired sessions.
	Cleanup(ctx context.Context) error

	// Close stops background routines and releases resources.
	Close() error
}
