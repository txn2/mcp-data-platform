package catalogapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// usesLimit bounds the datasets and columns listed under one definition.
const usesLimit = 100

// childLimit bounds the glossary entries listed under one node.
const childLimit = 500

// DefinitionList is one page of definitions.
type DefinitionList struct {
	Results []catalog.DefinitionSummary `json:"results"`
}

// listDefinitions handles GET /api/v1/portal/catalog/definitions.
//
// @Summary      List catalog definitions
// @Description  Returns a page of glossary terms, glossary nodes, tags, domains and data products, ordered by name: of the kinds named (every kind when none is), under one glossary node, or at the root of the glossary, matching q in the name, the slug or the description. Each glossary node says how many entries sit directly under it.
// @Tags         Data Catalog
// @Produce      json
// @Param        kind    query  string  false  "Definition kind; repeatable"
// @Param        parent  query  string  false  "Glossary node reference whose entries are listed"
// @Param        roots   query  bool    false  "Only the entries under no glossary node"
// @Param        q       query  string  false  "Text to match"
// @Param        limit   query  int     false  "Page size (default 50, at most 200)"
// @Param        offset  query  int     false  "Page offset"
// @Success      200  {object}  DefinitionList
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/definitions [get]
func (h *Handler) listDefinitions(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.reader(w, r); !ok {
		return
	}
	q := r.URL.Query()
	query := catalog.DefinitionQuery{
		Parent: q.Get("parent"),
		Roots:  q.Get("roots") == "true",
		Text:   q.Get("q"),
		Limit:  pageLimit(q.Get("limit")),
		Offset: max(atoi(q.Get("offset")), 0),
	}
	for _, k := range q["kind"] {
		query.Kinds = append(query.Kinds, catalog.Kind(k))
	}
	results, err := h.deps.Store.ListDefinitions(r.Context(), query)
	if err != nil {
		writeStoreError(w, err, "glossary node")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, DefinitionList{Results: results})
}

// DefinitionView is a definition as its page shows it: the definition, the
// glossary entries under a node, the datasets and columns that carry a term,
// tag or domain the caller's persona reaches, and whether the caller may edit
// it.
type DefinitionView struct {
	*catalog.Definition
	Children []catalog.DefinitionSummary `json:"children"`
	Uses     []catalog.Use               `json:"uses"`
	// UsesWithheld counts the carriers on connections the persona does not
	// reach.
	UsesWithheld int `json:"uses_withheld"`
	// MoreUses is set when more datasets and columns carry the definition than
	// the page lists.
	MoreUses bool `json:"more_uses"`
	Writable bool `json:"writable"`
}

