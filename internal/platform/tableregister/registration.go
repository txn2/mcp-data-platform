// Package tableregister makes a file already in object storage readable as a
// table, without copying it and without giving anything a write tool.
//
// A managed resource and a portal asset are both stored as one object under a
// per-object directory. Trino's Hive connector reads CSV from an external
// location, so "make this file queryable" is a CREATE TABLE naming that
// directory: no ingestion, no copy, and the table tracks whatever the object
// holds now. One registrar serves both kinds because a registration says the
// same thing about either.
//
// What the registrar does NOT do is decide who may register. Every entry point
// -- the resources REST API, the asset REST API, the manage_table tool --
// resolves its own caller and hands one in; the registrar applies the persona
// connection boundary to that caller and refuses on anything it cannot
// establish.
package tableregister

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Source kinds a registration can be built from. They are the two things a
// person can put a file into the platform through.
const (
	// KindResource is a managed resource: a file a person uploaded.
	KindResource = "resource"
	// KindAsset is a portal asset: a file the platform wrote, typically a
	// trino_export or a script's output.
	KindAsset = "asset"
)

// Column is one column of a registered table.
//
// Type is recorded even though Hive CSV admits exactly one: a reader of the
// record should not have to know the connector's rule to know what a query
// will get back, and a stored type is what a later format would vary.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Registration records that a source object's directory is readable as a table
// on a connection.
type Registration struct {
	ID           string    `json:"id"`
	SourceKind   string    `json:"source_kind"`
	SourceID     string    `json:"source_id"`
	Connection   string    `json:"connection"`
	Catalog      string    `json:"catalog"`
	Schema       string    `json:"schema"`
	Table        string    `json:"table"`
	Location     string    `json:"location"`
	Columns      []Column  `json:"columns"`
	RegisteredBy string    `json:"registered_by"`
	RegisteredAt time.Time `json:"registered_at"`
}

// QualifiedName is the table as a query names it.
func (r Registration) QualifiedName() string {
	return r.Catalog + "." + r.Schema + "." + r.Table
}

// IsStale reports whether the registration still points at the source's
// current content.
//
// A resource revision and an asset version both write a new object under a new
// directory and move the head key to it. The table keeps serving the directory
// it was registered against, which is the revision that was current then --
// correct SQL over the wrong bytes, and nothing about the table says so. This
// compares the recorded location against the directory of the head key the
// source carries now; re-registering targets the current head.
//
// An overwrite in place is not staleness: replacing the object at the same key
// changes what the table returns on the next query, with no re-registration,
// which is what makes a repeating vendor drop a re-upload rather than a chore.
func (r Registration) IsStale(bucket, currentHeadKey string) bool {
	dir := DirectoryOf(currentHeadKey)
	if dir == "" {
		return true
	}
	return r.Location != LocationURI(bucket, dir)
}

// LocationURI renders the external location a directory in a bucket is
// addressed by.
func LocationURI(bucket, dir string) string {
	return "s3://" + bucket + "/" + dir
}

// DirectoryOf returns the directory portion of an object key, with its
// trailing slash. A key with no directory yields the empty string, which no
// registration can be built on.
func DirectoryOf(key string) string {
	idx := strings.LastIndex(key, "/")
	if idx < 0 {
		return ""
	}
	return key[:idx+1]
}

// Store persists registrations.
//
// Insert is expected to fail when the name is already claimed; the registrar
// turns that into a refusal naming the holder rather than silently replacing a
// table someone else registered.
type Store interface {
	Insert(ctx context.Context, r Registration) error
	Get(ctx context.Context, id string) (*Registration, error)
	// ByName returns the registration holding a name on a connection, or nil
	// when the name is free.
	ByName(ctx context.Context, connection, catalog, schema, table string) (*Registration, error)
	// BySource returns every registration of one resource or asset.
	BySource(ctx context.Context, kind, sourceID string) ([]Registration, error)
	// ForSources returns the registrations of many sources of one kind, keyed
	// by source id. It is the read a list view and a search result set use, so
	// a page of hits costs one query rather than one per hit.
	ForSources(ctx context.Context, kind string, sourceIDs []string) (map[string][]Registration, error)
	Delete(ctx context.Context, id string) error
}

// Errors the registrar returns. Every surface renders these, so the wording a
// person sees comes from one place.
var (
	// ErrNotFound is returned for a registration id that does not exist.
	ErrNotFound = errors.New("registration not found")

	// ErrNoScratchTarget means the connection has no scratch: block, so
	// nothing can be registered on it.
	ErrNoScratchTarget = errors.New("this connection has no scratch catalog and schema configured, so a table cannot be registered on it")

	// ErrConnectionDenied means the caller's persona is not granted the
	// connection. It is the same boundary a tool call meets.
	ErrConnectionDenied = errors.New("your persona is not granted this connection")

	// ErrNotCSV means the source object is not a CSV, which is the only format
	// a registration can be built from.
	ErrNotCSV = errors.New("only a CSV file can be registered as a table")

	// ErrEmptyHeader means the object had no header row to take columns from.
	ErrEmptyHeader = errors.New("the file has no header row, so the table has no column names")

	// ErrNoIdentity means the call carried no identity to register under.
	// Registration records who made it and decides replacement on that, so an
	// anonymous registration would be one nobody owns and anyone could take
	// over.
	ErrNoIdentity = errors.New("registering a table needs a signed-in identity")

	// ErrBadReference means the reference a caller passed is not one this
	// platform issues, or names something that is not a stored file. It is
	// separate from ErrNoSuchFile because the caller can see the difference
	// themselves: the string they sent is malformed, so telling them so
	// discloses nothing they did not already have.
	ErrBadReference = errors.New("not a reference to a stored file")

	// ErrNoSuchFile is the one answer to a reference that resolves to a record
	// this caller may not register: one that does not exist, one that was
	// deleted, and one that exists but belongs to somebody else are answered
	// identically, so the surface never confirms the existence of a record the
	// caller cannot act on. It is what `fetch` does with a reference outside
	// the caller's reach, held to here for the same reason.
	ErrNoSuchFile = errors.New("that reference names no stored file you can register")

	// ErrRefused marks a refusal the caller can act on -- a name already
	// taken, a sibling object in the way -- as opposed to a failure of the
	// platform. Every such refusal wraps it, so a surface can answer with a
	// status that says "your request was understood and declined" rather than
	// reporting a store outage as a conflict, or the reverse.
	ErrRefused = errors.New("registration refused")
)

// refusedf builds a caller-actionable refusal carrying ErrRefused.
//
// The sentinel's own text is dropped from the rendering: it exists for
// errors.Is, and prefixing every message with "registration refused:" would
// put a label in front of the sentence that already says what to do.
func refusedf(format string, args ...any) error {
	return &refusal{reason: fmt.Sprintf(format, args...)}
}

// refusal is a caller-actionable refusal that reads as its own sentence and
// still answers to ErrRefused.
type refusal struct{ reason string }

func (e *refusal) Error() string { return e.reason }

// Is reports the sentinel every surface matches on.
func (*refusal) Is(target error) bool { return target == ErrRefused }
