// Package datahubapi serves the portal's DataHub Catalog and Context Docs REST
// surface (#718): browse/search/read over DataHub connections plus catalog
// metadata edits and context-document CRUD, gated per-persona. It is a separate
// package from pkg/portal so the portal package stays within its size budget
// (#594); it plugs into the portal by registering its routes on the portal mux
// via a registrar hook, and reads the authenticated user through portal.GetUser.
package datahubapi

import (
	"context"
	"fmt"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// Connection identifies a DataHub connection the portal can browse, and whether
// it is write-enabled (a connection with read_only=true is read-only no matter
// what tools the caller's persona grants).
type Connection struct {
	Name     string `json:"name"`
	Writable bool   `json:"writable"`
}

// Reader is the read surface the Catalog and Context Docs tabs need over a single
// DataHub connection. The semantic DataHub adapter (pkg/semantic/datahub)
// satisfies it directly, so the reader returns the semantic types the enrichment
// layer already uses rather than a portal-local mirror.
type Reader interface {
	ResolveURN(ctx context.Context, urn string) (*semantic.TableIdentifier, error)
	GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error)
	GetColumnsContext(ctx context.Context, table semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error)
	SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error)
	SearchTags(ctx context.Context, query string, limit int) ([]semantic.EntityRef, error)
	SearchGlossaryTerms(ctx context.Context, query string, limit int) ([]semantic.EntityRef, error)
	ListDomains(ctx context.Context) ([]semantic.EntityRef, error)
	ListRootGlossaryNodes(ctx context.Context, offset, limit int) ([]semantic.GlossaryNode, int, error)
	ListRootGlossaryTerms(ctx context.Context, offset, limit int) ([]semantic.GlossaryTerm, int, error)
	ListGlossaryNodeChildren(ctx context.Context, nodeURN string, offset, limit int) (*semantic.GlossaryChildren, error)
	GetGlossaryParentChain(ctx context.Context, urn string) ([]semantic.GlossaryNode, error)
	// GetGlossaryTerm reads one term by URN (#1159). It is the only by-URN read
	// any governance vocabulary has upstream: a tag or a domain resolves only by
	// listing its vocabulary and matching, which is what shapes the label
	// resolver in labels.go and the deep-link paths in the portal.
	GetGlossaryTerm(ctx context.Context, urn string) (*semantic.GlossaryTerm, error)
	SearchDocuments(ctx context.Context, query string, limit int) ([]semantic.DocumentResult, error)
	BrowseDocuments(ctx context.Context, offset, limit int) ([]semantic.DocumentResult, int, error)
	GetDocument(ctx context.Context, urn string) (*semantic.DocumentResult, error)
	// GetRelatedDocuments returns the context documents attached to one entity
	// (#1158), which the corpus-wide browse and search reads cannot express.
	GetRelatedDocuments(ctx context.Context, urn string) ([]semantic.DocumentResult, error)
}

// OwnerChange is an owner to add along with its ownership type.
type OwnerChange struct {
	OwnerURN      string `json:"owner_urn"`
	OwnershipType string `json:"ownership_type,omitempty"`
}

