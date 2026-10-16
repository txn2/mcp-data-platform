// Package tablereconcile compares a connection's scratch schema with the
// table registrations on it and repairs what the platform can prove is its
// own (#2136).
//
// A table can exist with no registration (a registration that failed after
// the coordinator made its table, a restore, a statement run by hand), a
// registration can name a table that is gone, and a table can read somewhere
// its registration does not say. Nothing else lists those three, and the
// first could not be removed through the platform at all: unregistering takes
// a registration id, and an orphan has none.
//
// What is repaired is only what the platform can prove is its own: an orphan
// whose location is the directory of a file the platform stores, and a
// registration whose table is gone. Everything else is reported and left.
package tablereconcile

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister/srcdir"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// Engine is what a reconcile asks the query engine. The Trino toolkit
// satisfies it.
type Engine interface {
	ScratchTarget(connection string) (trino.ScratchConfig, bool)
	ListTables(ctx context.Context, connection, catalog, schema string) ([]trino.SchemaTable, bool, error)
	TableLocation(ctx context.Context, connection, catalog, schema, table string) (location string, found bool, err error)
	TableExists(ctx context.Context, connection, catalog, schema, table string) (bool, error)
	Exec(ctx context.Context, connection, sql string) error
}

// Store is the half of the registration store a reconcile reads and writes.
// tableregister.Store satisfies it.
type Store interface {
	List(ctx context.Context, f tableregister.Filter) ([]tableregister.Registration, int, error)
	ByName(ctx context.Context, connection, catalog, schema, table string) (*tableregister.Registration, error)
	Get(ctx context.Context, id string) (*tableregister.Registration, error)
	Delete(ctx context.Context, id string) error
}

// Auditor records a repair. tableregister.Registrar satisfies it.
type Auditor interface {
	AuditStatement(ctx context.Context, s tableregister.Statement)
}

// Deps is what a Reconciler acts through.
type Deps struct {
	Engine Engine
	Store  Store
	Audit  Auditor
	// Locate reads a stored file by kind and id. Nil leaves only the
	// managed-resources layout to prove an orphan by.
	Locate tableregister.Locator
	// ResourceBucket is the managed-resources bucket: a location under its
	// resources/ prefix is a managed resource's directory whether or not the
	// resource still exists.
	ResourceBucket string
}

// Reconciler compares a connection's scratch schema with the registrations on
// it and repairs what can be proved.
type Reconciler struct {
	deps Deps
}

// New builds a Reconciler, or nil when the engine or the store is missing.
func New(deps Deps) *Reconciler {
	if deps.Engine == nil || deps.Store == nil || deps.Audit == nil {
		return nil
	}
	return &Reconciler{deps: deps}
}

// readLimit bounds the definitions read at once.
const readLimit = 8

// auditSource is the source a repair's audit event records.
const auditSource = "admin"

// Reconcile compares the scratch schema of a connection with the
// registrations on it.
func (c *Reconciler) Reconcile(ctx context.Context, connection string) (*Reconciliation, error) {
	target, ok := c.deps.Engine.ScratchTarget(connection)
	if !ok || !target.Configured() {
		return nil, tableregister.ErrNoScratchTarget
	}
	tables, truncated, err := c.deps.Engine.ListTables(ctx, connection, target.Catalog, target.Schema)
	if err != nil {
		return nil, fmt.Errorf("listing the scratch schema: %w", err)
	}
	regs, err := c.registrations(ctx, connection, target)
	if err != nil {
		return nil, err
	}
	out := &Reconciliation{
		Connection: connection, Catalog: target.Catalog, Schema: target.Schema, Truncated: truncated,
		Orphans: []Orphan{}, Missing: []Missing{}, Drifted: []Drifted{},
	}
	present := make(map[string]bool, len(tables))
	for _, t := range tables {
		present[t.Name] = true
	}
	for _, r := range regs {
		if !present[r.Table] && !truncated {
			out.Missing = append(out.Missing, Missing{Registration: r, Provable: r.SourceKind != tableregister.KindWebhook})
		}
	}
	locations, err := c.locations(ctx, connection, target, tables, regs)
	if err != nil {
		return nil, err
	}
	c.classify(ctx, out, tables, regs, locations)
	return out, nil
}

