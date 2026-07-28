// Package connview builds the list_connections view: the configured connections
// across toolkits, each enriched with the canonical knowledge pages that reference
// it (#634). It lives outside pkg/platform so that package stays within its size
// budget, and depends only on narrow capabilities (a source resolver and a
// knowledge-page reverse lookup) rather than on the platform itself.
package connview

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// maxKnowledgePages bounds how many referencing pages are listed per connection, so
// list_connections output stays small even when a connection is widely documented.
// The full total is still reported via Entry.KnowledgePageCount.
const maxKnowledgePages = 5

// knowledgeEnrichConcurrency bounds the parallel per-connection knowledge-page
// lookups in enrichWithKnowledge, so a deployment with many connections cannot
// open an unbounded number of concurrent DB queries.
const knowledgeEnrichConcurrency = 8

// dataKinds are the toolkit kinds that represent a data connection in the fallback
// (non-ConnectionLister) path.
var dataKinds = map[string]bool{"trino": true, "datahub": true, "s3": true}

// KnowledgePage is a brief reference to a knowledge page documenting a connection.
type KnowledgePage struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// Entry describes a single toolkit connection. CatalogID and OperationCount are
// populated only for kinds where they have meaning (today: api).
type Entry struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Connection string `json:"connection"`
	// Reference is the canonical mcp:connection:(kind,name) citation string, so an
	// agent can reference this connection from a knowledge page without composing
	// it by hand.
	Reference         string                        `json:"reference,omitempty"`
	Description       string                        `json:"description,omitempty"`
	IsDefault         bool                          `json:"is_default,omitempty"`
	DataHubSourceName string                        `json:"datahub_source_name,omitempty"`
	CatalogID         string                        `json:"catalog_id,omitempty"`
	OperationCount    int                           `json:"operation_count,omitempty"`
	Health            *toolkit.ConnectionHealthWire `json:"health,omitempty"`
	// KnowledgePageCount is the total number of knowledge pages that reference this
	// connection; KnowledgePages carries a bounded sample of them (#634).
	KnowledgePageCount int             `json:"knowledge_page_count,omitempty"`
	KnowledgePages     []KnowledgePage `json:"knowledge_pages,omitempty"`
}

// Output is the JSON response for the list_connections tool. Withheld and Notice
// are present only when the caller's persona hid connections (#1108): the
// enumeration reports what it removed and why instead of quietly returning a
// short list that reads as the whole deployment.
type Output struct {
	Connections []Entry `json:"connections"`
	Count       int     `json:"count"`
	Withheld    int     `json:"withheld,omitempty"`
	Notice      string  `json:"notice,omitempty"`
}

// Permit reports whether the caller may see a connection. Build applies it
// before the (per-connection, concurrent) knowledge enrichment, so a hidden
// connection costs no lookup. A nil Permit enumerates every connection, which is
// what a system caller with no persona (the connection backfill) needs.
type Permit func(kind, name string) bool

// allows applies a Permit, treating nil as "every connection is visible".
func (p Permit) allows(kind, name string) bool {
	return p == nil || p(kind, name)
}

// ConnectionNames returns the persona-facing connection names a toolkit
// currently serves: every connection a multi-connection toolkit enumerates, or
// the single configured connection name of one that does not. These are the
// names a persona's connections rules match and a tool call's `connection`
// argument carries, so callers that reason about connection permissions resolve
// them here rather than assuming a toolkit is its connection.
func ConnectionNames(tk registry.Toolkit) []string {
	if lister, ok := tk.(toolkit.ConnectionLister); ok {
		details := lister.ListConnections()
		names := make([]string, 0, len(details))
		for _, c := range details {
			names = append(names, c.Name)
		}
		return names
	}
	return []string{policyName(tk)}
}

// policyName is the identity a persona's connection rules match on for a
// single-connection toolkit: its configured connection name, or its instance
// name when it carries none.
func policyName(tk registry.Toolkit) string {
	if conn := tk.Connection(); conn != "" {
		return conn
	}
	return tk.Name()
}

// SourceResolver resolves a connection's DataHub source name (empty when none).
type SourceResolver interface {
	DataHubSourceName(kind, name string) string
}

// PageLookup is the knowledge-page reverse lookup: the pages referencing a target.
type PageLookup interface {
	ListPagesReferencing(ctx context.Context, ref knowledgepage.EntityRef) ([]knowledgepage.PageRef, error)
}

