package datahubapi

// The business glossary: the hierarchy of nodes and terms, and the edits a
// steward makes to it (#1155 reads, #1158 writes).
//
// A glossary term and a glossary node are not a third and fourth governance
// vocabulary in the sense vocabulary.go means: they are a tree rather than a
// flat set, they are created under a parent, and DataHub retires both through
// one call. So they are shaped here instead — but on the same principle, only
// what has no existing route becomes one. A term's or node's definition is
// edited with PUT catalog/entity/description (upstream routes both glossary
// types to the GraphQL updateDescription mutation, which writes the
// glossaryTermInfo/glossaryNodeInfo "definition" field), and the tables a term
// covers come from the catalog search's glossary-term filters below.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// DataHub search filter fields for glossary-term usage, and the query
// parameters that reach them.
//
// filterFieldGlossaryTerms matches a dataset whose TABLE or whose COLUMN
// carries the term: DataHub folds field-level terms into the dataset's
// glossaryTerms index. filterFieldColumnGlossaryTerms matches column-level
// assignments only, which is what distinguishes the two — there is no
// table-level-only field. Verified against a live DataHub GMS: a term applied
// only to a column is returned by the first field as well as the second.
const (
	filterFieldGlossaryTerms       = "glossaryTerms"
	filterFieldColumnGlossaryTerms = "fieldGlossaryTerms"

	qpGlossaryTerm       = "glossary_term"
	qpColumnGlossaryTerm = "column_glossary_term"
)

// glossaryKind is one creatable kind of glossary entity. Terms and nodes differ
// only in what they are called and which writer method runs — both take a name,
// a definition, and an optional parent node — so they are one handler
// parameterized by this rather than a copy per kind.
type glossaryKind struct {
	// entityType names the kind in audit records ("glossaryTerm", "glossaryNode").
	entityType string
	// label names the kind in error messages ("glossary term", "glossary node").
	label string
	// create defines the entity and returns the URN DataHub assigned it.
	create func(ctx context.Context, w Writer, name, definition, parentNode string) (string, error)
}

var (
	// glossaryTermKind is a term: the named piece of business vocabulary itself.
	glossaryTermKind = glossaryKind{
		entityType: "glossaryTerm",
		label:      "glossary term",
		create: func(ctx context.Context, w Writer, name, definition, parentNode string) (string, error) {
			return w.CreateGlossaryTerm(ctx, name, definition, parentNode)
		},
	}

	// glossaryNodeKind is a node: the directory terms and other nodes sit in.
	glossaryNodeKind = glossaryKind{
		entityType: "glossaryNode",
		label:      "glossary node",
		create: func(ctx context.Context, w Writer, name, definition, parentNode string) (string, error) {
			return w.CreateGlossaryNode(ctx, name, definition, parentNode)
		},
	}
)

// glossaryRoutes registers the glossary hierarchy reads and edits under
// base/{conn}/catalog/glossary.
//
// The delete takes either kind on one route because upstream is one call
// (DeleteGlossaryEntity): a route per kind would differ only in which URN it
// accepts, which the shared URN validation already covers.
func (h *Handler) glossaryRoutes(mux *http.ServeMux, base string) {
	const path = "/{conn}/catalog/glossary"
	mux.HandleFunc("GET "+base+path+"/roots", h.browseGlossaryRoots)
	mux.HandleFunc("GET "+base+path+"/children", h.browseGlossaryChildren)
	mux.HandleFunc("GET "+base+path+"/parents", h.getGlossaryParents)
	mux.HandleFunc("POST "+base+path+"/nodes", func(w http.ResponseWriter, r *http.Request) {
		h.createGlossaryEntity(w, r, glossaryNodeKind)
	})
	mux.HandleFunc("POST "+base+path+"/terms", func(w http.ResponseWriter, r *http.Request) {
		h.createGlossaryEntity(w, r, glossaryTermKind)
	})
	mux.HandleFunc("DELETE "+base+path+"/entity", h.deleteGlossaryEntity)
}

