package callrecord

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// Tool names the catalog records. They are the platform's data-access verbs:
// the two that run a statement, the two that stream a result to an asset, and
// the one that invokes an upstream API. Everything else a toolkit offers —
// browsing a catalog, describing a table, listing endpoints — is discovery, and
// a record of it would be a record of nothing worth running again.
const (
	toolTrinoQuery   = "trino_query"
	toolTrinoExecute = "trino_execute"
	toolTrinoExport  = "trino_export"
	toolAPIInvoke    = "api_invoke_endpoint"
	toolAPIExport    = "api_export"
)

// recordedTools maps a tool name to the kind of record it produces.
var recordedTools = map[string]string{
	toolTrinoQuery:   KindSQL,
	toolTrinoExecute: KindSQL,
	toolTrinoExport:  KindSQL,
	toolAPIInvoke:    KindAPI,
	toolAPIExport:    KindAPI,
}

// KindForTool returns the record kind a tool produces, or "" when the tool is
// not one the catalog records.
func KindForTool(tool string) string { return recordedTools[tool] }

// URNBuilder turns a table reference into the dataset URN the catalog knows it
// by, applying the connection's platform and catalog mapping. It is the same
// function the reflexive-capture path takes (middleware.URNBuilder); declared
// here so this package does not import the middleware that will later read it.
//
// The connection is named by kind and name together. A name alone is ambiguous
// where a deployment carries it across kinds, and the target it produced then
// named a platform the statement never ran against (#1384); the audit event
// this recorder reads carries the kind alongside the name.
type URNBuilder func(connectionKind, connection, catalog, schema, table string) string

// Recorder catalogs data-access calls as they are audited.
//
// It is a decorator over the audit store rather than a middleware of its own,
// for two reasons. The audit event is the complete record of a call — its id,
// its purpose, its outcome, its duration, its arguments after the redaction
// policy has been applied — so a middleware would be reassembling what already
// exists. And the platform's audit writer is asynchronous, so a decorator here
// does its work on the writer's drain goroutine: cataloging a query costs the
// query nothing.
//
// A failure to record is logged and swallowed. The catalog is derived from the
// audit log; losing an entry costs a query its place in the catalog, and must
// never cost the audit row it was derived from.
type Recorder struct {
	inner audit.Logger
	store Store
	urn   URNBuilder
}

// NewRecorder wraps an audit store so every data-access call it records is also
// cataloged. A nil store returns the audit logger unchanged, which is what a
// deployment without the catalog gets.
func NewRecorder(inner audit.Logger, store Store, urn URNBuilder) audit.Logger {
	if store == nil {
		return inner
	}
	return &Recorder{inner: inner, store: store, urn: urn}
}

// Log writes the audit event, then catalogs it when it is a data-access call.
// The audit write goes first and its error is returned unchanged: audit is the
// system of record, and the catalog is a reader of it.
func (r *Recorder) Log(ctx context.Context, event audit.Event) error {
	err := r.inner.Log(ctx, event)
	if rec, ok := r.recordFrom(event); ok {
		r.catalog(ctx, rec)
	}
	//nolint:wrapcheck // the audit store's error is the caller's to interpret;
	// wrapping it here would make this decorator look like the failure.
	return err
}

// Query passes through to the audit store.
//
//nolint:wrapcheck // a pass-through must not relabel the store's own error.
func (r *Recorder) Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	return r.inner.Query(ctx, filter)
}

// Close passes through to the audit store, which owns the connection.
//
//nolint:wrapcheck // a pass-through must not relabel the store's own error.
func (r *Recorder) Close() error { return r.inner.Close() }

