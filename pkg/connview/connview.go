// Package connview builds the list_connections view: the connections a
// deployment holds, each enriched with the canonical knowledge pages that
// reference it (#634). It lives outside pkg/platform so that package stays
// within its size budget, and depends only on narrow capabilities (a source
// resolver, a knowledge-page reverse lookup and the connection store) rather
// than on the platform itself.
//
// A connection exists because the connection store holds a row for it, not
// because this process has built one. Several replicas run over one database,
// and a connection saved through one of them is a row before it is anything in
// the others' memory, so an enumeration answered from what this process serves
// answers for this process rather than for the deployment: an operator who
// adds a connection and asks what exists is told a different thing depending
// on which replica the load balancer picked (#1757). The store is therefore
// what is enumerated, and what this process serves supplies the per-process
// detail the row cannot carry — a connection's health is the outcome of calls
// this replica made.
package connview

import (
	"context"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/connid"
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

// ToolDescription is what list_connections tells a caller it answers. It lives
// with the view it describes rather than with the tool registration, so the
// sentence about a field and the field itself are added in one place.
const ToolDescription = "List all configured data connections across toolkits (Trino, DataHub, S3, etc.). " +
	"Each connection includes a count and a bounded sample of the canonical knowledge pages that document it. " +
	"Where the kind has the notion, a connection also reports read_only: true means write-class calls are refused " +
	"on it, so plan the write path BEFORE staging data rather than meeting the refusal partway through. " +
	"read_only: false is not scoped to whatever catalog, schema or bucket the connection declares: those are the " +
	"defaults a call uses when it names none, not a boundary, and a fully qualified statement reaches wherever " +
	"the connection's upstream identity may reach."

// readOnlyReporter is the optional capability of a toolkit that serves ONE
// connection and so lists none: it answers for that connection's writability
// directly. Declared here as an interface rather than reached by importing the
// toolkit, which this package deliberately does not do.
type readOnlyReporter interface {
	IsReadOnly() bool
}

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
	// ReadOnly is true when this connection refuses write-class calls, and is
	// omitted entirely on a kind with no such notion (#1805).
	ReadOnly *bool `json:"read_only,omitempty"`
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

// SourceResolver resolves a connection's DataHub source name (empty when none).
type SourceResolver interface {
	DataHubSourceName(kind, name string) string
}

// PageLookup is the knowledge-page reverse lookup: the pages referencing a target.
type PageLookup interface {
	ListPagesReferencing(ctx context.Context, ref knowledgepage.EntityRef) ([]knowledgepage.PageRef, error)
}

// Stored is one connection as the connection store holds it. The fields are
// the ones a row answers on its own: what the connection is called, what it
// is, and — for a kind whose operations the catalog holds — how large its
// surface is. Health is absent by construction, being the outcome of calls a
// particular replica made.
type Stored struct {
	Kind           string
	Name           string
	Description    string
	CatalogID      string
	OperationCount int
	// ReadOnly is the stored connection's writability, nil on a kind that has
	// no such setting. It is carried here as well as on the live listing so
	// the answer does not depend on which replica the caller reached: a
	// connection added on another replica is a row here before it is anything
	// this process serves (#1757), and a surface that reported its writability
	// only when it happened to be local would report two different things
	// about one connection (#1805).
	ReadOnly *bool
}

// StoreLister lists every connection the store holds, across kinds. A
// deployment that keeps connections in its configuration file alone wires
// none, and the enumeration is what this process serves.
type StoreLister interface {
	ListStoredConnections(ctx context.Context) ([]Stored, error)
}

// Deps are Build's collaborators, each optional: a nil Source omits the
// DataHub source names, a nil Pages skips the knowledge enrichment, a nil
// Permit enumerates every connection (what a system caller with no persona
// needs), and a nil Stored enumerates only what this process serves.
type Deps struct {
	Source SourceResolver
	Pages  PageLookup
	Permit Permit
	Stored StoreLister
}

// Build enumerates the connections the deployment holds that deps.Permit
// admits and enriches each with the knowledge pages that reference it (bounded
// by maxKnowledgePages). Every field of deps may be nil.
//
// What this process serves is enumerated first, because it answers with the
// per-process detail a row cannot carry, and the store supplies every
// connection left — a connection saved through another replica, which is a row
// here before it is anything else (#1757). Nothing is built to list it: a
// connection is put in service by the call that addresses it, which reads the
// same row.
//
// A connection the permit rejects is counted, not merely dropped: the count
// (and the notice built from it) is what distinguishes "this deployment has one
// connection" from "you may see one of its connections".
func Build(ctx context.Context, toolkits []registry.Toolkit, deps Deps) Output {
	entries := make([]Entry, 0, len(toolkits))
	// seen holds every connection this process serves, by the index of its
	// entry or withheldEntry when the permit hid it. The store half reads it to
	// tell a connection it has already reported from one it has not — a
	// connection hidden from this caller is reported neither twice nor as two
	// withheld connections, which is what the notice counts.
	seen := make(map[string]int)
	withheld := 0
	for _, tk := range toolkits {
		var n int
		if lister, ok := tk.(toolkit.ConnectionLister); ok {
			entries, n = appendFromLister(entries, seen, tk, lister, deps.Source, deps.Permit)
		} else {
			entries, n = appendFallback(entries, seen, tk, deps.Source, deps.Permit)
		}
		withheld += n
	}
	entries, n := appendStored(ctx, entries, seen, deps)
	withheld += n
	enrichWithKnowledge(ctx, deps.Pages, entries)
	return Output{Connections: entries, Count: len(entries), Withheld: withheld}
}

// withheldEntry marks a connection this process serves that the permit hid, so
// it has no entry to index.
const withheldEntry = -1

// seenKey is how a connection is keyed while one enumeration runs: the pair
// that identifies it, which is also the pair a knowledge-page reference and the
// connection store are keyed by.
func seenKey(kind, name string) string { return kind + "/" + name }

// appendStored reports every connection the store holds: the ones this process
// does not serve are appended, and the ones it does take the row's description.
//
// The description is the row's because that is where an operator writes it —
// the column, not the configuration a toolkit parses, which is why a served api
// connection reported its base URL while the admin page showed the sentence
// somebody typed. Answering it from the row is what makes one connection read
// the same on both surfaces and on every replica. Everything else about a
// served connection stays what it derived when it built it: the health of calls
// this replica made, and the surface it read.
//
// A store that cannot answer degrades the enumeration to what this process
// serves rather than failing it: a caller asking what exists is better served
// by the connections this replica can name than by an error, and the refusal
// it would otherwise get says nothing about the connection it was looking for.
func appendStored(ctx context.Context, entries []Entry, seen map[string]int, deps Deps) (out []Entry, withheld int) {
	if deps.Stored == nil {
		return entries, 0
	}
	stored, err := deps.Stored.ListStoredConnections(ctx)
	if err != nil {
		slog.WarnContext(ctx, "listing the connection store failed; reporting the connections this replica serves",
			"error", logsan.SanitizeForLog(err.Error()))
		return entries, 0
	}
	for _, sc := range stored {
		if i, ok := seen[seenKey(sc.Kind, sc.Name)]; ok {
			// A connection this process serves is already accounted for,
			// whether it was reported or withheld.
			if i != withheldEntry && sc.Description != "" {
				entries[i].Description = sc.Description
			}
			continue
		}
		if !deps.Permit.allows(sc.Kind, sc.Name) {
			withheld++
			continue
		}
		e := Entry{
			Kind:           sc.Kind,
			Name:           sc.Name,
			Connection:     sc.Name,
			Reference:      knowledgepage.ConnectionRef(sc.Kind, sc.Name),
			Description:    sc.Description,
			CatalogID:      sc.CatalogID,
			OperationCount: sc.OperationCount,
			ReadOnly:       sc.ReadOnly,
		}
		if deps.Source != nil {
			e.DataHubSourceName = deps.Source.DataHubSourceName(sc.Kind, sc.Name)
		}
		entries = append(entries, e)
	}
	return entries, withheld
}

func appendFromLister(
	entries []Entry, seen map[string]int, tk registry.Toolkit, lister toolkit.ConnectionLister,
	src SourceResolver, permit Permit,
) (out []Entry, withheld int) {
	for _, conn := range lister.ListConnections() {
		if !permit.allows(tk.Kind(), conn.Name) {
			seen[seenKey(tk.Kind(), conn.Name)] = withheldEntry
			withheld++
			continue
		}
		seen[seenKey(tk.Kind(), conn.Name)] = len(entries)
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
			ReadOnly:       conn.ReadOnly,
		}
		if src != nil {
			e.DataHubSourceName = src.DataHubSourceName(tk.Kind(), conn.Name)
		}
		entries = append(entries, e)
	}
	return entries, withheld
}

