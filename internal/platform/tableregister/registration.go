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
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/tablecsv"
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

// Column is one column a registered table declares; see tablecsv.Column.
type Column = tablecsv.Column

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
	// Follow means the registration is moved to the source's new head
	// directory when a revision or version is written (#1536). It is what a
	// registration gets unless the caller pins it: a person or an agent that
	// replaces a file expects the table over it to read the new contents.
	// Pinned -- follow off -- is the choice for a report that must keep
	// returning the rows it was registered over until somebody decides
	// otherwise.
	Follow bool `json:"follow"`
	// Repair means the registration corrects its file: a version of the source
	// carrying a defect a reader cannot see past, of a kind the platform can
	// correct, is saved corrected as the file's next version and the table is
	// moved onto that version (#1577). Off -- the default, and what a
	// registration made without asking gets -- such a version leaves the
	// registration where it was with the reason recorded on it, because nobody
	// asked for the file to be rewritten.
	//
	// It is on the record because the follow has nothing else to act on: the
	// choice was made once, at a registration, and the versions it applies to
	// arrive afterwards. It does nothing for a registration that is not
	// following, which never meets a new version at all.
	Repair bool `json:"repair"`
	// FollowError is why the last follow did not move the registration, and is
	// empty while it is where the file is. It is kept on the record because a
	// follow never fails the write that triggered it, so the listing has to be
	// able to say what is behind and why without the log of that write.
	FollowError string `json:"follow_error,omitempty"`
}

// Result is one completed registration: the record, the source it was built
// over, and what a correction of that source changed.
//
// Source is not always the source that was handed in. A file that had to be
// corrected first is registered over the version the correction wrote, and a
// surface reporting on the registration -- whether it is stale, which object
// it reads -- has to be looking at that version rather than the one it asked
// about.
type Result struct {
	Registration
	Source Source `json:"-"`
	// Correction is what saving a corrected version changed, or nil when the
	// file was registered exactly as it was stored.
	//
	// It is not called Repair. The embedded Registration carries a Repair of
	// its own -- the standing choice this registration was made with (#1577) --
	// and two fields under one name at two depths is a shadow in Go and a
	// dropped field in JSON: encoding/json keeps the shallower tag and emits
	// neither the report nor the choice.
	Correction *RepairReport `json:"correction,omitempty"`
	// Siblings is what a replacing registration found about the OTHER
	// registrations on the connection after its DROP ran (#1546): one outcome
	// per table that no longer exists. Empty when every other table is still
	// there, and always empty for a registration that replaced nothing.
	Siblings []FollowOutcome `json:"siblings,omitempty"`
}

// RepairReport is what saving a corrected version of a file changed, which
// version it was saved as, and what that version did to the other tables
// registered over the file.
type RepairReport struct {
	tablecsv.NormalizeReport
	Version int `json:"version"`
	// Followed is what writing the corrected version did to every OTHER
	// registration over the file (#1536): a following one was moved onto it,
	// a pinned one is now behind it. The registration the correction was made
	// for is not among them, being current by construction.
	Followed []FollowOutcome `json:"followed,omitempty"`
}

// Summary renders the correction as the sentence a person whose file changed
// is told, in both surfaces, followed by what it did to the other tables over
// the file.
func (r *RepairReport) Summary() string {
	if r == nil {
		return ""
	}
	s := "Saved version " + strconv.Itoa(r.Version) + " of this file, which " + repairSummary(r.NormalizeReport) +
		". The file as it was uploaded is still there as the version before it."
	if lines := Sentences(r.Followed); len(lines) > 0 {
		s += " " + strings.Join(lines, " ")
	}
	return s
}

// repairSummary says what a correction did, in the terms the person who
// uploaded the file would use. It is also the change summary the version trail
// records, so the version panel and the registration message agree.
func repairSummary(report tablecsv.NormalizeReport) string {
	parts := make([]string, 0, 3)
	if report.FromLineEndings != "" {
		parts = append(parts, "rewrote the "+report.FromLineEndings+" line endings as newlines")
	}
	if report.RowsRepaired > 0 {
		parts = append(parts, "put "+plural(report.RowsRepaired, "row", "rows")+" back onto one line")
	}
	if report.FromEncoding != "" {
		parts = append(parts, "converted the text from "+report.FromEncoding+" to UTF-8")
	}
	if len(parts) == 0 {
		return "rewrote it as a plain UTF-8 CSV"
	}
	return tablecsv.JoinAnd(parts)
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
	// List returns a page of registrations across every source, newest first,
	// with the total the filter matched.
	//
	// It is the only read here that is not keyed by one source or one name,
	// which is why nothing could answer "what is registered on this platform"
	// before it: the scratch schema is shared, so a reader could query a table
	// through Trino that no surface would list (#1472).
	List(ctx context.Context, f Filter) ([]Registration, int, error)
	// Relocate moves a registration onto a new directory with the columns the
	// file there declares, and clears the failure of any earlier follow. It
	// is the store half of a follow (#1536): the table was already moved by
	// the DDL, and the row has to say what the table now reads.
	Relocate(ctx context.Context, id, location string, columns []Column) error
	// RecordFollowFailure keeps why a follow did not move a registration, so
	// a listing reports it behind the file with the reason.
	RecordFollowFailure(ctx context.Context, id, reason string) error
	Delete(ctx context.Context, id string) error
}

