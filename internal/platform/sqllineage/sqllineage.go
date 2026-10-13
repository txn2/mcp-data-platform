// Package sqllineage records lineage from the statements the platform runs
// (#1922): a statement that writes a table from what it reads -- CREATE TABLE
// AS, INSERT INTO ... SELECT, MERGE, CREATE VIEW -- becomes an edge from each
// table it read to the table it wrote. It is an audit decorator, so it sees
// every statement the tool surface ran, whoever ran it: an agent, a person in
// the portal, or a managed script.
//
// It reads the statement from the audited call's parameters, so a deployment
// that turns parameter logging off records no SQL lineage.
package sqllineage

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// writingTools are the tools a writing statement can be run through.
var writingTools = map[string]bool{"trino_execute": true}

// connectionKind is the kind of every connection those tools run on.
const connectionKind = "trino"

// EdgeWriter records lineage edges.
type EdgeWriter interface {
	AddEdges(ctx context.Context, edges []catalog.Edge) error
}

// Connection is what the recorder needs to know about the connection a
// statement ran on: the instance name catalog references carry, and the
// catalog and schema an unqualified name resolves in.
type Connection struct {
	Instance string
	Catalog  string
	Schema   string
}

// Resolver finds a connection by the name a call bound it by.
type Resolver func(bound string) (Connection, bool)

// Recorder is the decorator.
type Recorder struct {
	inner   audit.Logger
	edges   EdgeWriter
	resolve Resolver
}

// New wraps inner.
func New(inner audit.Logger, edges EdgeWriter, resolve Resolver) *Recorder {
	return &Recorder{inner: inner, edges: edges, resolve: resolve}
}

// Log writes the event, then records the lineage of a successful writing
// statement. A lineage write that fails is logged; the audit write's own
// outcome is what is returned.
func (r *Recorder) Log(ctx context.Context, event audit.Event) error {
	err := r.inner.Log(ctx, event)
	if edges := r.edgesOf(event); len(edges) > 0 {
		if lerr := r.edges.AddEdges(ctx, edges); lerr != nil {
			slog.WarnContext(ctx, "sql lineage: edges not recorded", "event_id", logsan.SanitizeForLog(event.ID),
				"error", logsan.SanitizeForLog(lerr.Error()))
		}
	}
	return err //nolint:wrapcheck // the audit store's error, unchanged
}

// Query passes through to the audit store.
func (r *Recorder) Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	return r.inner.Query(ctx, filter) //nolint:wrapcheck // a pass-through
}

// Close passes through to the audit store.
func (r *Recorder) Close() error { return r.inner.Close() } //nolint:wrapcheck // a pass-through

// edgesOf returns the edges a successful writing statement makes, or none.
func (r *Recorder) edgesOf(event audit.Event) []catalog.Edge {
	if !event.Success || !writingTools[event.ToolName] {
		return nil
	}
	sql, _ := event.Parameters["sql"].(string)
	target, ok := sqltables.WriteTarget(sql)
	if !ok {
		return nil
	}
	conn, ok := r.resolve(event.Connection)
	if !ok {
		return nil
	}
	targetRef, ok := qualify(conn, target)
	if !ok {
		return nil
	}
	edges := make([]catalog.Edge, 0)
	for _, src := range sqltables.Extract(sql) {
		sourceRef, ok := qualify(conn, src)
		if !ok || sourceRef == targetRef {
			continue
		}
		edges = append(edges, catalog.Edge{
			Source: sourceRef, Target: targetRef, WriterKind: catalog.WriterSQL,
			WriterRef: event.ID, WrittenBy: event.UserEmail,
		})
	}
	return edges
}

// qualify names a table fully under the connection's defaults and returns its
// dataset reference. A name the defaults cannot complete names no dataset.
func qualify(conn Connection, t sqltables.Ref) (string, bool) {
	cat, schema := t.Catalog, t.Schema
	if schema == "" {
		schema = conn.Schema
	}
	if cat == "" {
		cat = conn.Catalog
	}
	if cat == "" || schema == "" || t.Table == "" {
		return "", false
	}
	return catalog.DatasetRef(connectionKind, conn.Instance, cat, schema, t.Table).String(), true
}