// DocumentInput is a context-document create/update request. An empty ID creates
// a new document linked to EntityURN; a populated ID updates in place (EntityURN
// is then ignored, matching the upstream upsert contract).
type DocumentInput struct {
	ID        string `json:"id,omitempty"`
	EntityURN string `json:"entity_urn,omitempty"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Category  string `json:"category,omitempty"`
}

// Writer is the write surface for catalog metadata edits and context-doc CRUD
// over a single write-enabled connection. Tags, glossary terms, and owners are
// applied as batched add/remove sets rather than per-item calls: per-item writes
// read-modify-write DataHub's eventually consistent aspects and clobber each
// other (#721/#729), so the batched forms are the only lossless primitives.
type Writer interface {
	UpdateDescription(ctx context.Context, urn, description string) error
	ApplyTagChanges(ctx context.Context, urn string, add, remove []string) error
	ApplyGlossaryTermChanges(ctx context.Context, urn string, add, remove []string) error
	ApplyOwnerChanges(ctx context.Context, urn string, add []OwnerChange, remove []string) error
	SetDomain(ctx context.Context, entityURN, domainURN string) error
	UnsetDomain(ctx context.Context, entityURN string) error
	// CreateGlossaryNode adds a directory to the business glossary, under
	// parentNode or at the root when it is empty, returning the new node's URN
	// (#1155).
	CreateGlossaryNode(ctx context.Context, name, definition, parentNode string) (string, error)
	// CreateGlossaryTerm adds a term to the business glossary, under parentNode
	// or at the root when it is empty, returning the new term's URN (#1158).
	// Editing a term's definition is UpdateDescription with the term's URN,
	// which is why there is no term-specific update here.
	CreateGlossaryTerm(ctx context.Context, name, definition, parentNode string) (string, error)
	// DeleteGlossaryEntity retires a glossary term or node. Upstream is one call
	// for both kinds, and it removes neither a node's children nor a term's
	// assignments, which is why the portal shows both before offering the delete.
	DeleteGlossaryEntity(ctx context.Context, urn string) error
	// CreateTag defines a new tag and returns its URN (#1156). Editing a tag's
	// description is UpdateDescription with the tag's URN, which is why there is
	// no tag-specific update here.
	CreateTag(ctx context.Context, name, description string) (string, error)
	// DeleteTag removes a tag definition. Nothing here checks what still carries
	// the tag, which is why the portal shows a tag's current usage before
	// offering the delete.
	DeleteTag(ctx context.Context, tagURN string) error
	// CreateDomain defines a new domain and returns its URN (#1157). Editing a
	// domain's description is UpdateDescription with the domain's URN, and
	// moving a table into or out of a domain is SetDomain/UnsetDomain, which is
	// why neither has a domain-specific method here.
	CreateDomain(ctx context.Context, name, description string) (string, error)
	// DeleteDomain removes a domain definition. Nothing here clears the domain
	// from the tables that carry it, which is why the portal shows a domain's
	// current membership before offering the delete.
	DeleteDomain(ctx context.Context, domainURN string) error
	UpsertContextDocument(ctx context.Context, in DocumentInput) (*semantic.DocumentResult, error)
	DeleteContextDocument(ctx context.Context, documentID string) error
}

// Bridge exposes read/write access to the configured DataHub connections. Writer
// returns ok=false for an unknown or read-only connection, so a write against a
// read-only connection is rejected before any persona check.
type Bridge interface {
	Connections() []Connection
	Reader(conn string) (Reader, bool)
	Writer(conn string) (Writer, bool)
}

// StaticBridge is a Bridge assembled once at wiring time from a fixed set of
// per-connection read/write surfaces.
type StaticBridge struct {
	conns   []Connection
	readers map[string]Reader
	writers map[string]Writer
}

// NewStaticBridge returns an empty StaticBridge ready for Add.
func NewStaticBridge() *StaticBridge {
	return &StaticBridge{readers: map[string]Reader{}, writers: map[string]Writer{}}
}

// Add registers a connection's surfaces. A nil writer marks the connection
// read-only (no writer is exposed for it).
func (b *StaticBridge) Add(name string, reader Reader, writer Writer) {
	writable := writer != nil
	b.conns = append(b.conns, Connection{Name: name, Writable: writable})
	b.readers[name] = reader
	if writable {
		b.writers[name] = writer
	}
}

// Empty reports whether no connection was added.
func (b *StaticBridge) Empty() bool { return len(b.conns) == 0 }

// Connections returns the registered connections.
func (b *StaticBridge) Connections() []Connection { return b.conns }

// Reader returns the read surface for a connection, ok=false if unknown.
func (b *StaticBridge) Reader(conn string) (Reader, bool) {
	r, ok := b.readers[conn]
	return r, ok
}

// Writer returns the write surface for a connection, ok=false if unknown or read-only.
func (b *StaticBridge) Writer(conn string) (Writer, bool) {
	w, ok := b.writers[conn]
	return w, ok
}

// BuildConnection builds the read (and, when the connection is write-enabled, the
// write) surfaces for a live DataHub client. A read-only connection returns a nil
// writer. Both surfaces share the one client.
func BuildConnection(client *dhclient.Client, semanticPlatform string, catalogMapping map[string]string, readOnly bool) (Reader, Writer, error) {
	reader, err := datahubsemantic.NewWithClient(datahubsemantic.Config{
		Platform:       semanticPlatform,
		CatalogMapping: catalogMapping,
	}, client)
	if err != nil {
		return nil, nil, fmt.Errorf("building datahub reader: %w", err)
	}
	if readOnly {
		return reader, nil, nil
	}
	return reader, clientWriter{w: knowledgekit.NewDataHubClientWriter(client)}, nil
}

// clientWriter adapts the toolkit's batched, clobber-safe DataHubClientWriter to
// the Writer interface, converting the package-local owner and document DTOs to
// the writer/upstream types.
type clientWriter struct {
	w *knowledgekit.DataHubClientWriter
}

const errWriter = "datahub writer: %w"

// UpdateDescription sets an entity's description.
func (cw clientWriter) UpdateDescription(ctx context.Context, urn, description string) error {
	if err := cw.w.UpdateDescription(ctx, urn, description); err != nil {
		return fmt.Errorf(errWriter, err)
	}
	return nil
}

// ApplyTagChanges applies a batched add/remove set of tags.
func (cw clientWriter) ApplyTagChanges(ctx context.Context, urn string, add, remove []string) error {
	if err := cw.w.ApplyTagChanges(ctx, urn, add, remove); err != nil {
		return fmt.Errorf(errWriter, err)
	}
	return nil
}

// ApplyGlossaryTermChanges applies a batched add/remove set of glossary terms.
func (cw clientWriter) ApplyGlossaryTermChanges(ctx context.Context, urn string, add, remove []string) error {
	if err := cw.w.ApplyGlossaryTermChanges(ctx, urn, add, remove); err != nil {
		return fmt.Errorf(errWriter, err)
	}
	return nil
}

// ApplyOwnerChanges converts the DTOs and applies the owner set.
func (cw clientWriter) ApplyOwnerChanges(ctx context.Context, urn string, add []OwnerChange, remove []string) error {
	changes := make([]knowledgekit.OwnerChange, len(add))
	for i, o := range add {
		changes[i] = knowledgekit.OwnerChange{OwnerURN: o.OwnerURN, OwnershipType: o.OwnershipType}
	}
	if err := cw.w.ApplyOwnerChanges(ctx, urn, changes, remove); err != nil {
		return fmt.Errorf(errWriter, err)
	}
	return nil
}

// SetDomain assigns a domain to an entity.
func (cw clientWriter) SetDomain(ctx context.Context, entityURN, domainURN string) error {
	if err := cw.w.SetDomain(ctx, entityURN, domainURN); err != nil {
		return fmt.Errorf(errWriter, err)
	}
	return nil
}

// UnsetDomain removes the domain from an entity.
func (cw clientWriter) UnsetDomain(ctx context.Context, entityURN string) error {
	if err := cw.w.UnsetDomain(ctx, entityURN); err != nil {
		return fmt.Errorf(errWriter, err)
	}
	return nil
}

// CreateGlossaryNode creates a glossary node and returns its URN.
func (cw clientWriter) CreateGlossaryNode(ctx context.Context, name, definition, parentNode string) (string, error) {
	urn, err := cw.w.CreateGlossaryNode(ctx, name, definition, parentNode)
	if err != nil {
		return "", fmt.Errorf(errWriter, err)
	}
	return urn, nil
}

// CreateGlossaryTerm creates a glossary term and returns its URN.
func (cw clientWriter) CreateGlossaryTerm(ctx context.Context, name, definition, parentNode string) (string, error) {
	urn, err := cw.w.CreateGlossaryTerm(ctx, name, definition, parentNode)
	if err != nil {
		return "", fmt.Errorf(errWriter, err)
	}
	return urn, nil
}

// DeleteGlossaryEntity removes a glossary term or node.
func (cw clientWriter) DeleteGlossaryEntity(ctx context.Context, urn string) error {
	if err := cw.w.DeleteGlossaryEntity(ctx, urn); err != nil {
		return fmt.Errorf(errWriter, err)
	}
	return nil
}

// CreateTag creates a tag definition and returns its URN.
func (cw clientWriter) CreateTag(ctx context.Context, name, description string) (string, error) {
	urn, err := cw.w.CreateTag(ctx, name, description)
	if err != nil {
		return "", fmt.Errorf(errWriter, err)
	}
	return urn, nil
}

// DeleteTag removes a tag definition.
func (cw clientWriter) DeleteTag(ctx context.Context, tagURN string) error {
	if err := cw.w.DeleteTag(ctx, tagURN); err != nil {
		return fmt.Errorf(errWriter, err)
	}
	return nil
}

// CreateDomain creates a domain definition and returns its URN.
func (cw clientWriter) CreateDomain(ctx context.Context, name, description string) (string, error) {
	urn, err := cw.w.CreateDomain(ctx, name, description)
	if err != nil {
		return "", fmt.Errorf(errWriter, err)
	}
	return urn, nil
}

// DeleteDomain removes a domain definition.
func (cw clientWriter) DeleteDomain(ctx context.Context, domainURN string) error {
	if err := cw.w.DeleteDomain(ctx, domainURN); err != nil {
		return fmt.Errorf(errWriter, err)
	}
	return nil
}

// UpsertContextDocument creates or updates a context document.
func (cw clientWriter) UpsertContextDocument(ctx context.Context, in DocumentInput) (*semantic.DocumentResult, error) {
	doc, err := cw.w.UpsertContextDocument(ctx, in.EntityURN, types.ContextDocumentInput{
		ID:       in.ID,
		Title:    in.Title,
		Content:  in.Content,
		Category: in.Category,
	})
	if err != nil {
		return nil, fmt.Errorf(errWriter, err)
	}
	return contextDocToDocumentResult(doc), nil
}

// DeleteContextDocument removes a context document by id.
func (cw clientWriter) DeleteContextDocument(ctx context.Context, documentID string) error {
	if err := cw.w.DeleteContextDocument(ctx, documentID); err != nil {
		return fmt.Errorf(errWriter, err)
	}
	return nil
}

// contextDocToDocumentResult maps an upstream context document to the semantic
// DocumentResult the read path returns, so create/update responses share the
// read shape.
func contextDocToDocumentResult(d *types.ContextDocument) *semantic.DocumentResult {
	if d == nil {
		return nil
	}
	return &semantic.DocumentResult{
		URN:     dhclient.BuildDocumentURN(d.ID),
		Title:   d.Title,
		SubType: d.Category,
		Body:    d.Content,
	}
}
