package sqllineage

import (
	"context"
	"log/slog"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptout/exportrecord"
	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// readingTools are the tools a script reads tables through.
var readingTools = map[string]bool{"trino_query": true, "trino_execute": true, "trino_export": true}

// Scripts records the lineage of managed-script runs: an edge from every
// table a run read to every file it wrote. A table registered over one of
// those files has its own edge from the file, so a walk upstream from the
// table reaches what the run read.
type Scripts struct {
	edges   EdgeWriter
	resolve Resolver
}

// NewScripts builds the script lineage recorder.
func NewScripts(edges EdgeWriter, resolve Resolver) *Scripts {
	return &Scripts{edges: edges, resolve: resolve}
}

// Run collects what one run read.
type Run struct {
	s     *Scripts
	mu    sync.Mutex
	reads []string
	seen  map[string]bool
}

// Begin starts collecting for one run.
func (s *Scripts) Begin() *Run {
	if s == nil {
		return nil
	}
	return &Run{s: s, seen: make(map[string]bool)}
}

// Observe returns a call observer that notes the tables each successful query
// read and then hands the call to next.
func (r *Run) Observe(next func(tool string, args, out map[string]any, err error)) func(string, map[string]any, map[string]any, error) {
	if r == nil {
		return next
	}
	return func(tool string, args, out map[string]any, err error) {
		if err == nil && readingTools[tool] {
			r.note(args)
		}
		if next != nil {
			next(tool, args, out, err)
		}
	}
}

// note records the tables a statement read.
func (r *Run) note(args map[string]any) {
	sql, _ := args["sql"].(string)
	connection, _ := args["connection"].(string)
	conn, ok := r.s.resolve(connection)
	if !ok {
		return
	}
	target, writes := sqltables.WriteTarget(sql)
	for _, t := range sqltables.Extract(sql) {
		if writes && t.FullPath == target.FullPath {
			continue
		}
		ref, ok := qualify(conn, t)
		if !ok {
			continue
		}
		r.mu.Lock()
		if !r.seen[ref] {
			r.seen[ref] = true
			r.reads = append(r.reads, ref)
		}
		r.mu.Unlock()
	}
}

// Record writes an edge from every table the run read to every file it wrote.
// A preview wrote nothing and adds nothing.
func (r *Run) Record(ctx context.Context, scriptID, writtenBy string, exports []exportrecord.Record) {
	if r == nil {
		return
	}
	r.mu.Lock()
	reads := append([]string(nil), r.reads...)
	r.mu.Unlock()
	edges := make([]catalog.Edge, 0)
	for _, e := range exports {
		target := exportRef(e)
		if target == "" {
			continue
		}
		for _, src := range reads {
			edges = append(edges, catalog.Edge{
				Source: src, Target: target, WriterKind: catalog.WriterScript,
				WriterRef: "mcp:script:" + scriptID, WrittenBy: writtenBy,
			})
		}
	}
	if len(edges) == 0 {
		return
	}
	if err := r.s.edges.AddEdges(ctx, edges); err != nil {
		slog.WarnContext(ctx, "script lineage: edges not recorded", "script", logsan.SanitizeForLog(scriptID),
			"error", logsan.SanitizeForLog(err.Error()))
	}
}

// exportRef is the reference of the file an export wrote: its managed
// resource or its portal asset. An export delivered to a bucket leaves no
// platform record to point at.
func exportRef(e exportrecord.Record) string {
	switch {
	case e.Preview:
		return ""
	case e.ResourceID != "":
		return "mcp:resource:" + e.ResourceID
	case e.AssetID != "":
		return "mcp:asset:" + e.AssetID
	default:
		return ""
	}
}
