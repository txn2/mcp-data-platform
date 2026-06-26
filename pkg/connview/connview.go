// Package connview builds the list_connections view: the configured connections
// across toolkits, each enriched with the canonical knowledge pages that reference
// it (#634). It lives outside pkg/platform so that package stays within its size
// budget, and depends only on narrow capabilities (a source resolver and a
// knowledge-page reverse lookup) rather than on the platform itself.
package connview

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// maxKnowledgePages bounds how many referencing pages are listed per connection, so
// list_connections output stays small even when a connection is widely documented.
// The full total is still reported via Entry.KnowledgePageCount.
const maxKnowledgePages = 5

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

// Output is the JSON response for the list_connections tool.
type Output struct {
	Connections []Entry `json:"connections"`
	Count       int     `json:"count"`
}

// SourceResolver resolves a connection's DataHub source name (empty when none).
type SourceResolver interface {
	DataHubSourceName(kind, name string) string
}

// PageLookup is the knowledge-page reverse lookup: the pages referencing a target.
type PageLookup interface {
	ListPagesReferencing(ctx context.Context, ref knowledgepage.EntityRef) ([]knowledgepage.PageRef, error)
}

// Build enumerates connections across the toolkits and enriches each with the
// knowledge pages that reference it (bounded by maxKnowledgePages). src and pages
// may be nil; a nil page lookup simply skips the knowledge enrichment.
func Build(ctx context.Context, toolkits []registry.Toolkit, src SourceResolver, pages PageLookup) Output {
	entries := make([]Entry, 0, len(toolkits))
	for _, tk := range toolkits {
		if lister, ok := tk.(toolkit.ConnectionLister); ok {
			entries = appendFromLister(entries, tk, lister, src)
		} else {
			entries = appendFallback(entries, tk, src)
		}
	}
	enrichWithKnowledge(ctx, pages, entries)
	return Output{Connections: entries, Count: len(entries)}
}

func appendFromLister(entries []Entry, tk registry.Toolkit, lister toolkit.ConnectionLister, src SourceResolver) []Entry {
	for _, conn := range lister.ListConnections() {
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
	return entries
}

func appendFallback(entries []Entry, tk registry.Toolkit, src SourceResolver) []Entry {
	kind := tk.Kind()
	if !dataKinds[kind] {
		return entries
	}
	e := Entry{Kind: kind, Name: tk.Name(), Connection: tk.Connection(), Reference: knowledgepage.ConnectionRef(kind, tk.Name())}
	if src != nil {
		e.DataHubSourceName = src.DataHubSourceName(kind, tk.Name())
	}
	return append(entries, e)
}

// enrichWithKnowledge fills each entry's KnowledgePageCount and a bounded sample of
// referencing pages. A nil lookup or per-connection failure is skipped, never fatal.
// Knowledge pages are org-shared, so their titles are safe to surface here.
func enrichWithKnowledge(ctx context.Context, pages PageLookup, entries []Entry) {
	if pages == nil {
		return
	}
	for i := range entries {
		e := &entries[i]
		refs, err := pages.ListPagesReferencing(ctx, knowledgepage.EntityRef{
			TargetType:     knowledgepage.RefTargetConnection,
			ConnectionKind: e.Kind,
			ConnectionName: e.Name,
		})
		if err != nil || len(refs) == 0 {
			continue
		}
		e.KnowledgePageCount = len(refs)
		for _, pg := range refs {
			if len(e.KnowledgePages) >= maxKnowledgePages {
				break
			}
			e.KnowledgePages = append(e.KnowledgePages, KnowledgePage{ID: pg.ID, Slug: pg.Slug, Title: pg.Title})
		}
	}
}
