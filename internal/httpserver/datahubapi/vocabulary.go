package datahubapi

// Governance vocabularies: the flat, named sets the catalog is described with —
// tags (#1156) and domains (#1157). Both are defined and retired from the portal
// through the same two routes, and the flow is identical: authorize the write
// grant, validate, forward one upstream call, audit it, answer.
//
// Only the writes live here, because the reads each surface needs already exist.
// Listing a vocabulary is its picker's lookup route (catalog/lookup/tags,
// catalog/lookup/domains); the tables an entry covers come from the catalog
// search's matching filter (?tags=<urn>, ?domain=<urn>); an entry's description
// is edited with PUT catalog/entity/description, which takes any entity URN; and
// a domain's membership is edited with PUT catalog/entity/domain, aimed at the
// table. A second route for any of those would be an alias, not a capability.

import (
	"context"
	"net/http"
	"strings"
)

// vocabulary describes one governance vocabulary's create and delete. The two
// kinds differ only in what they are called, which URN they accept, and which
// writer method runs, so they are one implementation parameterized by this
// rather than a copy per kind.
type vocabulary struct {
	// kind names the entity in audit records and error messages ("tag", "domain").
	kind string
	// urnTypes are the URN entity types a delete accepts.
	urnTypes []string
	// create defines a new entry and returns the URN DataHub assigned it.
	create func(ctx context.Context, w Writer, name, description string) (string, error)
	// remove retires an entry by URN.
	remove func(ctx context.Context, w Writer, urn string) error
}

var (
	// tagVocabulary is the tag vocabulary (#1156). CreateTag is on the Writer
	// interface but not on the exported DataHubWriter, for the reason recorded
	// there: the knowledge apply path assigns tags but never authors them.
	tagVocabulary = vocabulary{
		kind:     "tag",
		urnTypes: tagURNTypes,
		create: func(ctx context.Context, w Writer, name, description string) (string, error) {
			return w.CreateTag(ctx, name, description)
		},
		remove: func(ctx context.Context, w Writer, urn string) error {
			return w.DeleteTag(ctx, urn)
		},
	}

	// domainVocabulary is the domain vocabulary (#1157).
	domainVocabulary = vocabulary{
		kind:     fieldDomain,
		urnTypes: domainURNTypes,
		create: func(ctx context.Context, w Writer, name, description string) (string, error) {
			return w.CreateDomain(ctx, name, description)
		},
		remove: func(ctx context.Context, w Writer, urn string) error {
			return w.DeleteDomain(ctx, urn)
		},
	}
)

// vocabularyRequest is the create payload for any governance vocabulary.
type vocabularyRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// createVocabularyEntry defines a new entry in v and returns the URN DataHub
// assigned it. Gated on the datahub_create grant plus a write-enabled
// connection, like every other create on this surface.
//
// The new entry is not immediately listable: the list read is served from
// DataHub's asynchronously populated index, so a caller that re-lists straight
// away may not see what it just created. The returned URN is authoritative in
// the meantime.
func (h *Handler) createVocabularyEntry(w http.ResponseWriter, r *http.Request, v vocabulary) {
	auth, ok := h.authorizeWrite(w, r, datahubCreateTool)
	if !ok {
		return
	}
	var req vocabularyRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, errNameRequired)
		return
	}
	urn, err := v.create(r.Context(), auth.writer, req.Name, req.Description)
	h.audit(r, auth, datahubCreateTool, map[string]any{
		"entity_type": v.kind,
		"name":        req.Name,
	}, err)
	if err != nil {
		writeError(w, http.StatusBadGateway, v.kind+" create failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"urn": urn})
}

// deleteVocabularyEntry retires an entry of v. Gated on the datahub_delete grant.
//
// The URN is a query parameter rather than a path segment because a tag or
// domain URN is itself colon-delimited, matching how the glossary reads take
// theirs. A URN of the wrong kind is a 400 here rather than a forwarded call
// that would surface as a misleading 502.
func (h *Handler) deleteVocabularyEntry(w http.ResponseWriter, r *http.Request, v vocabulary) {
	auth, ok := h.authorizeWrite(w, r, datahubDeleteTool)
	if !ok {
		return
	}
	urn, ok := requireURNParam(w, r, v.urnTypes)
	if !ok {
		return
	}
	err := v.remove(r.Context(), auth.writer, urn)
	h.audit(r, auth, datahubDeleteTool, map[string]any{
		"entity_type": v.kind,
		"urn":         urn,
	}, err)
	if err != nil {
		writeError(w, http.StatusBadGateway, v.kind+" delete failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// vocabularyRoutes registers the create and delete for one vocabulary under
// base/{conn}/catalog/<path>.
func (h *Handler) vocabularyRoutes(mux *http.ServeMux, base, path string, v vocabulary) {
	mux.HandleFunc("POST "+base+"/{conn}/catalog/"+path, func(w http.ResponseWriter, r *http.Request) {
		h.createVocabularyEntry(w, r, v)
	})
	mux.HandleFunc("DELETE "+base+"/{conn}/catalog/"+path, func(w http.ResponseWriter, r *http.Request) {
		h.deleteVocabularyEntry(w, r, v)
	})
}
