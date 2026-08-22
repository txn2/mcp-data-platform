package tableregister

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// Source is the object a registration is built over: where it lives, what it
// is, and who it belongs to. Each surface builds one from its own record, so
// the registrar never learns what a resource or an asset is.
type Source struct {
	Kind        string
	ID          string
	Name        string
	Bucket      string
	HeadKey     string
	ContentType string
	// OwnerID is who the source belongs to. It decides whether a caller may
	// replace an existing registration of the same name.
	OwnerID string
}

// Caller is who is asking. Persona drives the connection boundary and the
// table-name prefix; IsAdmin lifts the ownership check on replacing a
// registration.
type Caller struct {
	UserID  string
	Email   string
	Persona string
	Roles   []string
	IsAdmin bool
}

// Request is one registration.
type Request struct {
	Connection string
	// TableName is the caller's choice. Empty takes a slug of the source's
	// filename. Either way it is slugified and persona-prefixed.
	TableName string
	// Source names where the registration comes from: "portal" for a REST or
	// UI action, "mcp" for a tool call. It is recorded on the audit event.
	Source string
}

// Deps is what a Registrar needs.
type Deps struct {
	Store Store
	Trino Executor
	// Objects reads a source's bytes, keyed by source kind. It is per kind
	// because the two kinds do not have to share an object store: a
	// deployment names the portal's S3 connection and the managed-resources
	// one separately, and reading a resource through the portal's client
	// would look in the wrong bucket on any deployment that split them.
	Objects  map[string]ObjectReader
	Scope    ConnectionScope
	Audit    AuditLogger
	NewID    func() (string, error)
	MaxBytes int64
}

// DefaultMaxBytes bounds the object the registrar reads to find a header row.
//
// Neither S3 adapter has a range read, so learning the first line costs a full
// GetObject. The bound matches the managed-resource upload cap, which is the
// largest object either surface can have put there.
const DefaultMaxBytes = 100 << 20

// Log field names shared by this package's warnings.
const (
	logFieldError = "error"
	logFieldTable = "table"
)

// Registrar registers and unregisters tables over stored objects.
type Registrar struct {
	deps Deps
}

// New creates a Registrar. A nil Store or Trino executor makes every call
// report that registration is unavailable rather than panicking, which is the
// state of a deployment with no database or no Trino toolkit.
func New(deps Deps) *Registrar {
	if deps.MaxBytes <= 0 {
		deps.MaxBytes = DefaultMaxBytes
	}
	return &Registrar{deps: deps}
}

// ErrUnavailable means the deployment has no registration mechanism wired.
var ErrUnavailable = errors.New("table registration is not available on this deployment")

// Available reports whether registration is wired at all, so a surface can
// hide the action rather than offering one that always refuses.
func (r *Registrar) Available() bool {
	return r != nil && r.deps.Store != nil && r.deps.Trino != nil && len(r.deps.Objects) > 0
}

// objectsFor returns the reader for a source kind, or nil when the deployment
// has no store for it -- a platform with a portal but no managed resources
// configured, and the reverse.
func (r *Registrar) objectsFor(kind string) ObjectReader {
	return r.deps.Objects[kind]
}

// Register makes the source's directory readable as a table and records it.
//
// The order is deliberate: everything that can refuse does so before any
// statement runs, so a refused registration leaves nothing behind in Trino.
// The record is written last, because a row naming a table that was never
// created is worse than a table with no row -- the first is a lie a search hit
// repeats, the second is an object in a scratch schema.
func (r *Registrar) Register(ctx context.Context, caller Caller, src Source, req Request) (*Registration, error) {
	if !r.Available() {
		return nil, ErrUnavailable
	}
	if caller.Email == "" {
		return nil, ErrNoIdentity
	}

	reg, existing, err := r.plan(ctx, caller, src, req)
	if err != nil {
		return nil, err
	}

	ddl := BuildDDL(reg, existing != nil)
	execErr := r.runDDL(ctx, req.Connection, ddl)
	r.audit(ctx, auditRecord{caller: caller, reg: reg, ddl: ddl, source: req.Source, err: execErr})
	if execErr != nil {
		return nil, execErr
	}

	// The DROP already removed the replaced table, so its row is stale
	// whatever happens next; dropping it before the insert is also what frees
	// the unique name for this one.
	if existing != nil {
		if err := r.deps.Store.Delete(ctx, existing.ID); err != nil {
			return nil, fmt.Errorf("replacing the previous registration: %w", err)
		}
	}
	if err := r.deps.Store.Insert(ctx, reg); err != nil {
		return nil, fmt.Errorf("recording the registration: %w", err)
	}
	return &reg, nil
}

