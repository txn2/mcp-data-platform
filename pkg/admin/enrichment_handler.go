package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
)

const (
	pathKeyName     = "name"
	pathKeyID       = "id"
	msgRuleNotFound = "enrichment rule not found"
)

// EnrichmentStore abstracts enrichment.Store for the admin handler, so tests
// can swap in a stub without pulling in a real database.
type EnrichmentStore interface {
	List(ctx context.Context, connection, tool string, enabledOnly bool) ([]enrichment.Rule, error)
	Get(ctx context.Context, id string) (*enrichment.Rule, error)
	Create(ctx context.Context, r enrichment.Rule) (enrichment.Rule, error)
	Update(ctx context.Context, r enrichment.Rule) (enrichment.Rule, error)
	Delete(ctx context.Context, id string) error
}

// registerEnrichmentRoutes registers CRUD endpoints for gateway enrichment
// rules. Rules live per-connection. Routes are only added when an
// EnrichmentStore is configured (DB available) AND the handler is mutable.
func (h *Handler) registerEnrichmentRoutes() {
	if h.deps.EnrichmentStore == nil || !h.isMutable() {
		return
	}
	const base = "/api/v1/admin/gateway/connections/{name}/enrichment-rules"
	h.mux.HandleFunc("GET "+base, h.listEnrichmentRules)
	h.mux.HandleFunc("POST "+base, h.createEnrichmentRule)
	h.mux.HandleFunc("GET "+base+"/{id}", h.getEnrichmentRule)
	h.mux.HandleFunc("PUT "+base+"/{id}", h.updateEnrichmentRule)
	h.mux.HandleFunc("DELETE "+base+"/{id}", h.deleteEnrichmentRule)
	if h.deps.EnrichmentEngine != nil {
		h.mux.HandleFunc("POST "+base+"/{id}/dry-run", h.dryRunEnrichmentRule)
	}
}

// dryRunEnrichmentRequest is the body of a dry-run invocation. The fields
// mirror what an upstream call carries so the engine can resolve bindings.
type dryRunEnrichmentRequest struct {
	Args     map[string]any `json:"args"`
	Response any            `json:"response"`
	User     dryRunUser     `json:"user"`
}

type dryRunUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// dryRunEnrichmentResponse mirrors enrichment.Result enough for the
// admin UI to preview the merged response and any per-rule outcomes.
type dryRunEnrichmentResponse struct {
	Response any                    `json:"response"`
	Warnings []string               `json:"warnings,omitempty"`
	Fired    []enrichment.FiredRule `json:"fired,omitempty"`
}

// dryRunEnrichmentRule lets an operator preview a single rule against a
// sample call without persisting anything or affecting other rules.
//
// @Summary      Dry-run a single enrichment rule
// @Description  Loads the rule, applies it against the provided sample args/response/user without persisting and without invoking the live gateway, and returns the merged response + per-rule trace. Use this in the rule editor to validate bindings and merge strategy.
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        name  path  string                  true  "Gateway connection name"
// @Param        id    path  string                  true  "Rule id"
// @Param        body  body  dryRunEnrichmentRequest true  "Sample call"
// @Success      200   {object}  dryRunEnrichmentResponse
// @Failure      404   {object}  problemDetail
// @Failure      400   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/enrichment-rules/{id}/dry-run [post]
func (h *Handler) dryRunEnrichmentRule(w http.ResponseWriter, r *http.Request) {
	connection := r.PathValue(pathKeyName)
	id := r.PathValue(pathKeyID)

	rule, err := h.deps.EnrichmentStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, enrichment.ErrRuleNotFound) {
			writeError(w, http.StatusNotFound, msgRuleNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load enrichment rule")
		return
	}
	if rule.ConnectionName != connection {
		writeError(w, http.StatusNotFound, msgRuleNotFound)
		return
	}

	var body dryRunEnrichmentRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Run the engine in single-rule mode by routing through a one-shot
	// store wrapper that returns only this rule.
	oneShot := singleRuleStore{rule: *rule}
	preview := enrichment.NewEngine(oneShot, h.deps.EnrichmentEngine.Sources())

	call := enrichment.CallContext{
		Connection: connection,
		ToolName:   rule.ToolName,
		Args:       body.Args,
		User: enrichment.UserSnapshot{
			ID:    body.User.ID,
			Email: body.User.Email,
		},
	}
	res := preview.Apply(r.Context(), call, body.Response)
	writeJSON(w, http.StatusOK, dryRunEnrichmentResponse{
		Response: res.Response,
		Warnings: res.Warnings,
		Fired:    res.Fired,
	})
}