// glossaryTermFilters turns the glossary-term query parameters into advanced
// search filters. A term URN is not validated here: an unknown or malformed
// value matches nothing, which is the same answer the search would give for a
// term no table carries, so there is nothing a 400 would protect.
func glossaryTermFilters(q url.Values) []semantic.FieldFilter {
	// A slice rather than a map: the filters are forwarded in this order, and a
	// map's iteration order would make the forwarded request differ run to run.
	params := []struct{ param, field string }{
		{qpGlossaryTerm, filterFieldGlossaryTerms},
		{qpColumnGlossaryTerm, filterFieldColumnGlossaryTerms},
	}
	var filters []semantic.FieldFilter
	for _, p := range params {
		if v := strings.TrimSpace(q.Get(p.param)); v != "" {
			filters = append(filters, semantic.FieldFilter{Field: p.field, Values: []string{v}})
		}
	}
	return filters
}

// --- hierarchy reads (#1155) ---

// glossaryRootsResponse is the top of the glossary tree. Root nodes and root
// terms carry separate totals because DataHub pages the two independently, so a
// single total would misreport how much is left of either.
type glossaryRootsResponse struct {
	Nodes      []semantic.GlossaryNode `json:"nodes"`
	NodesTotal int                     `json:"nodes_total"`
	Terms      []semantic.GlossaryTerm `json:"terms"`
	TermsTotal int                     `json:"terms_total"`
}