// getDefinition handles GET /api/v1/portal/catalog/definition.
//
// @Summary      Get a catalog definition
// @Description  Returns a glossary term, glossary node, tag, domain or data product by reference (its key may be the id or the slug), the entries under a glossary node, and up to 100 datasets and columns carrying a term, tag or domain, without those on connections the caller's persona does not reach, which are counted instead; more_uses says more carry it than are listed.
// @Tags         Data Catalog
// @Produce      json
// @Param        ref  query  string  true  "Definition reference, e.g. mcp:glossary_term:revenue"
// @Success      200  {object}  DefinitionView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/definition [get]
func (h *Handler) getDefinition(w http.ResponseWriter, r *http.Request) {
	c, ok := h.reader(w, r)
	if !ok {
		return
	}
	ref, ok := parseRef(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	def, err := h.deps.Store.GetDefinition(ctx, ref)
	if err != nil {
		writeStoreError(w, err, "definition")
		return
	}
	view := DefinitionView{Definition: def, Children: []catalog.DefinitionSummary{}, Uses: []catalog.Use{}, Writable: c.canWrite()}
	if def.Kind == catalog.KindGlossaryNode {
		if view.Children, err = h.deps.Store.ListDefinitions(ctx, catalog.DefinitionQuery{Parent: def.Reference, Limit: childLimit}); err != nil {
			writeStoreError(w, err, "glossary node")
			return
		}
	}
	uses, err := h.deps.Store.Uses(ctx, ref, usesLimit+1)
	if err != nil {
		writeStoreError(w, err, "definition")
		return
	}
	if len(uses) > usesLimit {
		uses, view.MoreUses = uses[:usesLimit], true
	}
	for _, u := range uses {
		if c.reaches(u.Dataset) {
			view.Uses = append(view.Uses, u)
		} else {
			view.UsesWithheld++
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, view)
}

// DefinitionCreate is a definition to create.
type DefinitionCreate struct {
	Kind catalog.Kind `json:"kind"`
	Name string       `json:"name"`
	// Parent is the glossary node a term or node is placed under.
	Parent      string `json:"parent,omitempty"`
	Description string `json:"description,omitempty"`
}

// createDefinition handles POST /api/v1/portal/catalog/definitions.
//
// @Summary      Create a catalog definition
// @Description  Creates a glossary term, glossary node, tag, domain or data product, with its description when one is given. A term or node may be placed under a glossary node. A definition of the same kind whose name gives the same slug already exists is refused with 409 naming it. Takes the apply_knowledge tool on the caller's persona.
// @Tags         Data Catalog
// @Accept       json
// @Produce      json
// @Param        body  body      DefinitionCreate  true  "The definition"
// @Success      201   {object}  catalog.Definition
// @Failure      400   {object}  httpjson.ProblemDetail
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/definitions [post]
func (h *Handler) createDefinition(w http.ResponseWriter, r *http.Request) {
	c, ok := h.writer(w, r)
	if !ok {
		return
	}
	var in DefinitionCreate
	if !decodeBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	ctx := r.Context()
	if existing := h.existingDefinition(ctx, in.Kind, strings.TrimSpace(in.Name)); existing != nil {
		httpjson.WriteError(w, http.StatusConflict, fmt.Sprintf(
			"a %s named %q already exists (%s); open it to edit it", strings.ReplaceAll(string(in.Kind), "_", " "), existing.Name, existing.Reference))
		return
	}
	def, err := h.deps.Store.PutDefinition(ctx, catalog.DefinitionInput{Kind: in.Kind, Name: strings.TrimSpace(in.Name), Parent: in.Parent})
	if err == nil && strings.TrimSpace(in.Description) != "" {
		value, _ := wirejson.Marshal(strings.TrimSpace(in.Description)) //nolint:errchkjson // a string always encodes
		ref := catalog.KeyRef(def.Kind, def.ID)
		if _, err = h.deps.Store.WriteAspect(ctx, ref, catalog.AspectDescription, value, c.user.Email); err == nil {
			def, err = h.deps.Store.GetDefinition(ctx, ref)
		}
	}
	h.audit(ctx, c, "create_definition", map[string]any{"kind": string(in.Kind), "name": in.Name}, err)
	if err != nil {
		writeStoreError(w, err, "glossary node")
		return
	}
	httpjson.WriteJSON(w, http.StatusCreated, def)
}

// existingDefinition is the definition a create of this kind and name would
// land on: the store keys a definition by kind and slug, and storing a second
// one of the same slug updates the first, moving and renaming it. A create
// never does that; the caller is told to edit the one that exists.
func (h *Handler) existingDefinition(ctx context.Context, kind catalog.Kind, name string) *catalog.Definition {
	key := name
	if kind != catalog.KindTag {
		key = catalog.Slug(name)
	}
	if key == "" {
		return nil
	}
	def, err := h.deps.Store.GetDefinition(ctx, catalog.KeyRef(kind, key))
	if err != nil {
		return nil
	}
	return def
}

// deleteDefinition handles DELETE /api/v1/portal/catalog/definition.
//
// @Summary      Delete a catalog definition
// @Description  Deletes a glossary term, glossary node, tag, domain or data product. A glossary node with entries under it, and a term, tag or domain a dataset or column carries, is refused with 409 naming what still uses it. Takes the apply_knowledge tool on the caller's persona.
// @Tags         Data Catalog
// @Param        ref  query  string  true  "Definition reference"
// @Success      204
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/definition [delete]
func (h *Handler) deleteDefinition(w http.ResponseWriter, r *http.Request) {
	c, ok := h.writer(w, r)
	if !ok {
		return
	}
	ref, ok := parseRef(w, r)
	if !ok {
		return
	}
	err := h.deps.Store.DeleteDefinition(r.Context(), ref)
	h.audit(r.Context(), c, "delete_definition", map[string]any{"reference": ref.String()}, err)
	if err != nil {
		writeStoreError(w, err, "definition")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