// plan works out everything a registration is made of and refuses on anything
// it cannot establish, before any statement runs. It returns the registration
// to create and the one it would replace, if any.
func (r *Registrar) plan(
	ctx context.Context, caller Caller, src Source, req Request,
) (reg Registration, existing *Registration, err error) {
	target, err := r.resolveTarget(caller, req.Connection)
	if err != nil {
		return reg, nil, err
	}
	location, err := r.locationFor(ctx, src)
	if err != nil {
		return reg, nil, err
	}
	columns, err := r.columnsFor(ctx, src)
	if err != nil {
		return reg, nil, err
	}

	table := tableNameFor(caller, src, req)
	if table == "" {
		return reg, nil, refusedf("a table name could not be derived; give one explicitly")
	}
	existing, err = r.deps.Store.ByName(ctx, req.Connection, target.Catalog, target.Schema, table)
	if err != nil {
		return reg, nil, fmt.Errorf("checking the table name: %w", err)
	}
	if err := mayReplace(caller, existing, target, table); err != nil {
		return reg, nil, err
	}

	reg = Registration{
		SourceKind:   src.Kind,
		SourceID:     src.ID,
		Connection:   req.Connection,
		Catalog:      target.Catalog,
		Schema:       target.Schema,
		Table:        table,
		Location:     location,
		Columns:      columns,
		RegisteredBy: caller.Email,
	}
	if reg.ID, err = r.newID(); err != nil {
		return reg, nil, err
	}
	return reg, existing, nil
}

// resolveTarget applies the connection boundary and finds where registrations
// land on that connection.
func (r *Registrar) resolveTarget(caller Caller, connection string) (trino.ScratchConfig, error) {
	if r.deps.Scope != nil && !r.deps.Scope.AllowConnection(caller.Persona, connection) {
		return trino.ScratchConfig{}, ErrConnectionDenied
	}
	target, ok := r.deps.Trino.ScratchTarget(connection)
	if !ok || !target.Configured() {
		return trino.ScratchConfig{}, ErrNoScratchTarget
	}
	return target, nil
}

// mayReplace decides whether this caller may take over a name someone already
// registered. A registration is replaced only by the person who made it or by
// an administrator; anyone else is told who holds it, because a shared schema
// where the last writer silently wins is a schema nobody can rely on.
func mayReplace(caller Caller, existing *Registration, target trino.ScratchConfig, table string) error {
	if existing == nil {
		return nil
	}
	if caller.IsAdmin || existing.RegisteredBy == caller.Email {
		return nil
	}
	return refusedf("%s.%s.%s is already registered by %s; choose another table name",
		target.Catalog, target.Schema, table, existing.RegisteredBy)
}

// locationFor returns the directory the external table points at, refusing a
// directory that holds anything besides the source object.
//
// The refusal is the whole protection. Hive reads every non-hidden object
// under an external location and parses it as CSV, so a stray file beside the
// content does not fail the query -- it comes back as rows. Thumbnails are
// written under hidden names for exactly this reason and so are invisible
// here; anything else is named back to the caller.
func (r *Registrar) locationFor(ctx context.Context, src Source) (string, error) {
	dir := DirectoryOf(src.HeadKey)
	if dir == "" {
		return "", refusedf("this file is not stored under a directory of its own, so no table can be pointed at it")
	}
	objects := r.objectsFor(src.Kind)
	if objects == nil {
		return "", ErrUnavailable
	}

	entries, truncated, err := objects.ListDirectory(ctx, src.Bucket, dir)
	if err != nil {
		return "", fmt.Errorf("listing the file's directory: %w", err)
	}
	if truncated {
		return "", refusedf("this file's directory holds more objects than can be checked, so it cannot be registered")
	}

	var siblings []string
	for _, e := range entries {
		if e.Key == src.HeadKey {
			continue
		}
		siblings = append(siblings, strings.TrimPrefix(e.Key, dir))
	}
	if len(siblings) > 0 {
		sort.Strings(siblings)
		return "", refusedf(
			"a table reads every file in this file's directory, and %s sits beside it; move or remove it and register again",
			joinAnd(siblings))
	}

	return LocationURI(src.Bucket, dir), nil
}

// joinAnd renders a short list in prose so a refusal names what is in the way
// rather than printing a slice.
func joinAnd(items []string) string {
	if len(items) == 0 {
		return ""
	}
	head, last := items[:len(items)-1], items[len(items)-1]
	switch len(head) {
	case 0:
		return last
	case 1:
		return head[0] + " and " + last
	default:
		return strings.Join(head, ", ") + ", and " + last
	}
}

