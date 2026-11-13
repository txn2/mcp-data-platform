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
	// Set creates a binding when its ID is empty and replaces the one with its
	// ID otherwise, which must be one of its script's; a name another binding
	// has is ErrNameTaken. A script may hold several bindings (#2441).
	Set(ctx context.Context, b *Binding) error
	// GetByID is one binding, ErrNotFound when none has the id.
	GetByID(ctx context.Context, id string) (*Binding, error)
	// ByScript is a script's bindings by name, empty when it has none.
	ByScript(ctx context.Context, scriptID string) ([]Binding, error)
	// GetByName is the binding a name serves, ErrNotFound when none does.
	GetByName(ctx context.Context, name string) (*Binding, error)
	// List is one page of the bindings filter matches, by name, and how
	// many it matches in all.
	List(ctx context.Context, filter Filter) (page []Binding, total int, err error)
	// Enable turns one binding on or off.
	Enable(ctx context.Context, id string, enabled bool, actor string) error
	// Delete removes one binding with its failed and refused requests.
	Delete(ctx context.Context, id string) error
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

// Stamp is when a binding and its script were last written, and the version
// of a form approved to be served to callers with no session (#2494). Every
// write of a script row moves its updated_at, a new version included, and an
// approval moves ApprovedVersion, so two equal stamps mean nothing a request
// runs has changed.
type Stamp struct {
	Binding time.Time
	Script  time.Time
	// ApprovedVersion is the highest version approved for form_public, 0
	// for none.
	ApprovedVersion int
	// Application is the id of the application the script is a part of and
	// AppConnection the connection that application's schema is on, both ""
	// for none (#2539). Read with the stamp, so a request learns the run role
	// its statements log in as with no query of its own.
	Application, AppConnection string
}