// singleRuleStore wraps a single Rule into a Store so the dry-run engine
// only ever evaluates that one rule, regardless of what's persisted.
type singleRuleStore struct {
	rule enrichment.Rule
}

// List returns just the wrapped rule, ignoring filters. It satisfies
// enrichment.Store for the dry-run engine.
func (s singleRuleStore) List(_ context.Context, _, _ string, _ bool) ([]enrichment.Rule, error) {
	return []enrichment.Rule{s.rule}, nil
}

// Get always reports not-found; the dry-run engine never calls Get.
func (singleRuleStore) Get(_ context.Context, _ string) (*enrichment.Rule, error) {
	return nil, enrichment.ErrRuleNotFound
}

// Create is a no-op required by enrichment.Store.
func (singleRuleStore) Create(_ context.Context, r enrichment.Rule) (enrichment.Rule, error) {
	return r, nil
}

// Update is a no-op required by enrichment.Store.
func (singleRuleStore) Update(_ context.Context, r enrichment.Rule) (enrichment.Rule, error) {
	return r, nil
}

// Delete is a no-op required by enrichment.Store.
func (singleRuleStore) Delete(_ context.Context, _ string) error { return nil }

// listEnrichmentRules returns every enrichment rule for a connection.
//
// @Summary      List enrichment rules for a connection
// @Tags         Connections
// @Produce      json
// @Param        name  path  string  true  "Gateway connection name"
// @Param        tool_name    query  string  false  "Filter by proxied tool name"
// @Param        enabled_only query  bool    false  "Only return enabled rules"
// @Success      200  {array}  enrichment.Rule
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/enrichment-rules [get]
func (h *Handler) listEnrichmentRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.deps.EnrichmentStore.List(r.Context(),
		r.PathValue(pathKeyName),
		r.URL.Query().Get("tool_name"),
		r.URL.Query().Get("enabled_only") == "true",
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list enrichment rules")
		return
	}
	if rules == nil {
		rules = []enrichment.Rule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

// getEnrichmentRule returns a single rule by id.
//
// @Summary      Get an enrichment rule
// @Tags         Connections
// @Produce      json
// @Param        name  path  string  true  "Gateway connection name"
// @Param        id    path  string  true  "Rule id"
// @Success      200  {object}  enrichment.Rule
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/enrichment-rules/{id} [get]
func (h *Handler) getEnrichmentRule(w http.ResponseWriter, r *http.Request) {
	rule, err := h.deps.EnrichmentStore.Get(r.Context(), r.PathValue(pathKeyID))
	if err != nil {
		if errors.Is(err, enrichment.ErrRuleNotFound) {
			writeError(w, http.StatusNotFound, msgRuleNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get enrichment rule")
		return
	}
	if rule.ConnectionName != r.PathValue(pathKeyName) {
		writeError(w, http.StatusNotFound, msgRuleNotFound)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// enrichmentRuleBody is the create/update payload.
type enrichmentRuleBody struct {
	ToolName      string               `json:"tool_name"`
	WhenPredicate enrichment.Predicate `json:"when_predicate"`
	EnrichAction  enrichment.Action    `json:"enrich_action"`
	MergeStrategy enrichment.Merge     `json:"merge_strategy"`
	Description   string               `json:"description"`
	Enabled       bool                 `json:"enabled"`
}

// createEnrichmentRule inserts a new rule under the named connection.
//
// @Summary      Create an enrichment rule
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        name  path  string              true  "Gateway connection name"
// @Param        body  body  enrichmentRuleBody  true  "Rule"
// @Success      201  {object}  enrichment.Rule
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/enrichment-rules [post]
func (h *Handler) createEnrichmentRule(w http.ResponseWriter, r *http.Request) {
	connection := r.PathValue(pathKeyName)

	var body enrichmentRuleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule := enrichment.Rule{
		ConnectionName: connection,
		ToolName:       body.ToolName,
		WhenPredicate:  body.WhenPredicate,
		EnrichAction:   body.EnrichAction,
		MergeStrategy:  body.MergeStrategy,
		Description:    body.Description,
		Enabled:        body.Enabled,
		CreatedBy:      authorEmailOrID(r.Context()),
		CreatedAt:      time.Now().UTC(),
	}
	if err := rule.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := h.deps.EnrichmentStore.Create(r.Context(), rule)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create enrichment rule")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// updateEnrichmentRule replaces the mutable fields of an existing rule.
//
// @Summary      Update an enrichment rule
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        name  path  string              true  "Gateway connection name"
// @Param        id    path  string              true  "Rule id"
// @Param        body  body  enrichmentRuleBody  true  "Rule"
// @Success      200  {object}  enrichment.Rule
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/enrichment-rules/{id} [put]
func (h *Handler) updateEnrichmentRule(w http.ResponseWriter, r *http.Request) {
	connection := r.PathValue(pathKeyName)
	id := r.PathValue(pathKeyID)

	existing, err := h.deps.EnrichmentStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, enrichment.ErrRuleNotFound) {
			writeError(w, http.StatusNotFound, msgRuleNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load enrichment rule")
		return
	}
	if existing.ConnectionName != connection {
		writeError(w, http.StatusNotFound, msgRuleNotFound)
		return
	}

	var body enrichmentRuleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	existing.ToolName = body.ToolName
	existing.WhenPredicate = body.WhenPredicate
	existing.EnrichAction = body.EnrichAction
	existing.MergeStrategy = body.MergeStrategy
	existing.Description = body.Description
	existing.Enabled = body.Enabled

	if err := existing.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := h.deps.EnrichmentStore.Update(r.Context(), *existing)
	if err != nil {
		if errors.Is(err, enrichment.ErrRuleNotFound) {
			writeError(w, http.StatusNotFound, msgRuleNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update enrichment rule")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// deleteEnrichmentRule removes a rule by id.
//
// @Summary      Delete an enrichment rule
// @Tags         Connections
// @Param        name  path  string  true  "Gateway connection name"
// @Param        id    path  string  true  "Rule id"
// @Success      204
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/enrichment-rules/{id} [delete]
func (h *Handler) deleteEnrichmentRule(w http.ResponseWriter, r *http.Request) {
	connection := r.PathValue(pathKeyName)
	id := r.PathValue(pathKeyID)

	// Scope check: reject if the rule isn't part of the named connection.
	existing, err := h.deps.EnrichmentStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, enrichment.ErrRuleNotFound) {
			writeError(w, http.StatusNotFound, msgRuleNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load enrichment rule")
		return
	}
	if existing.ConnectionName != connection {
		writeError(w, http.StatusNotFound, msgRuleNotFound)
		return
	}

	if err := h.deps.EnrichmentStore.Delete(r.Context(), id); err != nil {
		if errors.Is(err, enrichment.ErrRuleNotFound) {
			writeError(w, http.StatusNotFound, msgRuleNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete enrichment rule")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authorEmailOrID returns the current user's email or user id, whichever is
// populated, for audit attribution on rule writes.
func authorEmailOrID(ctx context.Context) string {
	u := GetUser(ctx)
	if u == nil {
		return ""
	}
	if u.Email != "" {
		return u.Email
	}
	return u.UserID
}