// columnsFor reads the object's header row.
func (r *Registrar) columnsFor(ctx context.Context, src Source) ([]Column, error) {
	if !isCSV(src.ContentType, src.HeadKey) {
		return nil, ErrNotCSV
	}
	objects := r.objectsFor(src.Kind)
	if objects == nil {
		return nil, ErrUnavailable
	}
	body, _, err := objects.GetObject(ctx, src.Bucket, src.HeadKey)
	if err != nil {
		return nil, fmt.Errorf("reading the file: %w", err)
	}
	if int64(len(body)) > r.deps.MaxBytes {
		return nil, refusedf("the file is larger than the %d MB a registration reads", r.deps.MaxBytes>>20)
	}
	return ReadHeaderColumns(body)
}

// isCSV reports whether the source is a CSV. The stored content type decides
// it; the key's extension is the fallback for a record written before
// detection, or one whose type was never set.
func isCSV(declared, key string) bool {
	if ct := strings.ToLower(strings.TrimSpace(declared)); ct != "" {
		if base, _, found := strings.Cut(ct, ";"); found {
			ct = strings.TrimSpace(base)
		}
		if ct != "" && ct != contenttype.OctetStream && ct != "text/plain" {
			return strings.Contains(ct, "csv")
		}
	}
	return strings.HasSuffix(strings.ToLower(key), ".csv")
}

// tableNameFor derives the name the table takes.
func tableNameFor(caller Caller, src Source, req Request) string {
	raw := req.TableName
	if strings.TrimSpace(raw) == "" {
		// The filename is what a person recognizes the file by, and it is what
		// they would have typed.
		raw = fileNameOf(src.HeadKey)
		if SlugifyTableName(raw) == "" {
			raw = src.Name
		}
	}
	slug := SlugifyTableName(raw)
	if slug == "" {
		return ""
	}
	return PrefixedTableName(caller.Persona, slug)
}

// fileNameOf returns the last segment of an object key.
func fileNameOf(key string) string {
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		return key[idx+1:]
	}
	return key
}

// newID mints a registration id.
func (r *Registrar) newID() (string, error) {
	if r.deps.NewID == nil {
		return "", errors.New("no id generator configured")
	}
	id, err := r.deps.NewID()
	if err != nil {
		return "", fmt.Errorf("generating a registration id: %w", err)
	}
	return id, nil
}

// runDDL issues the statements a registration is made of, in order.
func (r *Registrar) runDDL(ctx context.Context, connection string, ddl []string) error {
	for _, stmt := range ddl {
		if err := r.deps.Trino.Exec(ctx, connection, stmt); err != nil {
			return fmt.Errorf("registering the table: %w", err)
		}
	}
	return nil
}

// Unregister drops a registered table and forgets it.
//
// Dropping a Hive external table removes the metastore entry and leaves the
// objects, so unregistering never touches the file the person uploaded. The
// row goes even when the DROP fails: the alternative is a record of a table
// nobody can remove through the platform, and the DROP is IF EXISTS so a table
// already gone is not an error in the first place.
func (r *Registrar) Unregister(ctx context.Context, caller Caller, id, source string) error {
	if !r.Available() {
		return ErrUnavailable
	}
	reg, err := r.mayUnregister(ctx, caller, id)
	if err != nil {
		return err
	}

	stmt := "DROP TABLE IF EXISTS " + qualified(*reg)
	execErr := r.deps.Trino.Exec(ctx, reg.Connection, stmt)
	r.audit(ctx, auditRecord{caller: caller, reg: *reg, ddl: []string{stmt}, source: source, err: execErr})
	if execErr != nil {
		slog.Warn("table registration: dropping the table failed; the record is removed anyway",
			"registration", logsan.SanitizeForLog(reg.ID),
			logFieldTable, logsan.SanitizeForLog(reg.QualifiedName()),
			logFieldError, logsan.SanitizeForLog(execErr.Error()))
	}
	if err := r.deps.Store.Delete(ctx, reg.ID); err != nil {
		return fmt.Errorf("removing the registration: %w", err)
	}
	return nil
}

// mayUnregister reads the registration and decides whether this caller may
// drop it: the person who made it, or an administrator. An id that does not
// exist is answered before identity, so a probe learns nothing about what is
// registered.
func (r *Registrar) mayUnregister(ctx context.Context, caller Caller, id string) (*Registration, error) {
	reg, err := r.deps.Store.Get(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's ErrNotFound is what a surface renders
	}
	if reg == nil {
		return nil, ErrNotFound
	}
	if r.deps.Scope != nil && !r.deps.Scope.AllowConnection(caller.Persona, reg.Connection) {
		return nil, ErrConnectionDenied
	}
	if caller.Email == "" {
		return nil, ErrNoIdentity
	}
	if !caller.IsAdmin && reg.RegisteredBy != caller.Email {
		return nil, refusedf("%s was registered by %s and only they or an administrator can remove it",
			reg.QualifiedName(), reg.RegisteredBy)
	}
	return reg, nil
}

