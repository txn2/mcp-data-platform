package catalogapi

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// lineageDepth is how far a dataset's lineage is walked each way.
const lineageDepth = 3

// The page a dataset search returns when it names none, and the most it may.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// DatasetList is one page of datasets.
type DatasetList struct {
	Results []catalog.DatasetSummary `json:"results"`
}

// searchDatasets handles GET /api/v1/portal/catalog/datasets.
//
// @Summary      Search catalog datasets
// @Description  Returns a page of the datasets the caller's persona reaches that match q (the name, the path or the description, ignoring case; empty or * matches every dataset), optionally on one connection and carrying every tag named.
// @Tags         Data Catalog
// @Produce      json
// @Param        q                query  string  false  "Text to match"
// @Param        connection_kind  query  string  false  "Connection kind: trino, api or graphql"
// @Param        connection       query  string  false  "Connection name"
// @Param        tag              query  string  false  "A tag every match carries; repeatable"
// @Param        limit            query  int     false  "Page size (default 50, at most 200)"
// @Param        offset           query  int     false  "Page offset"
// @Success      200  {object}  DatasetList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/datasets [get]
func (h *Handler) searchDatasets(w http.ResponseWriter, r *http.Request) {
	c, ok := h.reader(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	query := catalog.DatasetQuery{
		Text:           q.Get("q"),
		ConnectionKind: q.Get("connection_kind"),
		Connection:     q.Get("connection"),
		Tags:           q["tag"],
		Limit:          pageLimit(q.Get("limit")),
		Offset:         max(atoi(q.Get("offset")), 0),
	}
	if !c.admin {
		query.Scoped = true
		for _, conn := range h.reachable(c) {
			query.Within = append(query.Within, conn.Kind+"/"+conn.Name)
		}
	}
	results, err := h.deps.Store.SearchDatasets(r.Context(), query)
	if err != nil {
		writeStoreError(w, err, "datasets")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, DatasetList{Results: results})
}

// pageLimit reads a page size, defaulting and capping it.
func pageLimit(s string) int {
	n := atoi(s)
	if n <= 0 {
		return defaultLimit
	}
	return min(n, maxLimit)
}

// atoi reads an integer parameter, zero when absent or malformed.
func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// BrowseLevel is one level of a connection's datasets.
type BrowseLevel struct {
	Entries []catalog.BrowseEntry `json:"entries"`
}

// browse handles GET /api/v1/portal/catalog/browse.
//
// @Summary      Browse a connection's datasets
// @Description  Returns one level of a connection's datasets: each path group (a Trino catalog, then a schema) with how many datasets it holds, then the datasets directly under the path. An empty path lists the top level.
// @Tags         Data Catalog
// @Produce      json
// @Param        connection_kind  query  string  true   "Connection kind"
// @Param        connection       query  string  true   "Connection name"
// @Param        path             query  string  false  "Dotted path in reference form"
// @Success      200  {object}  BrowseLevel
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/browse [get]
func (h *Handler) browse(w http.ResponseWriter, r *http.Request) {
	c, ok := h.reader(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	conn := Connection{Kind: q.Get("connection_kind"), Name: q.Get("connection")}
	path, err := splitPath(q.Get("path"))
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "path: "+err.Error())
		return
	}
	if conn.Kind == "" || conn.Name == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "connection_kind and connection are required")
		return
	}
	if !c.reaches(probeRef(conn)) {
		httpjson.WriteError(w, http.StatusNotFound, "connection not found")
		return
	}
	entries, err := h.deps.Store.Browse(r.Context(), catalog.BrowseQuery{
		ConnectionKind: conn.Kind, Connection: conn.Name, Prefix: path,
	})
	if err != nil {
		writeStoreError(w, err, "connection")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, BrowseLevel{Entries: entries})
}

// DatasetView is a dataset as the dataset page shows it: its columns and
// curation, the lineage around it the caller may see, the names of the
// definitions its curation refers to, and whether the caller may edit it.
type DatasetView struct {
	*catalog.Dataset
	Upstream   []catalog.Hop `json:"upstream"`
	Downstream []catalog.Hop `json:"downstream"`
	// Names maps each glossary term and domain reference the curation holds
	// to its display name.
	Names    map[string]string `json:"names"`
	Writable bool              `json:"writable"`
}