func appendFallback(
	entries []Entry, seen map[string]int, tk registry.Toolkit, src SourceResolver, permit Permit,
) (out []Entry, withheld int) {
	kind := tk.Kind()
	if !dataKinds[kind] {
		return entries, 0
	}
	// connid derives both names, so discovery keys on exactly what the
	// authorizer checks and what the source map is keyed by (#1396).
	c := connid.NewResolver([]registry.Toolkit{tk}, nil).ByInstance(kind, connid.Instance(tk.Name()))
	if !permit.allows(kind, string(c.Bound)) {
		seen[seenKey(kind, tk.Name())] = withheldEntry
		return entries, 1
	}
	seen[seenKey(kind, tk.Name())] = len(entries)
	// Name and Reference stay on the INSTANCE name: the reference FKs to the
	// connection_instances row the backfill seeds, and that row's name is the
	// key a stored connection is merged back into toolkit config under.
	//
	// Connection is the BOUND name, which is what a caller puts in a tool
	// call's connection argument. Reporting the raw connection_name here left
	// it empty for every toolkit that sets none, while the name such a call
	// actually binds is the instance.
	e := Entry{
		Kind: kind, Name: tk.Name(), Connection: string(c.Bound),
		Reference: knowledgepage.ConnectionRef(kind, tk.Name()),
	}
	// A toolkit serving one connection reports its writability for the whole
	// toolkit, which is what a per-connection listing reports per connection.
	// Asking here keeps this path's answer the same as the stored path's for
	// the same connection (#1805).
	if reporter, ok := tk.(readOnlyReporter); ok {
		readOnly := reporter.IsReadOnly()
		e.ReadOnly = &readOnly
	}
	if src != nil {
		e.DataHubSourceName = src.DataHubSourceName(kind, string(c.Bound))
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
