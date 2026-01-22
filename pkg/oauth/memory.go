package oauth

import (
	"context"
	"errors"
	"sync"
	"time"
)

// MemoryStorage is an in-memory implementation of Storage.
// It is thread-safe and suitable for development/testing.
// For production, use PostgresStorage.
type MemoryStorage struct {
	mu            sync.RWMutex
	clients       map[string]*Client
	codes         map[string]*AuthorizationCode
	refreshTokens map[string]*RefreshToken
}

// NewMemoryStorage creates a new in-memory storage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		clients:       make(map[string]*Client),
		codes:         make(map[string]*AuthorizationCode),
		refreshTokens: make(map[string]*RefreshToken),
	}
}

// CreateClient stores a new client.
func (m *MemoryStorage) CreateClient(_ context.Context, client *Client) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clients[client.ClientID]; exists {
		return errors.New("client already exists")
	}
	m.clients[client.ClientID] = client
	return nil
}

// GetClient retrieves a client by ID.
func (m *MemoryStorage) GetClient(_ context.Context, clientID string) (*Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, ok := m.clients[clientID]
	if !ok {
		return nil, errors.New("client not found")
	}
	return client, nil
}

// UpdateClient updates an existing client.
func (m *MemoryStorage) UpdateClient(_ context.Context, client *Client) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clients[client.ClientID]; !exists {
		return errors.New("client not found")
	}
	m.clients[client.ClientID] = client
	return nil
}

// DeleteClient deletes a client.
func (m *MemoryStorage) DeleteClient(_ context.Context, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.clients, clientID)
	return nil
}

// ListClients returns all clients.
func (m *MemoryStorage) ListClients(_ context.Context) ([]*Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	clients := make([]*Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	return clients, nil
}

// SaveAuthorizationCode stores an authorization code.
func (m *MemoryStorage) SaveAuthorizationCode(_ context.Context, code *AuthorizationCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.codes[code.Code] = code
	return nil
}

// GetAuthorizationCode retrieves an authorization code.
func (m *MemoryStorage) GetAuthorizationCode(_ context.Context, code string) (*AuthorizationCode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	authCode, ok := m.codes[code]
	if !ok {
		return nil, errors.New("authorization code not found")
	}
	return authCode, nil
}

// DeleteAuthorizationCode deletes an authorization code.
func (m *MemoryStorage) DeleteAuthorizationCode(_ context.Context, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.codes, code)
	return nil
}

// CleanupExpiredCodes removes expired authorization codes.
func (m *MemoryStorage) CleanupExpiredCodes(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for code, authCode := range m.codes {
		if authCode.ExpiresAt.Before(now) {
			delete(m.codes, code)
		}
	}
	return nil
}

// SaveRefreshToken stores a refresh token.
func (m *MemoryStorage) SaveRefreshToken(_ context.Context, token *RefreshToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.refreshTokens[token.Token] = token
	return nil
}

// GetRefreshToken retrieves a refresh token.
func (m *MemoryStorage) GetRefreshToken(_ context.Context, token string) (*RefreshToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	refreshToken, ok := m.refreshTokens[token]
	if !ok {
		return nil, errors.New("refresh token not found")
	}
	return refreshToken, nil
}

// DeleteRefreshToken deletes a refresh token.
func (m *MemoryStorage) DeleteRefreshToken(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.refreshTokens, token)
	return nil
}

// DeleteRefreshTokensForClient deletes all refresh tokens for a client.
func (m *MemoryStorage) DeleteRefreshTokensForClient(_ context.Context, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for token, rt := range m.refreshTokens {
		if rt.ClientID == clientID {
			delete(m.refreshTokens, token)
		}
	}
	return nil
}

// CleanupExpiredTokens removes expired refresh tokens.
func (m *MemoryStorage) CleanupExpiredTokens(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for token, rt := range m.refreshTokens {
		if rt.ExpiresAt.Before(now) {
			delete(m.refreshTokens, token)
		}
	}
	return nil
}

// Verify MemoryStorage implements Storage interface.
var _ Storage = (*MemoryStorage)(nil)