// registrations reads every registration on the connection's scratch schema,
// keyed by table name.
func (c *Reconciler) registrations(
	ctx context.Context, connection string, target trino.ScratchConfig,
) (map[string]tableregister.Registration, error) {
	out := map[string]tableregister.Registration{}
	for offset := 0; ; offset += tableregister.MaxListLimit {
		page, total, err := c.deps.Store.List(ctx, tableregister.Filter{
			Connections: []string{connection}, Limit: tableregister.MaxListLimit, Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("listing the registrations: %w", err)
		}
		for _, r := range page {
			if r.Catalog == target.Catalog && r.Schema == target.Schema {
				out[r.Table] = r
			}
		}
		if offset+len(page) >= total || len(page) == 0 {
			return out, nil
		}
	}
}

// locations reads the location of every table that is not a view and is
// either unregistered or registered over a file, a few at a time.
func (c *Reconciler) locations(
	ctx context.Context, connection string, target trino.ScratchConfig,
	tables []trino.SchemaTable, regs map[string]tableregister.Registration,
) (map[string]string, error) {
	var names []string
	for _, t := range tables {
		if r, ok := regs[t.Name]; t.View || (ok && r.SourceKind == tableregister.KindWebhook) {
			continue
		}
		names = append(names, t.Name)
	}
	found := make([]string, len(names))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(readLimit)
	for i, name := range names {
		g.Go(func() error {
			loc, _, err := c.deps.Engine.TableLocation(gctx, connection, target.Catalog, target.Schema, name)
			if err != nil {
				return fmt.Errorf("reading the location of %s: %w", name, err)
			}
			found[i] = loc
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("reading the scratch tables: %w", err)
	}
	out := make(map[string]string, len(names))
	for i, name := range names {
		out[name] = found[i]
	}
	return out, nil
}

// classify sorts the tables whose locations were read into orphans and
// drifted registrations. A table under a webhook source's directory is that
// source's raw or compacted table and is neither.
func (c *Reconciler) classify(
	ctx context.Context, out *Reconciliation, tables []trino.SchemaTable,
	regs map[string]tableregister.Registration, locations map[string]string,
) {
	var webhookDirs []string
	for _, r := range regs {
		if r.SourceKind == tableregister.KindWebhook && r.Location != "" {
			webhookDirs = append(webhookDirs, r.Location)
		}
	}
	for _, t := range tables {
		loc, read := locations[t.Name]
		if !read {
			continue
		}
		if r, ok := regs[t.Name]; ok {
			if !srcdir.Same(r.Location, loc) {
				out.Drifted = append(out.Drifted, Drifted{Registration: r, Location: loc})
			}
			continue
		}
		if srcdir.Under(loc, webhookDirs) {
			continue
		}
		src := c.sourceAt(ctx, loc)
		out.Orphans = append(out.Orphans, Orphan{Table: t.Name, Location: loc, Source: src, Provable: src != nil})
	}
}

// sourceAt names the stored file a location is the directory of: a managed
// resource or a portal asset the platform holds whose own directory the
// location lies in, or, under the managed-resources bucket's resources/
// prefix, the resource its layout names whether or not it still exists. Nil
// for a location that is neither.
func (c *Reconciler) sourceAt(ctx context.Context, location string) *SourceRef {
	bucket, key, ok := srcdir.Split(location)
	if !ok {
		return nil
	}
	if ref := c.heldSourceAt(ctx, key, location); ref != nil {
		return ref
	}
	if c.deps.ResourceBucket != "" && bucket == c.deps.ResourceBucket {
		return resourceByLayout(key)
	}
	return nil
}

// heldSourceAt finds the stored file whose own directory the location lies
// in, trying each path segment of the key as its id.
func (c *Reconciler) heldSourceAt(ctx context.Context, key, location string) *SourceRef {
	if c.deps.Locate == nil {
		return nil
	}
	for _, kind := range []string{tableregister.KindResource, tableregister.KindAsset} {
		for _, id := range srcdir.CandidateIDs(key) {
			src, found := c.deps.Locate(ctx, kind, id)
			if found && srcdir.Owns(src.Bucket, src.ID, src.HeadKey, location) {
				return &SourceRef{Kind: kind, ID: id, Exists: true}
			}
		}
	}
	return nil
}

// The managed-resource key layout: resources/{scope}/{scope id}/{resource id}/...
const (
	resourceKeyRoot    = "resources"
	resourceIDSegment  = 3
	resourceKeyMinimum = resourceIDSegment + 2
)

// resourceByLayout reads the resource a managed-resources key names.
func resourceByLayout(key string) *SourceRef {
	parts := strings.Split(key, srcdir.Sep)
	if len(parts) < resourceKeyMinimum || parts[0] != resourceKeyRoot || parts[resourceIDSegment] == "" {
		return nil
	}
	return &SourceRef{Kind: tableregister.KindResource, ID: parts[resourceIDSegment]}
}

// Repair carries out the repairs asked for on a connection, each proved again
// at the moment it is made, and reports what each did. A repair that cannot
// be proved is reported, never made.
func (c *Reconciler) Repair(
	ctx context.Context, caller tableregister.Caller, connection string, items []RepairItem,
) ([]RepairOutcome, error) {
	target, ok := c.deps.Engine.ScratchTarget(connection)
	if !ok || !target.Configured() {
		return nil, tableregister.ErrNoScratchTarget
	}
	out := make([]RepairOutcome, 0, len(items))
	for _, it := range items {
		var reason string
		switch it.Action {
		case ActionDrop:
			reason = c.dropOrphan(ctx, caller, connection, target, it.Table)
		case ActionForget:
			reason = c.forgetMissing(ctx, caller, connection, it.RegistrationID)
		default:
			reason = "unknown action " + it.Action + "; drop or forget"
		}
		out = append(out, RepairOutcome{RepairItem: it, Done: reason == "", Reason: reason})
	}
	return out, nil
}

// dropOrphan drops a table with no registration whose location is a stored
// file's directory, and returns why it did not when it did not.
func (c *Reconciler) dropOrphan(
	ctx context.Context, caller tableregister.Caller, connection string, target trino.ScratchConfig, table string,
) string {
	reg, err := c.deps.Store.ByName(ctx, connection, target.Catalog, target.Schema, table)
	if err != nil {
		return "the registrations could not be read: " + err.Error()
	}
	if reg != nil {
		return "a registration holds this table; unregister it instead"
	}
	location, found, err := c.deps.Engine.TableLocation(ctx, connection, target.Catalog, target.Schema, table)
	switch {
	case err != nil:
		return "the table could not be read: " + err.Error()
	case !found:
		return "the table is already gone"
	case c.sourceAt(ctx, location) == nil:
		return "not the platform's: it reads " + srcdir.Describe(location) + "; left alone"
	}
	r := tableregister.Registration{
		Connection: connection, Catalog: target.Catalog, Schema: target.Schema, Table: table, Location: location,
	}
	stmt := tableregister.DropTableStatement(r)
	err = c.deps.Engine.Exec(ctx, connection, stmt)
	c.deps.Audit.AuditStatement(ctx, tableregister.Statement{
		Caller: caller, Registration: r, DDL: []string{stmt}, Source: auditSource, Err: err,
	})
	if err != nil {
		return "the drop failed: " + err.Error()
	}
	return ""
}

// forgetMissing deletes a registration whose table is gone, and returns why
// it did not when it did not.
func (c *Reconciler) forgetMissing(ctx context.Context, caller tableregister.Caller, connection, id string) string {
	reg, err := c.deps.Store.Get(ctx, id)
	if err != nil || reg == nil || reg.Connection != connection {
		return "no registration " + id + " on " + connection
	}
	if reg.SourceKind == tableregister.KindWebhook {
		return "a webhook source's table is removed by deleting the source"
	}
	exists, err := c.deps.Engine.TableExists(ctx, connection, reg.Catalog, reg.Schema, reg.Table)
	switch {
	case err != nil:
		return "the table could not be looked up: " + err.Error()
	case exists:
		return "the table exists; unregister it instead"
	}
	err = c.deps.Store.Delete(ctx, reg.ID)
	c.deps.Audit.AuditStatement(ctx, tableregister.Statement{
		Caller: caller, Registration: *reg, Source: auditSource, Err: errors.Join(errTableGone, err),
	})
	if err != nil {
		return "the registration could not be removed: " + err.Error()
	}
	return ""
}

// errTableGone is the cause a forgotten registration's audit event records.
var errTableGone = errors.New("the registration was removed because its table no longer exists")
