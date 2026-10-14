package catalogapi

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// History is the changes to an entity's curation, newest first.
type History struct {
	Changes []catalog.Change `json:"changes"`
}

// history handles GET /api/v1/portal/catalog/history.
//
// @Summary      Get curation history
// @Description  Returns the changes to one aspect of a dataset, column or definition, newest first, or to every aspect it carries when none is named: who wrote each value, when, what it replaced, and which change a rollback undid.
// @Tags         Data Catalog
// @Produce      json
// @Param        ref     query  string  true   "Catalog reference"
// @Param        aspect  query  string  false  "Aspect name"
// @Success      200  {object}  History
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/history [get]
func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	c, ok := h.reader(w, r)
	if !ok {
		return
	}
	ref, ok := parseRef(w, r)
	if !ok {
		return
	}
	if !c.reaches(ref.String()) {
		httpjson.WriteError(w, http.StatusNotFound, "entity not found")
		return
	}
	aspects := catalog.AspectsFor(ref.Kind)
	if a := r.URL.Query().Get("aspect"); a != "" {
		aspects = []catalog.AspectName{catalog.AspectName(a)}
	}
	changes := make([]catalog.Change, 0)
	for _, a := range aspects {
		got, err := h.deps.Store.History(r.Context(), ref, a)
		if err != nil {
			writeStoreError(w, err, "entity")
			return
		}
		changes = append(changes, got...)
	}
	slices.SortStableFunc(changes, func(a, b catalog.Change) int { return b.WrittenAt.Compare(a.WrittenAt) })
	httpjson.WriteJSON(w, http.StatusOK, History{Changes: changes})
}

// AspectWrite is one aspect value to write.
type AspectWrite struct {
	Reference string             `json:"reference"`
	Aspect    catalog.AspectName `json:"aspect"`
	// Value is the aspect's new value in its own shape; null clears it.
	Value json.RawMessage `json:"value" swaggertype:"object"`
	// Expect, when present, is the value the writer read the aspect as (null
	// for absent). The write is refused with 409 when the aspect holds
	// anything else, so a value built from a stale read never replaces a
	// write made since.
	Expect json.RawMessage `json:"expect,omitempty" swaggertype:"object"`
}

// edit is the store write an aspect write stands for, checked against the
// value the writer read when it sent one.
func (in AspectWrite) edit(ref catalog.Ref) catalog.AspectEdit {
	e := catalog.AspectEdit{Ref: ref, Aspect: in.Aspect, Value: in.Value}
	if in.Expect != nil {
		e.Checked = true
		if string(in.Expect) != "null" {
			e.Expect = in.Expect
		}
	}
	return e
}

// writeAspect handles PUT /api/v1/portal/catalog/aspect.
//
// @Summary      Write a curation aspect
// @Description  Writes one aspect of a dataset, column or definition: description, tags, glossary_terms, owners, domain, custom_properties, structured_properties, documentation_links, curated_queries, incidents or context_documents, each in its own shape, and records the change in its history. A null value clears the aspect. When expect carries the value the writer read, a write made since is refused with 409 rather than replaced. Takes the apply_knowledge tool on the caller's persona and, for a dataset or column, the persona's reach of its connection.
// @Tags         Data Catalog
// @Accept       json
// @Produce      json
// @Param        body  body      AspectWrite  true  "The aspect and its value"
// @Success      200   {object}  catalog.Change
// @Failure      400   {object}  httpjson.ProblemDetail
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/aspect [put]
func (h *Handler) writeAspect(w http.ResponseWriter, r *http.Request) {
	c, ok := h.writer(w, r)
	if !ok {
		return
	}
	var in AspectWrite
	if !decodeBody(w, r, &in) {
		return
	}
	ref, err := catalog.ParseRef(in.Reference)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "reference: "+err.Error())
		return
	}
	if !c.reaches(ref.String()) {
		httpjson.WriteError(w, http.StatusNotFound, "entity not found")
		return
	}
	changes, err := h.deps.Store.WriteAspects(r.Context(), []catalog.AspectEdit{in.edit(ref)}, c.user.Email)
	h.audit(r.Context(), c, "write_aspect", map[string]any{"reference": ref.String(), "aspect": string(in.Aspect)}, err)
	if err != nil {
		writeStoreError(w, err, "entity")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, changes[0])
}

// RollbackRequest names the change to undo and the entity and aspect it
// belongs to.
type RollbackRequest struct {
	Reference string             `json:"reference"`
	Aspect    catalog.AspectName `json:"aspect"`
	ChangeID  string             `json:"change_id"`
}

// rollback handles POST /api/v1/portal/catalog/rollback.
//
// @Summary      Roll back a curation change
// @Description  Restores the value a change replaced and records that as a new change naming the one it undid. The change must belong to the reference and aspect named. A change the aspect was written again after is refused with 409 naming the newer write, since undoing it would discard that write too. Takes the apply_knowledge tool on the caller's persona.
// @Tags         Data Catalog
// @Accept       json
// @Produce      json
// @Param        body  body      RollbackRequest  true  "The change to roll back"
// @Success      200   {object}  catalog.Change
// @Failure      400   {object}  httpjson.ProblemDetail
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/rollback [post]
func (h *Handler) rollback(w http.ResponseWriter, r *http.Request) {
	c, ok := h.writer(w, r)
	if !ok {
		return
	}
	var in RollbackRequest
	if !decodeBody(w, r, &in) {
		return
	}
	ref, err := catalog.ParseRef(in.Reference)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "reference: "+err.Error())
		return
	}
	if !c.reaches(ref.String()) {
		httpjson.WriteError(w, http.StatusNotFound, "entity not found")
		return
	}
	ctx := r.Context()
	changes, err := h.deps.Store.History(ctx, ref, in.Aspect)
	if err != nil {
		writeStoreError(w, err, "entity")
		return
	}
	if !slices.ContainsFunc(changes, func(ch catalog.Change) bool { return ch.ID == in.ChangeID }) {
		httpjson.WriteError(w, http.StatusNotFound, "change not found")
		return
	}
	change, err := h.deps.Store.Rollback(ctx, in.ChangeID, c.user.Email)
	h.audit(ctx, c, "rollback", map[string]any{"reference": ref.String(), "aspect": string(in.Aspect), "change_id": in.ChangeID}, err)
	if err != nil {
		writeStoreError(w, err, "change")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, change)
}
