package prompt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// maxCollectionNameLength and maxCollectionDescriptionLength bound a
// collection's display name and description.
const (
	maxCollectionNameLength        = 128
	maxCollectionDescriptionLength = 2000
)

// ErrCollectionExists rejects a create or rename that collides with an
// existing collection name (names are an org-wide vocabulary, unique
// case-insensitively). REST handlers map it to 409.
var ErrCollectionExists = errors.New("a collection with that name already exists")

// ErrCollectionNotFound rejects an assignment to a collection that does not
// exist (e.g. deleted between listing and assigning). REST handlers map it to
// 404.
var ErrCollectionNotFound = errors.New("collection not found")

// Collection is a named group organizing the prompt library by team, domain,
// or workflow (#1010). Collections are visible to every portal user; a prompt
// belongs to at most one collection.
type Collection struct {
	ID          string    `json:"id" example:"col_a1b2c3d4"`
	Name        string    `json:"name" example:"Sales Reporting"`
	Description string    `json:"description" example:"Weekly and daily sales SOPs"`
	CreatedBy   string    `json:"created_by" example:"jane@example.com"`
	PromptCount int       `json:"prompt_count" example:"7"`
	CreatedAt   time.Time `json:"created_at" example:"2026-07-01T14:30:00Z"`
	UpdatedAt   time.Time `json:"updated_at" example:"2026-07-01T14:30:00Z"`
}

// ValidateCollectionName checks that a collection name is present and bounded.
// Collection names are free-text display names (unlike prompt names, which are
// invocation identifiers), so only presence and length are enforced.
func ValidateCollectionName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("collection name is required")
	}
	if len(name) > maxCollectionNameLength {
		return fmt.Errorf("collection name must be at most %d characters", maxCollectionNameLength)
	}
	return nil
}

// ValidateCollectionDescription bounds a collection description.
func ValidateCollectionDescription(desc string) error {
	if len(desc) > maxCollectionDescriptionLength {
		return fmt.Errorf("collection description must be at most %d characters", maxCollectionDescriptionLength)
	}
	return nil
}

// CollectionStore is the optional collection capability of a prompt store.
// The PostgreSQL store implements it; deployments without a database have no
// collections and the REST routes are not mounted.
type CollectionStore interface {
	// CreateCollection persists a new collection, generating its ID. Returns
	// ErrCollectionExists on a name collision.
	CreateCollection(ctx context.Context, c *Collection) error

	// GetCollection retrieves a collection by ID. Returns nil, nil if not found.
	GetCollection(ctx context.Context, id string) (*Collection, error)

	// ListCollections returns every collection with its prompt count, ordered
	// by name.
	ListCollections(ctx context.Context) ([]Collection, error)

	// UpdateCollection renames or re-describes a collection. Returns
	// ErrCollectionExists on a name collision.
	UpdateCollection(ctx context.Context, id, name, description string) error

	// DeleteCollection removes a collection, releasing its prompts to the
	// default (uncollected) group.
	DeleteCollection(ctx context.Context, id string) error

	// SetPromptCollection assigns a prompt to a collection, or clears the
	// assignment when collectionID is empty. It touches only the assignment:
	// no version snapshot is produced and the review gate is not involved
	// (placement is not reviewable substance).
	SetPromptCollection(ctx context.Context, promptID, collectionID string) error
}

// CollectionProvider exposes the collection capability through store
// decorators that would otherwise hide it from a type assertion. The
// promptlayer notifying wrapper implements it by delegating to the wrapped
// store; the composition root resolves the capability with AsCollectionStore.
type CollectionProvider interface {
	// Collections returns the underlying collection capability, or nil when
	// the backing store does not support collections.
	Collections() CollectionStore
}

// AsCollectionStore resolves the collection capability from a prompt store,
// looking through any decorator that implements CollectionProvider. Returns
// nil when the store has no collection support.
func AsCollectionStore(store Store) CollectionStore {
	if cs, ok := store.(CollectionStore); ok {
		return cs
	}
	if cp, ok := store.(CollectionProvider); ok {
		return cp.Collections()
	}
	return nil
}
