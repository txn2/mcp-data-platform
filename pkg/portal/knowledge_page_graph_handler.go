package portal

import (
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// Knowledge-graph read bounds. The graph is a whole-corpus read, so both the page
// window and the total node count are capped and any truncation is reported in the
// response rather than silently applied.
const (
	// defaultGraphPages is the page window when the client does not ask for one.
	// It matches the knowledge-page list's honored cap, so the default graph covers
	// the same corpus the cards view shows on its first load.
	defaultGraphPages = knowledgepage.MaxHonoredLimit
	// maxGraphPages bounds the page window a client may request. It is bound to the
	// store's honored limit rather than set to a larger number of our own: the
	// store does not clamp an over-large limit down, it falls back to its small
	// default, so advertising a bigger window would return FEWER pages the more a
	// client asked for.
	maxGraphPages = knowledgepage.MaxHonoredLimit
	// maxGraphNodes bounds the total nodes returned. Pages are added first and are
	// bounded by maxGraphPages, so this cap only ever drops referenced entities.
	maxGraphNodes = 1500
)

// knowledgeGraphNode is one vertex: a knowledge page, or an entity some page
// references. Its id is the entity's serialized URN (mcp:knowledge_page:<id> for a
// page), so a page referenced by another page is the same vertex as that page in
// the corpus listing rather than a duplicate.
type knowledgeGraphNode struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
	// Exists is false for a reference whose target is gone (a broken reference).
	Exists bool `json:"exists"`
	// Page is true for a node backed by a knowledge page in this listing; the
	// fields below are populated only for those.
	Page      bool       `json:"page"`
	Tags      []string   `json:"tags,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// knowledgeGraphEdge is one stored reference, from the referencing page to the
// entity it references. Source and Target are node ids.
type knowledgeGraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	// Type is the reference's target type, so the client can filter edges by the
	// kind of thing they point at without re-deriving it from the node.
	Type string `json:"type"`
	// RefSource is how the reference came to be: promoted, manual, or inline.
	RefSource string `json:"ref_source"`
}

// knowledgeGraphResponse is the corpus-wide graph envelope.
type knowledgeGraphResponse struct {
	Nodes []knowledgeGraphNode `json:"nodes"`
	Edges []knowledgeGraphEdge `json:"edges"`
	// TotalPages is how many live pages match the filter, which exceeds the pages
	// actually in the graph when the window truncated the corpus.
	TotalPages int `json:"total_pages"`
	// Truncated reports that the graph is not the whole matching corpus; Notice
	// says what was left out. Truncation is never silent.
	Truncated bool   `json:"truncated"`
	Notice    string `json:"notice,omitempty"`
}

// knowledgeGraph handles GET /api/v1/portal/knowledge-pages/graph.
//
// @Summary      The knowledge corpus as a graph
// @Description  Returns the knowledge pages and the entities they reference as typed nodes and edges, for the portal's graph view. Entities the viewer cannot access are absent (neither node nor edge), the same visibility rule the per-page refs and backlinks reads apply. Node and page caps are reported via truncated/notice rather than applied silently.
// @Tags         Knowledge
// @Produce      json
// @Param        tag    query  string  false  "Only include pages carrying this tag"
// @Param        limit  query  int     false  "Maximum pages to include (default and max 100)"
// @Success      200  {object}  knowledgeGraphResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages/graph [get]
//
// The store capability is bound at registration time rather than asserted here,
// so this handler exists only where it can be served.
func (h *Handler) knowledgeGraph(w http.ResponseWriter, r *http.Request, reader knowledgepage.GraphReader) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	limit := clampGraphLimit(r.URL.Query().Get("limit"))
	pages, total, err := h.deps.KnowledgePageStore.List(r.Context(),
		knowledgepage.Filter{Tag: r.URL.Query().Get("tag"), Limit: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list knowledge pages")
		return
	}
	refs, err := reader.ListEntityRefsForPages(r.Context(), graphPageIDs(pages))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load page references")
		return
	}

	b := &knowledgeGraphBuilder{h: h, r: r, user: user, index: map[string]struct{}{}, resolved: map[string]resolvedRef{}}
	b.addPages(pages)
	b.addRefs(refs)
	writeJSON(w, http.StatusOK, b.response(total, len(pages)))
}

// clampGraphLimit parses the requested page window, falling back to the default
// for an absent, malformed, or out-of-range value.
func clampGraphLimit(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultGraphPages
	}
	if n > maxGraphPages {
		return maxGraphPages
	}
	return n
}

// graphPageIDs projects the page ids for the bulk reference read.
func graphPageIDs(pages []knowledgepage.Page) []string {
	ids := make([]string, 0, len(pages))
	for i := range pages {
		ids = append(ids, pages[i].ID)
	}
	return ids
}

// refURNs projects the serialized URN of each reference for the batch catalog
// name resolve.
func refURNs(refs []knowledgepage.EntityRef) []string {
	urns := make([]string, 0, len(refs))
	for i := range refs {
		urns = append(urns, refs[i].URN())
	}
	return urns
}

// knowledgeGraphBuilder accumulates the access-filtered node and edge set. Each
// reference URN is resolved once and cached, so an entity cited by many pages
// costs one store lookup rather than one per citing page.
type knowledgeGraphBuilder struct {
	h        *Handler
	r        *http.Request
	user     *User
	nodes    []knowledgeGraphNode
	index    map[string]struct{}
	edges    []knowledgeGraphEdge
	resolved map[string]resolvedRef
	// labels holds the catalog display names resolved in one batch for the whole
	// reference set, so a graph of catalog citations names them without one
	// upstream round trip per vertex.
	labels map[string]string
	// nodesDropped records that the node cap kept a referenced entity out.
	nodesDropped bool
}

// addPages seeds the graph with one node per listed page. Pages are always added
// (the page window is well below the node cap), so an isolated page with no
// references is still a visible vertex.
func (b *knowledgeGraphBuilder) addPages(pages []knowledgepage.Page) {
	for i := range pages {
		p := pages[i]
		id := knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetKnowledgePage, RefPageID: p.ID}.URN()
		updated := p.UpdatedAt
		b.index[id] = struct{}{}
		b.nodes = append(b.nodes, knowledgeGraphNode{
			ID: id, Type: knowledgepage.RefTargetKnowledgePage, Label: p.Title,
			Exists: true, Page: true, Tags: p.Tags, UpdatedAt: &updated,
		})
	}
}

// addRefs adds one node per distinct accessible reference target and one edge per
// stored reference. A reference the viewer cannot access contributes neither, so
// an inaccessible entity is absent from the graph rather than present-but-unlabeled.
func (b *knowledgeGraphBuilder) addRefs(refs []knowledgepage.EntityRef) {
	b.labels = b.h.catalogLabels(b.r.Context(), b.user, refURNs(refs))
	for i := range refs {
		ref := refs[i]
		target := ref.URN()
		source := knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetKnowledgePage, RefPageID: ref.PageID}.URN()
		if target == "" || target == source {
			// An unrecognized target has no vertex, and a page citing itself is a
			// self-loop that carries no structure and cannot be drawn as an edge
			// between two vertices.
			continue
		}
		// Check the node cap BEFORE resolving. Resolution is a per-entity store
		// read (an asset costs a fetch plus an access check), so resolving a
		// target the cap will refuse anyway would let a corpus with tens of
		// thousands of distinct citations turn one request into that many
		// queries. Bounding it here bounds the request's work to the cap.
		if !b.known(target) && b.atNodeCap() {
			continue
		}
		rr := b.resolve(target, ref)
		if !rr.Accessible {
			continue
		}
		if !b.ensureNode(target, rr) {
			continue
		}
		b.edges = append(b.edges, knowledgeGraphEdge{
			Source: source, Target: target, Type: ref.TargetType, RefSource: ref.Source,
		})
	}
}

// known reports whether the graph already contains a node for the URN.
func (b *knowledgeGraphBuilder) known(id string) bool {
	_, ok := b.index[id]
	return ok
}

// atNodeCap reports whether the graph is full, recording that a referenced
// entity was left out so the response says so.
func (b *knowledgeGraphBuilder) atNodeCap() bool {
	if len(b.nodes) < maxGraphNodes {
		return false
	}
	b.nodesDropped = true
	return true
}

// resolve resolves a reference URN to its label and accessibility, memoizing the
// result so a widely-cited entity is looked up once per request.
func (b *knowledgeGraphBuilder) resolve(urn string, ref knowledgepage.EntityRef) resolvedRef {
	if rr, ok := b.resolved[urn]; ok {
		return rr
	}
	rr := b.h.resolveRef(b.r, b.user, urn, ref, b.labels)
	b.resolved[urn] = rr
	return rr
}

// ensureNode adds the reference target as a node if it is not already one (it is
// when the target is a page in the listing, or is cited by an earlier page).
// It reports whether the node is present, which is false only when the node cap
// refused it — in which case its edge is dropped too, so the response never
// carries an edge to a vertex it does not contain.
func (b *knowledgeGraphBuilder) ensureNode(id string, rr resolvedRef) bool {
	if b.known(id) {
		return true
	}
	if b.atNodeCap() {
		return false
	}
	b.index[id] = struct{}{}
	b.nodes = append(b.nodes, knowledgeGraphNode{
		ID: id, Type: rr.Type, Label: rr.Label, Exists: rr.Exists,
	})
	return true
}

// response finalizes the envelope, reporting any truncation explicitly: pages
// left out of the window, entities left out by the node cap, or both.
func (b *knowledgeGraphBuilder) response(totalPages, shownPages int) knowledgeGraphResponse {
	resp := knowledgeGraphResponse{Nodes: b.nodes, Edges: b.edges, TotalPages: totalPages}
	if resp.Nodes == nil {
		resp.Nodes = []knowledgeGraphNode{}
	}
	if resp.Edges == nil {
		resp.Edges = []knowledgeGraphEdge{}
	}
	resp.Notice = graphTruncationNotice(totalPages, shownPages, b.nodesDropped)
	resp.Truncated = resp.Notice != ""
	return resp
}

// graphTruncationNotice describes what the caps left out, or "" when the graph is
// the whole matching corpus.
func graphTruncationNotice(totalPages, shownPages int, nodesDropped bool) string {
	pagesDropped := totalPages > shownPages
	switch {
	case pagesDropped && nodesDropped:
		return "Showing the " + strconv.Itoa(shownPages) + " most recently updated of " + strconv.Itoa(totalPages) +
			" pages, and the graph reached its " + strconv.Itoa(maxGraphNodes) +
			"-node limit, so some referenced entities are not drawn. Filter by tag to see the rest."
	case pagesDropped:
		return "Showing the " + strconv.Itoa(shownPages) + " most recently updated of " + strconv.Itoa(totalPages) +
			" pages. Filter by tag to see the rest."
	case nodesDropped:
		return "The graph reached its " + strconv.Itoa(maxGraphNodes) +
			"-node limit, so some referenced entities are not drawn. Filter by tag to narrow it."
	default:
		return ""
	}
}
