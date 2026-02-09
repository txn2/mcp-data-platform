package session

import (
	"context"
	"maps"
	"sync"
	"time"
)

// MemoryStore implements Store using an in-memory map with TTL-based expiration.
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration

	cancel context.CancelFunc
	done   chan struct{}
}

// NewMemoryStore creates a new in-memory session store.
func NewMemoryStore(ttl time.Duration) *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]*Session),
		ttl:      ttl,
	}
}

// Create persists a new session.
func (s *MemoryStore) Create(_ context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[sess.ID] = sess
	return nil
}

// Get retrieves a session by ID. Returns nil, nil if not found or expired.
func (s *MemoryStore) Get(_ context.Context, id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil, nil //nolint:nilnil // Store interface specifies nil,nil for not-found
	}
	if time.Now().After(sess.ExpiresAt) {
		return nil, nil //nolint:nilnil // Store interface specifies nil,nil for expired
	}
	return sess, nil
}

// Touch updates LastActiveAt and extends ExpiresAt by the store's TTL.
func (s *MemoryStore) Touch(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil
	}

	now := time.Now()
	sess.LastActiveAt = now
	sess.ExpiresAt = now.Add(s.ttl)
	return nil
}

// Delete removes a session.
func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, id)
	return nil
}

// List returns all non-expired sessions.
func (s *MemoryStore) List(_ context.Context) ([]*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	result := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		if now.Before(sess.ExpiresAt) {
			result = append(result, sess)
		}
	}
	return result, nil
}

// UpdateState merges state into the session's State map.
func (s *MemoryStore) UpdateState(_ context.Context, id string, state map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil
	}
	if sess.State == nil {
		sess.State = make(map[string]any)
	}
	maps.Copy(sess.State, state)
	return nil
}

// Cleanup removes expired sessions.
func (s *MemoryStore) Cleanup(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
			delete(s.sessions, id)
		}
	}
	return nil
}

// StartCleanupRoutine starts a background goroutine that periodically removes
// expired sessions. The goroutine is stopped when Close is called.
func (s *MemoryStore) StartCleanupRoutine(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})

	go func() {
		defer close(s.done)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.Cleanup(ctx)
			}
		}
	}()
}

// Close stops the cleanup goroutine and waits for it to exit.
// It is safe to call Close even if StartCleanupRoutine was never called.
func (s *MemoryStore) Close() error {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	return nil
}

// Verify interface compliance.
var _ Store = (*MemoryStore)(nil)
