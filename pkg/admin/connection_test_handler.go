package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
)

// probeTimeout bounds one connection test. It is the sole bound on the call —
// a toolkit's probe asks its upstream one question and most upstream clients
// enforce their own timeouts only on the paths tool calls take — so a hung
// upstream must not be able to hold an admin request open.
const probeTimeout = 30 * time.Second

// testConnectionResponse is what a connection test reports.
//
// It carries the kind and name it tested so a response read on its own (a log,
// a portal toast, an agent's transcript) says which connection it is about.
type testConnectionResponse struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// testConnectionInstance handles POST /api/v1/admin/connection-instances/{kind}/{name}/test.
//
// @Summary      Test a connection instance
// @Description  Opens the connection and asks its upstream one harmless question — a trivial query, a bucket listing, an introspection, a tools/list, a GET at the base URL — and reports whether it answered. It is read-only and persists nothing. Until this existed a connection could be created, stored, listed and read back without anything ever having opened it, so an unusable connection was indistinguishable from a working one until a real call failed. 200 means the connection answered; 503 means it did not, with the upstream's own error.
// @Tags         Connections
// @Produce      json
// @Param        kind  path  string  true  "Toolkit kind (trino, s3, api, graphql, mcp)"
// @Param        name  path  string  true  "Instance name"
// @Success      200   {object}  testConnectionResponse
// @Failure      404   {object}  problemDetail
// @Failure      409   {object}  problemDetail
// @Failure      503   {object}  testConnectionResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/{kind}/{name}/test [post]
func (h *Handler) testConnectionInstance(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	name := r.PathValue(pathKeyName)

	if !knownConnectionKinds[kind] {
		writeError(w, http.StatusBadRequest, "unknown connection kind: "+kind)
		return
	}
	prober, found := h.findProber(kind)
	if !found {
		// The connection may well exist as a stored row; what is missing is a
		// process serving it, which is the one state a probe cannot speak for.
		writeError(w, http.StatusConflict,
			"no "+kind+" toolkit is registered in this process, so there is nothing to open the connection with; "+
				"a connection saved on another replica is testable there")
		return
	}
	if prober == nil {
		writeError(w, http.StatusConflict, "the "+kind+" toolkit cannot test a connection")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()

	// A connection saved through another replica is a row here before the
	// reload bus delivers it; testing it straight after the save must not
	// answer that it does not exist (#1888).
	if h.deps.ConnectionCatchUp != nil {
		h.deps.ConnectionCatchUp.TakeOn(ctx, kind, name)
	}
	result := prober.ProbeConnection(ctx, name)
	body := testConnectionResponse{
		Kind: kind, Name: name, OK: result.OK, Detail: result.Detail, Error: result.Error,
	}
	if !result.OK {
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// findProber resolves the registered toolkit of a kind and reports whether it
// can probe. The two returns are distinct answers: found=false means no toolkit
// of that kind runs here, while a nil prober means one does and does not
// implement the check.
func (h *Handler) findProber(kind string) (connprobe.Prober, bool) {
	if h.deps.ToolkitRegistry == nil {
		return nil, false
	}
	for _, tk := range h.deps.ToolkitRegistry.All() {
		if tk.Kind() != kind {
			continue
		}
		prober, ok := tk.(connprobe.Prober)
		if !ok {
			return nil, true
		}
		return prober, true
	}
	return nil, false
}