// UnregisterAllForSource drops every table registered over a source. It is
// what a resource or asset delete calls: the file is going, and a table over
// where it used to be would return nothing and explain nothing.
//
// It is best-effort by design. The delete that triggered it has its own
// reasons to succeed, and failing it because a scratch table could not be
// dropped would make an unrelated Trino outage look like a broken delete.
func (r *Registrar) UnregisterAllForSource(ctx context.Context, kind, sourceID string) {
	if !r.Available() {
		return
	}
	regs, err := r.deps.Store.BySource(ctx, kind, sourceID)
	if err != nil {
		slog.Warn("table registration: could not list registrations of a deleted source",
			"kind", logsan.SanitizeForLog(kind), "source", logsan.SanitizeForLog(sourceID), logFieldError, logsan.SanitizeForLog(err.Error()))
		return
	}
	for _, reg := range regs {
		if err := r.deps.Trino.Exec(ctx, reg.Connection, "DROP TABLE IF EXISTS "+qualified(reg)); err != nil {
			slog.Warn("table registration: dropping the table of a deleted source failed",
				logFieldTable, logsan.SanitizeForLog(reg.QualifiedName()), logFieldError, logsan.SanitizeForLog(err.Error()))
		}
		if err := r.deps.Store.Delete(ctx, reg.ID); err != nil {
			slog.Warn("table registration: removing the record of a deleted source failed",
				"registration", logsan.SanitizeForLog(reg.ID), logFieldError, logsan.SanitizeForLog(err.Error()))
		}
	}
}

// BySource returns every registration over one source.
func (r *Registrar) BySource(ctx context.Context, kind, sourceID string) ([]Registration, error) {
	if !r.Available() {
		return nil, nil //nolint:nilnil // no registrations is an answer, not a failure
	}
	return r.deps.Store.BySource(ctx, kind, sourceID) //nolint:wrapcheck // transparent read pass-through
}

// ForSources returns the registrations of many sources at once.
func (r *Registrar) ForSources(ctx context.Context, kind string, ids []string) (map[string][]Registration, error) {
	if !r.Available() || len(ids) == 0 {
		return nil, nil //nolint:nilnil // no registrations is an answer, not a failure
	}
	return r.deps.Store.ForSources(ctx, kind, ids) //nolint:wrapcheck // transparent read pass-through
}

// audit records the statement the registrar ran, the way the portal's DataHub
// writes are recorded: same event kind, same fields, so a registration shows
// up in the audit trail beside every other write the platform makes on a
// caller's behalf. Best-effort -- a logging failure never fails the operation.
func (r *Registrar) audit(ctx context.Context, rec auditRecord) {
	if r.deps.Audit == nil {
		return
	}
	source := rec.source
	if source == "" {
		source = "portal"
	}

	ev := audit.NewEvent("table_register")
	ev.UserID = rec.caller.UserID
	ev.UserEmail = rec.caller.Email
	ev.Persona = rec.caller.Persona
	ev.ToolkitKind = "trino"
	ev.ToolkitName = rec.reg.Connection
	ev.Connection = rec.reg.Connection
	ev.Parameters = map[string]any{
		"sql":         strings.Join(rec.ddl, ";\n"),
		"table":       rec.reg.QualifiedName(),
		"location":    rec.reg.Location,
		"source_kind": rec.reg.SourceKind,
		"source_id":   rec.reg.SourceID,
	}
	ev.Source = source
	ev.Transport = "http"
	ev.EventKind = audit.EventTypeMCPToolCall
	ev.Authorized = true
	ev.Success = rec.err == nil
	if rec.err != nil {
		ev.ErrorMessage = rec.err.Error()
	}
	if err := r.deps.Audit.Log(ctx, *ev); err != nil {
		slog.Warn("table registration audit log failed", logFieldError, logsan.SanitizeForLog(err.Error()),
			logFieldTable, logsan.SanitizeForLog(rec.reg.QualifiedName()))
	}
}

// auditRecord is what one audited registration or unregistration carries: who
// did it, to which registration, with which statements, and how it ended.
type auditRecord struct {
	caller Caller
	reg    Registration
	ddl    []string
	source string
	err    error
}