// Build enumerates the connections across the toolkits that permit admits and
// enriches each with the knowledge pages that reference it (bounded by
// maxKnowledgePages). src, pages, and permit may be nil; a nil page lookup skips
// the knowledge enrichment and a nil permit enumerates every connection.
//
// A connection the permit rejects is counted, not merely dropped: the count
// (and the notice built from it) is what distinguishes "this deployment has one
// connection" from "you may see one of its connections".
func Build(ctx context.Context, toolkits []registry.Toolkit, src SourceResolver, pages PageLookup, permit Permit) Output {
	entries := make([]Entry, 0, len(toolkits))
	withheld := 0
	for _, tk := range toolkits {
		var n int
		if lister, ok := tk.(toolkit.ConnectionLister); ok {
			entries, n = appendFromLister(entries, tk, lister, src, permit)
		} else {
			entries, n = appendFallback(entries, tk, src, permit)
		}
		withheld += n
	}
	enrichWithKnowledge(ctx, pages, entries)
	return Output{Connections: entries, Count: len(entries), Withheld: withheld}
}

func appendFromLister(
	entries []Entry, tk registry.Toolkit, lister toolkit.ConnectionLister, src SourceResolver, permit Permit,
) (out []Entry, withheld int) {
	for _, conn := range lister.ListConnections() {
		if !permit.allows(tk.Kind(), conn.Name) {
			withheld++
			continue
		}
		e := Entry{
			Kind:           tk.Kind(),
			Name:           conn.Name,
			Connection:     conn.Name,
			Reference:      knowledgepage.ConnectionRef(tk.Kind(), conn.Name),
			Description:    conn.Description,
			IsDefault:      conn.IsDefault,
			CatalogID:      conn.CatalogID,
			OperationCount: conn.OperationCount,
			Health:         conn.Health.Wire(),
		}
		if src != nil {
			e.DataHubSourceName = src.DataHubSourceName(tk.Kind(), conn.Name)
		}
		entries = append(entries, e)
	}
	return entries, withheld
}

func appendFallback(entries []Entry, tk registry.Toolkit, src SourceResolver, permit Permit) (out []Entry, withheld int) {
	kind := tk.Kind()
	if !dataKinds[kind] {
		return entries, 0
	}
	// A single-connection toolkit's persona/audit identity is its configured
	// connection name, which may differ from its instance name; filter on the
	// former so discovery keys on exactly what the authorizer checks.
	if !permit.allows(kind, policyName(tk)) {
		return entries, 1
	}
	e := Entry{Kind: kind, Name: tk.Name(), Connection: tk.Connection(), Reference: knowledgepage.ConnectionRef(kind, tk.Name())}
	if src != nil {
		e.DataHubSourceName = src.DataHubSourceName(kind, tk.Name())
	}
	return append(entries, e), 0
}

// enrichWithKnowledge fills each entry's KnowledgePageCount and a bounded sample of
// referencing pages. A nil lookup or per-connection failure is skipped, never fatal.
// Knowledge pages are org-shared, so their titles are safe to surface here.
func enrichWithKnowledge(ctx context.Context, pages PageLookup, entries []Entry) {
	if pages == nil {
		return
	}
	// Fan out the independent per-connection reverse lookups (bounded), each
	// writing only its own index slot so no synchronization on entries is
	// needed (the house pattern in pkg/knowledge/router.go). errgroup provides
	// the concurrency bound; no goroutine returns an error, because a
	// per-connection failure degrades only that entry — it must never fail the
	// whole view, exactly as the previous serial loop did.
	var g errgroup.Group
	g.SetLimit(knowledgeEnrichConcurrency)
	for i := range entries {
		g.Go(func() error {
			e := &entries[i]
			refs, err := pages.ListPagesReferencing(ctx, knowledgepage.EntityRef{
				TargetType:     knowledgepage.RefTargetConnection,
				ConnectionKind: e.Kind,
				ConnectionName: e.Name,
			})
			if err != nil || len(refs) == 0 {
				return nil //nolint:nilerr // a per-connection lookup error degrades only this entry, never the whole view (matches the prior serial loop)
			}
			e.KnowledgePageCount = len(refs)
			for _, pg := range refs {
				if len(e.KnowledgePages) >= maxKnowledgePages {
					break
				}
				e.KnowledgePages = append(e.KnowledgePages, KnowledgePage{ID: pg.ID, Slug: pg.Slug, Title: pg.Title})
			}
			return nil
		})
	}
	_ = g.Wait() // no arm returns an error, so Wait cannot fail
}