// browseGlossaryRoots returns the top of the glossary: the nodes and the terms
// with no parent. The two reads are independent upstream calls, so they run
// concurrently and the handler fails on the first error.
func (h *Handler) browseGlossaryRoots(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	offset := parseOffset(r.URL.Query().Get(qpOffset))
	limit := clampLimit(r.URL.Query().Get(qpLimit))

	var resp glossaryRootsResponse
	g, ctx := errgroup.WithContext(r.Context())
	g.Go(func() error {
		nodes, total, err := reader.ListRootGlossaryNodes(ctx, offset, limit)
		resp.Nodes, resp.NodesTotal = nodes, total
		if err != nil {
			return fmt.Errorf("root glossary nodes: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		terms, total, err := reader.ListRootGlossaryTerms(ctx, offset, limit)
		resp.Terms, resp.TermsTotal = terms, total
		if err != nil {
			return fmt.Errorf("root glossary terms: %w", err)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		writeError(w, http.StatusBadGateway, "glossary roots read failed: "+err.Error())
		return
	}
	resp.Nodes = orEmpty(resp.Nodes)
	resp.Terms = orEmpty(resp.Terms)
	writeJSON(w, http.StatusOK, resp)
}

// browseGlossaryChildren returns one page of the nodes and terms directly under a
// glossary node. Children are served from DataHub's asynchronously populated
// graph index, so an entity created moments earlier may not appear yet.
func (h *Handler) browseGlossaryChildren(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	urn, ok := requireURNParam(w, r, glossaryNodeURNTypes)
	if !ok {
		return
	}
	children, err := reader.ListGlossaryNodeChildren(r.Context(), urn,
		parseOffset(r.URL.Query().Get(qpOffset)), clampLimit(r.URL.Query().Get(qpLimit)))
	if err != nil {
		writeGlossaryReadError(w, "glossary children read failed", err)
		return
	}
	if children == nil {
		children = &semantic.GlossaryChildren{}
	}
	// Build the response rather than normalizing in place: the reader owns the
	// value it returned, and a caching implementation would see its own page
	// mutated.
	writeJSON(w, http.StatusOK, semantic.GlossaryChildren{
		Nodes: orEmpty(children.Nodes),
		Terms: orEmpty(children.Terms),
		Start: children.Start,
		Count: children.Count,
		Total: children.Total,
	})
}

// getGlossaryParents returns the ancestor nodes of a glossary term or node,
// direct parent first, so the UI can render the breadcrumb for an entity it
// reached without walking the tree.
func (h *Handler) getGlossaryParents(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	urn, ok := requireURNParam(w, r, glossaryEntityURNTypes)
	if !ok {
		return
	}
	chain, err := reader.GetGlossaryParentChain(r.Context(), urn)
	if err != nil {
		writeGlossaryReadError(w, "glossary parent chain read failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"parents": orEmpty(chain)})
}

// orEmpty returns a non-nil slice so a glossary response always renders a JSON
// array. The reader is an interface: a nil slice from any implementation would
// otherwise reach the client as null, which a caller has to special-case.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// writeGlossaryReadError maps an upstream glossary read failure to a status. A
// URN that DataHub does not know is a 404 rather than a 502: the request was
// well-formed and the backend answered, the entity simply is not there.
func writeGlossaryReadError(w http.ResponseWriter, label string, err error) {
	if errors.Is(err, dhclient.ErrNotFound) {
		writeError(w, http.StatusNotFound, label+": "+err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, label+": "+err.Error())
}

// --- edits (#1155 nodes, #1158 terms and delete) ---

// glossaryEntityRequest creates a glossary term or node. Definition is DataHub's
// name for a glossary entity's descriptive text (the glossaryTermInfo/
// glossaryNodeInfo aspect's "definition" field). An empty ParentNode creates the
// entity at the root of the glossary.
type glossaryEntityRequest struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
	ParentNode string `json:"parent_node,omitempty"`
}

// createGlossaryEntity adds a term or a node to the business glossary. Gated on
// the datahub_create grant, like every other create on this surface.
//
// The new entity is not immediately reachable through the hierarchy reads:
// children come from DataHub's asynchronously populated graph index, so a
// caller that re-browses the parent straight away may not see it. The returned
// URN is authoritative in the meantime.
func (h *Handler) createGlossaryEntity(w http.ResponseWriter, r *http.Request, kind glossaryKind) {
	auth, ok := h.authorizeWrite(w, r, datahubCreateTool)
	if !ok {
		return
	}
	var req glossaryEntityRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Definition = strings.TrimSpace(req.Definition)
	req.ParentNode = strings.TrimSpace(req.ParentNode)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, errNameRequired)
		return
	}
	// A malformed parent is a client error: reject it here rather than forwarding
	// it to DataHub, which would surface as a misleading 502.
	if req.ParentNode != "" && !isURNOfType(req.ParentNode, glossaryNodeURNTypes) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid parent node: %q must be a %s", req.ParentNode, urnHint(glossaryNodeURNTypes)))
		return
	}
	urn, err := kind.create(r.Context(), auth.writer, req.Name, req.Definition, req.ParentNode)
	h.audit(r, auth, datahubCreateTool, map[string]any{
		"entity_type": kind.entityType,
		"name":        req.Name,
		"parent_node": req.ParentNode,
	}, err)
	if err != nil {
		writeError(w, http.StatusBadGateway, kind.label+" create failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"urn": urn})
}

// deleteGlossaryEntity retires a glossary term or node. Gated on the
// datahub_delete grant.
//
// Nothing here checks what the entity holds or what carries it: DataHub removes
// a node without removing its children, which is why the portal shows a node's
// children and a term's usage before offering the delete.
func (h *Handler) deleteGlossaryEntity(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorizeWrite(w, r, datahubDeleteTool)
	if !ok {
		return
	}
	urn, ok := requireURNParam(w, r, glossaryEntityURNTypes)
	if !ok {
		return
	}
	err := auth.writer.DeleteGlossaryEntity(r.Context(), urn)
	h.audit(r, auth, datahubDeleteTool, map[string]any{
		"entity_type": datahubEntityType(urn),
		"urn":         urn,
	}, err)
	if err != nil {
		writeError(w, http.StatusBadGateway, "glossary entity delete failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