// Filter narrows a cross-source listing.
type Filter struct {
	// AllConnections lifts the connection boundary, which is what an
	// administrator gets.
	//
	// It is a flag rather than a nil Connections slice because the two states
	// it separates are opposites and both are reachable: a persona granted no
	// connection reaches nothing, and an administrator reaches everything.
	// Reading one as the other would either hide every table from an operator
	// or show every table to a persona that may query none of them.
	AllConnections bool
	// Connections is what the caller may see when AllConnections is not set.
	Connections []string
	// SourceKind limits the listing to KindResource or KindAsset. Empty spans
	// both, which is the point of the listing.
	SourceKind string
	// Query matches the qualified name -- catalog, schema and table -- without
	// regard to case.
	Query string
	// Limit and Offset page the result. A Limit at or below zero takes
	// DefaultListLimit, and one above MaxListLimit takes that: the caller of a
	// listing does not get to ask for the whole table.
	Limit  int
	Offset int
}

// The bounds a listing page is served within.
const (
	// DefaultListLimit is the page size a caller who names none gets.
	DefaultListLimit = 50
	// MaxListLimit is the largest page the store will build, whatever was
	// asked for.
	MaxListLimit = 200
)

// EffectiveLimit is the page size a filter resolves to.
func (f Filter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return DefaultListLimit
	}
	if f.Limit > MaxListLimit {
		return MaxListLimit
	}
	return f.Limit
}

// Errors the registrar returns. Every surface renders these, so the wording a
// person sees comes from one place.
var (
	// ErrNotFound is returned for a registration id that does not exist.
	ErrNotFound = errors.New("registration not found")

	// ErrNoScratchTarget means the connection has no scratch: block, so
	// nothing can be registered on it.
	ErrNoScratchTarget = errors.New("this connection has no scratch catalog and schema configured, so a table cannot be registered on it")

	// ErrConnectionReadOnly means the connection names a scratch target but
	// will not run the statement that creates the table.
	//
	// It is the sibling of ErrNoScratchTarget and is answered the same way: a
	// fact about the connection, knowable before the request, that no retry
	// and no different table name changes. Without it the refusal arrived from
	// the Trino interceptor as an unclassified error, which the HTTP surface
	// could only report as a 500 "the registration could not be completed" --
	// a configuration fact rendered as a platform outage, with the one word
	// that explains it ("read-only") dropped on the way out.
	ErrConnectionReadOnly = errors.New("this connection is read-only, so a table cannot be created on it; ask an administrator for a connection that accepts writes")

	// ErrConnectionDenied means the caller's persona is not granted the
	// connection. It is the same boundary a tool call meets.
	ErrConnectionDenied = errors.New("your persona is not granted this connection")

	// ErrNotCSV means the source object is not a CSV, which is the only format
	// a registration can be built from.
	ErrNotCSV = errors.New("only a CSV file can be registered as a table")

	// ErrEmptyHeader means the object had no header row to take columns from.
	ErrEmptyHeader = tablecsv.ErrEmptyHeader

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

	// ErrNeedsRepair marks the refusal of a file that cannot be read as a
	// table the way it is stored but could be if a corrected version of it
	// were saved first. Both surfaces match on it to offer that correction,
	// rather than leaving a person with a refusal and nothing to do about it.
	ErrNeedsRepair = errors.New("this file needs correcting before it can be read as a table")

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

// needsRepairf builds a refusal of a file the platform could register if it
// saved a corrected version of it first. It answers to ErrNeedsRepair as well
// as to ErrRefused, which is how a surface knows to offer the correction.
func needsRepairf(format string, args ...any) error {
	return &refusal{reason: fmt.Sprintf(format, args...), repairable: true}
}

// refusal is a caller-actionable refusal that reads as its own sentence and
// still answers to ErrRefused.
type refusal struct {
	reason string
	// repairable marks the subset the platform can offer a correction for.
	repairable bool
}

func (e *refusal) Error() string { return e.reason }

// Is reports the sentinels a surface matches on.
func (e *refusal) Is(target error) bool {
	return target == ErrRefused || (e.repairable && target == ErrNeedsRepair)
}

// plural renders a count with the form it reads with ("1 row", "2 rows").
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