// catalog stores the record and credits whatever it re-ran.
func (r *Recorder) catalog(ctx context.Context, rec Record) {
	if err := r.store.Insert(ctx, rec); err != nil {
		slog.Warn("call catalog: record not stored",
			"tool", rec.ToolName, "event_id", rec.EventID, "error", err)
		return
	}
	if !rec.Success {
		// A failed call re-ran nothing, so there is nothing to credit. The
		// store enforces this too (its Store contract is reachable from
		// elsewhere); skipping here saves a round trip per failed query.
		return
	}
	if _, err := r.store.CreditReuse(ctx, rec); err != nil {
		slog.Warn("call catalog: reuse not credited",
			"tool", rec.ToolName, "event_id", rec.EventID, "error", err)
	}
}

// recordFrom builds the record one audit event describes, and reports whether
// the event is a call the catalog keeps. An event with no id is not kept: the
// id is what an agent cites and what an asset records, so a record without one
// could never be referenced.
func (r *Recorder) recordFrom(ev audit.Event) (Record, bool) {
	kind := KindForTool(ev.ToolName)
	if kind == "" || ev.ID == "" {
		return Record{}, false
	}
	rec := Record{
		EventID:       ev.ID,
		Kind:          kind,
		ToolName:      ev.ToolName,
		Connection:    ev.Connection,
		Purpose:       ev.Purpose,
		UserID:        ev.UserID,
		UserEmail:     ev.UserEmail,
		SessionID:     ev.SessionID,
		Persona:       ev.Persona,
		Success:       ev.Success,
		ErrorMessage:  ev.ErrorMessage,
		DurationMS:    ev.DurationMS,
		ResponseChars: ev.ResponseChars,
		CreatedAt:     ev.Timestamp.UTC(),
	}
	if kind == KindSQL {
		r.describeSQL(&rec, ev.ToolkitKind, ev.Parameters)
	} else {
		describeAPI(&rec, ev.Parameters)
	}
	return rec, true
}

// describeSQL fills in the statement and the datasets it reads. Both come from
// the arguments the audit row kept, so a deployment that disables parameter
// capture catalogs the call without its statement rather than not at all: the
// purpose, the connection and the outcome are still worth having.
func (r *Recorder) describeSQL(rec *Record, connectionKind string, params map[string]any) {
	rec.Statement = stringParam(params, "sql")
	rec.Targets = r.targets(connectionKind, rec.Connection, rec.Statement)
}

// targets names the datasets a statement reads, as the URNs the catalog knows
// them by.
//
// A reference the statement does not qualify to catalog.schema.table is left
// out: the query engine resolves it against the session's own catalog, which
// the audit row does not record, so any URN built from it would name a dataset
// nobody asked for. A record with no targets is still a record; it is only
// never declared superseded, since supersession compares targets.
func (r *Recorder) targets(connectionKind, connection, statement string) []string {
	if r.urn == nil || statement == "" {
		return nil
	}
	var urns []string
	for _, ref := range sqltables.Extract(statement) {
		if ref.Catalog == "" || ref.Schema == "" || ref.Table == "" {
			continue
		}
		if urn := r.urn(connectionKind, connection, ref.Catalog, ref.Schema, ref.Table); urn != "" {
			urns = append(urns, urn)
		}
	}
	return urns
}

// describeAPI fills in the request line and the endpoint the call addressed.
func describeAPI(rec *Record, params map[string]any) {
	rec.Method = strings.ToUpper(stringParam(params, "method"))
	rec.Path = stringParam(params, "path")
	rec.OperationID = stringParam(params, "operation_id")
	target := APITarget(rec.Connection, rec.Method, rec.Path, rec.OperationID,
		stringMapParam(params, "path_params"))
	if target != "" {
		rec.Targets = []string{target}
	}
}

// apiTargetPrefix opens the target of an API call. An API record's target is
// not a dataset URN — there is no catalog entity for an endpoint — so it is
// spelled distinctly rather than made to look like one.
const apiTargetPrefix = "api:"

