package datahubapi

// Tag governance (#1156): define a tag and retire one from the portal.
//
// Only the writes live here, because the reads the Tags surface needs already
// exist. Listing and name-filtering tags is the same operation the catalog tag
// picker performs, served by GET .../catalog/lookup/tags; the datasets carrying
// a tag come from GET .../catalog/search?tags=<urn>, which the adapter maps to
// DataHub's "tags" filter field; and editing a tag's description is PUT
// .../catalog/entity/description, which takes any entity URN. Adding a second
// route for any of those would be an alias, not a capability.

import (
	"net/http"
	"strings"
)

// tagRequest is the tag create payload.
type tagRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// createTag defines a new tag in DataHub, returning the URN DataHub assigned it.
// Gated on the datahub_create grant plus a write-enabled connection, like every
// other create on this surface.
//
// The new tag is not immediately listable: GET .../catalog/lookup/tags reads
// DataHub's search index, which is populated asynchronously, so a caller that
// re-lists straight away may not see what it just created. The returned URN is
// authoritative in the meantime.
func (h *Handler) createTag(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorizeWrite(w, r, datahubCreateTool)
	if !ok {
		return
	}
	var req tagRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, errNameRequired)
		return
	}
	urn, err := auth.writer.CreateTag(r.Context(), req.Name, req.Description)
	h.audit(r, auth, datahubCreateTool, map[string]any{
		"entity_type": "tag",
		"name":        req.Name,
	}, err)
	if err != nil {
		writeError(w, http.StatusBadGateway, "tag create failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"urn": urn})
}

// deleteTag retires a tag definition. Gated on the datahub_delete grant.
//
// The URN is a query parameter rather than a path segment because a tag URN is
// itself colon-delimited, matching how the glossary reads take theirs. A URN
// that is not a tag is a 400 here rather than a forwarded call that would
// surface as a misleading 502.
func (h *Handler) deleteTag(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorizeWrite(w, r, datahubDeleteTool)
	if !ok {
		return
	}
	urn, ok := requireURNParam(w, r, tagURNTypes)
	if !ok {
		return
	}
	err := auth.writer.DeleteTag(r.Context(), urn)
	h.audit(r, auth, datahubDeleteTool, map[string]any{
		"entity_type": "tag",
		"urn":         urn,
	}, err)
	if err != nil {
		writeError(w, http.StatusBadGateway, "tag delete failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
