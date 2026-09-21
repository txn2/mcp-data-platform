package admin

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// connectionKindDoc describes one connection kind an instance can be created
// under: what it is, and what its config takes.
type connectionKindDoc struct {
	Kind string `json:"kind"`
	// ConfigSchema is the JSON Schema of the object sent as "config" on
	// PUT /admin/connection-instances/{kind}/{name}.
	ConfigSchema json.RawMessage `json:"config_schema,omitempty" swaggertype:"object"`
	// Note says how the schema is to be read, so nothing is inferred from its
	// silence.
	Note string `json:"note,omitempty"`
}

// configSchemaNote is attached to every kind's schema.
//
// It says what the schema does NOT do, because the reason these endpoints exist
// is a config that was accepted and reported as created while being unusable:
// an agent reading a schema here must not conclude that whatever the schema
// admits will work, nor that a key it does not name will be rejected.
const configSchemaNote = "These are the keys this kind reads. A key not named here is stored and ignored, so check " +
	"spelling against this list. Values are stored literally: ${VAR} is NOT expanded on a database-managed " +
	"connection (only the platform's configuration file expands it) and is refused on write. A schema this " +
	"config satisfies does not mean the connection works — POST .../test opens it and reports what the upstream " +
	"said."

// registerConnectionKindRoutes registers the connection-kind discovery
// endpoints. They read no store and no connection, so they are registered
// wherever the admin API is.
func (h *Handler) registerConnectionKindRoutes() {
	h.mux.HandleFunc("GET /api/v1/admin/connection-kinds", h.listConnectionKinds)
	h.mux.HandleFunc("GET /api/v1/admin/connection-kinds/{kind}", h.getConnectionKind)
}

// listConnectionKinds handles GET /api/v1/admin/connection-kinds.
//
// @Summary      List connection kinds and their config schemas
// @Description  Returns every kind a connection instance can be created under, each with the JSON Schema of its config object. A connection's config is a freeform object on the wire, so without this the only way to learn a key's name is to read the deployment's configuration file out of band.
// @Tags         Connections
// @Produce      json
// @Success      200  {array}  connectionKindDoc
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-kinds [get]
func (*Handler) listConnectionKinds(w http.ResponseWriter, _ *http.Request) {
	kinds := make([]string, 0, len(knownConnectionKinds))
	for kind := range knownConnectionKinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)

	docs := make([]connectionKindDoc, 0, len(kinds))
	for _, kind := range kinds {
		docs = append(docs, connectionKindDocFor(kind))
	}
	writeJSON(w, http.StatusOK, docs)
}

// getConnectionKind handles GET /api/v1/admin/connection-kinds/{kind}.
//
// @Summary      Get one connection kind's config schema
// @Description  Returns the JSON Schema of one kind's config object, which is what the "config" field of PUT /admin/connection-instances/{kind}/{name} takes.
// @Tags         Connections
// @Produce      json
// @Param        kind  path  string  true  "Toolkit kind (trino, s3, api, graphql, mcp)"
// @Success      200   {object}  connectionKindDoc
// @Failure      404   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-kinds/{kind} [get]
func (*Handler) getConnectionKind(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if !knownConnectionKinds[kind] {
		writeError(w, http.StatusNotFound, "unknown connection kind: "+kind)
		return
	}
	writeJSON(w, http.StatusOK, connectionKindDocFor(kind))
}

// connectionKindDocFor builds one kind's documentation.
func connectionKindDocFor(kind string) connectionKindDoc {
	doc := connectionKindDoc{Kind: kind, ConfigSchema: registry.ConnectionConfigSchema(kind)}
	if doc.ConfigSchema != nil {
		doc.Note = configSchemaNote
	}
	return doc
}