// getDataset handles GET /api/v1/portal/catalog/dataset.
//
// @Summary      Get a catalog dataset
// @Description  Returns a dataset with its columns and every aspect of their curation, the lineage edges within three hops either way whose far end the caller's persona reaches, and the names of the glossary terms and domain its curation refers to. A dataset on a connection the persona does not reach is not found.
// @Tags         Data Catalog
// @Produce      json
// @Param        ref  query  string  true  "Dataset reference (mcp:dataset:(kind,connection,path))"
// @Success      200  {object}  DatasetView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/dataset [get]
func (h *Handler) getDataset(w http.ResponseWriter, r *http.Request) {
	c, ok := h.reader(w, r)
	if !ok {
		return
	}
	ref, ok := parseRef(w, r)
	if !ok {
		return
	}
	if ref.Kind != catalog.KindDataset || !c.reaches(ref.String()) {
		httpjson.WriteError(w, http.StatusNotFound, "dataset not found")
		return
	}
	ctx := r.Context()
	d, err := h.deps.Store.GetDataset(ctx, ref)
	if err != nil {
		writeStoreError(w, err, "dataset")
		return
	}
	view := DatasetView{Dataset: d, Names: h.names(ctx, d), Writable: c.canWrite()}
	if view.Upstream, err = h.lineage(ctx, c, d.Reference, catalog.Upstream); err == nil {
		view.Downstream, err = h.lineage(ctx, c, d.Reference, catalog.Downstream)
	}
	if err != nil {
		writeStoreError(w, err, "lineage")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, view)
}

// lineage walks one direction from a dataset, keeping the edges whose two
// ends the caller reaches.
func (h *Handler) lineage(ctx context.Context, c *caller, ref string, dir catalog.Direction) ([]catalog.Hop, error) {
	hops, err := h.deps.Store.Lineage(ctx, ref, dir, lineageDepth)
	if err != nil {
		return nil, err //nolint:wrapcheck // writeStoreError reports it
	}
	out := make([]catalog.Hop, 0, len(hops))
	for _, hop := range hops {
		// A hop deeper than one names two datasets, neither of them this one,
		// so both ends must be ones the caller may see.
		if c.reaches(hop.Source) && c.reaches(hop.Target) {
			out = append(out, hop)
		}
	}
	return out, nil
}

// names resolves the display names of the glossary terms and domains a
// dataset's curation refers to. A reference no definition answers keeps no
// name, and the page shows the reference.
func (h *Handler) names(ctx context.Context, d *catalog.Dataset) map[string]string {
	refs := definitionRefs(d.Aspects)
	for _, col := range d.Columns {
		refs = append(refs, definitionRefs(col.Aspects)...)
	}
	slices.Sort(refs)
	out := make(map[string]string)
	for _, s := range slices.Compact(refs) {
		ref, err := catalog.ParseRef(s)
		if err != nil {
			continue
		}
		if def, err := h.deps.Store.GetDefinition(ctx, ref); err == nil {
			out[s] = def.Name
		}
	}
	return out
}

// definitionRefs are the definition references one entity's curation holds.
func definitionRefs(a catalog.Aspects) []string {
	refs := slices.Clone(a.GlossaryTerms)
	if a.Domain != "" {
		refs = append(refs, a.Domain)
	}
	return refs
}

// getSchemaDocument handles GET /api/v1/portal/catalog/schema-document.
//
// @Summary      Get a schema document
// @Description  Returns a connection's schema document (an OpenAPI spec or a GraphQL SDL) as the catalog scan last read it, when the caller's persona reaches the connection.
// @Tags         Data Catalog
// @Produce      json
// @Param        ref  query  string  true  "Schema document reference (mcp:schema_document:(kind,connection,filename))"
// @Success      200  {object}  SchemaDocumentView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/schema-document [get]
func (h *Handler) getSchemaDocument(w http.ResponseWriter, r *http.Request) {
	c, ok := h.reader(w, r)
	if !ok {
		return
	}
	ref, ok := parseRef(w, r)
	if !ok {
		return
	}
	if ref.Kind != catalog.KindSchemaDocument || !c.reaches(ref.String()) {
		httpjson.WriteError(w, http.StatusNotFound, "schema document not found")
		return
	}
	doc, err := h.deps.Store.GetDocument(r.Context(), ref)
	if err != nil {
		writeStoreError(w, err, "schema document")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, SchemaDocumentView{SchemaDocument: doc, Content: string(doc.Content)})
}

// SchemaDocumentView is a schema document with its content as text.
type SchemaDocumentView struct {
	*catalog.SchemaDocument
	Content string `json:"content"`
}
