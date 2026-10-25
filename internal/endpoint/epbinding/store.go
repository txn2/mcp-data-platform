package epbinding

import (
	"context"
	"time"
)

// Filter narrows a listing. A non-nil empty ScriptIDs matches nothing. An
// empty AuthMode and a nil Enabled match every binding. Limit is a page's
// size, bounded by the store, and Offset how many to pass over (#2251).
type Filter struct {
	ScriptIDs []string
	AuthMode  string
	Enabled   *bool
	Limit     int
	Offset    int
}

// Failure is one request that failed, kept for the script's owner to read.
type Failure struct {
	ID         string    `json:"id"`
	EndpointID string    `json:"endpoint_id"`
	Version    int       `json:"version"`
	Method     string    `json:"method"`
	Status     int       `json:"status"`
	Error      string    `json:"error"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// FailureRetention is how long a failed request is kept.
const FailureRetention = 7 * 24 * time.Hour

// Store keeps bindings and the failed requests they answered.
type Store interface {
	// Set creates or replaces sc's binding, keyed by its script; a name
	// another script's binding has is ErrNameTaken.
	Set(ctx context.Context, b *Binding) error
	// Get is a script's binding, ErrNotFound when it has none.
	Get(ctx context.Context, scriptID string) (*Binding, error)
	// GetByName is the binding a name serves, ErrNotFound when none does.
	GetByName(ctx context.Context, name string) (*Binding, error)
	// List is one page of the bindings filter matches, by name, and how
	// many it matches in all.
	List(ctx context.Context, filter Filter) (page []Binding, total int, err error)
	// SetEnabled turns a script's binding on or off.
	SetEnabled(ctx context.Context, scriptID string, enabled bool, actor string) error
	// RecordFailure keeps one failed request.
	RecordFailure(ctx context.Context, f Failure) error
	// Failures is an endpoint's most recent failed requests, newest first.
	Failures(ctx context.Context, endpointID string, limit int) ([]Failure, error)
	// SweepFailures deletes the failures recorded before cutoff.
	SweepFailures(ctx context.Context, cutoff time.Time) (int64, error)
	// Stamp is when the binding a name serves, and the script it binds, were
	// last written: one read that tells a replica whether what it compiled
	// for the name is still current. ErrNotFound when no binding has it.
	Stamp(ctx context.Context, name string) (Stamp, error)
}

// Stamp is when a binding and its script were last written. Every write of a
// script row moves its updated_at, a new version included, so two equal
// stamps mean nothing a request runs has changed.
type Stamp struct {
	Binding time.Time
	Script  time.Time
}