// APITarget names the resource an API call addressed: its operation id with
// every path parameter resolved into it, or the request line when the caller
// addressed the endpoint by path directly. It is scoped by connection for the
// same reason a dataset URN is scoped by platform: the same operation id
// against two upstreams is two endpoints.
//
// The path parameters are part of the target, not decoration. A target is what
// decides whether two calls addressed the same thing, so an operation id alone
// would make the same mutation against two different resources one target, and
// every call through a generic dispatch endpoint one target (#1352).
//
// A target that cannot distinguish the call is not returned at all. An
// operation id holding a placeholder no path parameter resolved names a
// template rather than a resource, and the empty target it yields is the same
// answer the SQL side gives when it cannot tell what a statement read: not
// comparable, so never declared superseded and never credited as reuse.
func APITarget(connection, method, path, operationID string, pathParams map[string]string) string {
	switch {
	case operationID != "":
		resolved, used, complete := resolveTemplate(operationID, pathParams)
		if !complete {
			return ""
		}
		return apiTargetPrefix + connection + ":" + resolved + unresolvedParams(pathParams, used)
	case path != "":
		// Only the path itself is a template question. A query string may
		// legitimately carry braces in a filter value, and dropping the target
		// over that would lose the endpoint for no reason.
		if route, _, _ := strings.Cut(path, "?"); placeholderPattern.MatchString(route) {
			return ""
		}
		return apiTargetPrefix + connection + ":" + strings.TrimSpace(method+" "+path)
	default:
		return ""
	}
}

// placeholderPattern matches one {name} slot of a path template. The name
// cannot span a path segment, which is what an OpenAPI template guarantees.
var placeholderPattern = regexp.MustCompile(`\{[^{}/]+\}`)

// resolveTemplate substitutes path parameters into a path template, reporting
// which parameters it consumed and whether every slot was filled. A slot with
// no value, or an empty one, leaves the template incomplete: an address with a
// hole in it identifies nothing.
func resolveTemplate(template string, params map[string]string) (resolved string, used map[string]bool, complete bool) {
	used = make(map[string]bool, len(params))
	complete = true
	resolved = placeholderPattern.ReplaceAllStringFunc(template, func(slot string) string {
		name := slot[1 : len(slot)-1]
		value := strings.TrimSpace(params[name])
		if value == "" {
			complete = false
			return slot
		}
		used[name] = true
		return value
	})
	return resolved, used, complete
}

// unresolvedParams renders the path parameters no slot consumed. An operation id
// that is a name rather than a template — which is what a spec declaring
// operationIds gives — carries no slots at all, so this is where its resolved
// resource is stated.
//
// It is rendered as JSON because a target is an identity: Go orders a map's keys
// when it marshals one, so two calls carrying the same values compare equal, and
// a value holding a separator cannot make two different parameter sets render
// alike. Substituting into a template needs no such care — a resolved path is
// the address the request actually used, so two calls resolving to the same one
// did address the same resource.
func unresolvedParams(params map[string]string, used map[string]bool) string {
	extra := make(map[string]string, len(params))
	for name, value := range params {
		value = strings.TrimSpace(value)
		if used[name] || value == "" {
			continue
		}
		extra[name] = value
	}
	if len(extra) == 0 {
		return ""
	}
	encoded, err := json.Marshal(extra)
	if err != nil {
		// Unreachable for map[string]string; a target that cannot be rendered
		// is no target rather than a partial one.
		return ""
	}
	return "(" + string(encoded) + ")"
}

// stringParam reads one string argument off a recorded parameter map.
func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

// stringMapParam reads one map-valued argument off a recorded parameter map.
// Both shapes an argument reaches here in are read: the typed map a toolkit
// declares, and the map[string]any a JSON round trip leaves behind.
func stringMapParam(params map[string]any, key string) map[string]string {
	switch raw := params[key].(type) {
	case map[string]string:
		return raw
	case map[string]any:
		out := make(map[string]string, len(raw))
		for name, value := range raw {
			if s, ok := value.(string); ok {
				out[name] = s
			}
		}
		return out
	default:
		return nil
	}
}

// Verify interface compliance: the recorder stands where the audit store does.
var _ audit.Logger = (*Recorder)(nil)
