package apphttp

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/apptool"
)

// Environments and variables (#2696). The portal is the person: its promote
// and rollback carry out the move in an environment that requires approval,
// which manage_application refuses an agent.

const (
	pathEnv     = "env"
	pathVar     = "name"
	keyEnv      = "environment"
	keyVariable = "variable"
)

func (h *handler) mountEnvironments(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("POST "+Prefix+"/{app}/environments", wrap(http.HandlerFunc(h.addEnvironment)))
	mux.Handle("PATCH "+Prefix+"/{app}/environments/{env}", wrap(http.HandlerFunc(h.setEnvironment)))
	mux.Handle("DELETE "+Prefix+"/{app}/environments/{env}", wrap(http.HandlerFunc(h.removeEnvironment)))
	mux.Handle("POST "+Prefix+"/{app}/environments/{env}/promote", wrap(http.HandlerFunc(h.promote)))
	mux.Handle("POST "+Prefix+"/{app}/environments/{env}/rollback", wrap(http.HandlerFunc(h.rollbackEnvironment)))
	mux.Handle("GET "+Prefix+"/{app}/variables", wrap(http.HandlerFunc(h.variables)))
	mux.Handle("PUT "+Prefix+"/{app}/variables/{name}", wrap(http.HandlerFunc(h.setVariable)))
	mux.Handle("DELETE "+Prefix+"/{app}/variables/{name}", wrap(http.HandlerFunc(h.deleteVariable)))
}

// environmentsNamed is the environments a create names, in order.
func environmentsNamed(names []string) []application.Environment {
	out := make([]application.Environment, 0, len(names))
	for _, n := range names {
		out = append(out, application.Environment{Name: n})
	}
	return out
}

// The documented request forms.
type (
	environmentRequest struct {
		Name string `json:"name" example:"stage"`
		// After is the environment the new one follows; empty puts it first.
		After            string `json:"after" example:"dev"`
		RequiresApproval bool   `json:"requires_approval"`
	}
	environmentPatch struct {
		RequiresApproval bool `json:"requires_approval"`
	}
	variableRequest struct {
		// Environment is the environment the value is for; empty sets the
		// application-wide default.
		Environment string `json:"environment" example:"prod"`
		Value       string `json:"value" example:"https://pay.example.com"`
	}
	variablesResponse struct {
		Variables []application.Variable `json:"variables"`
	}
)

// addEnvironment adds an environment.
//
// @Summary      Add an application environment
// @Description  Adds an environment after another (after; empty puts it first, and none may follow the last, which /endpoints serves). It runs no release until one is promoted or released to it. requires_approval makes an agent's promote or rollback into it a person's to carry out. For administrators.
// @Param        app   path  string              true  "Application name or id"
// @Param        body  body  environmentRequest  true  "The environment"
// @Success      200  {object}  application.Application
// @Tags         Applications
// @Accept       json
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/environments [post]
func (h *handler) addEnvironment(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	req, ok2 := decode[environmentRequest](w, r)
	if !ok || !ok2 {
		return
	}
	a, err := h.cfg.Service.AddEnvironment(r.Context(), actor, r.PathValue(pathApp),
		application.Environment{Name: req.Name, RequiresApproval: req.RequiresApproval}, req.After)
	h.record(r, actor, apptool.CmdAddEnvironment, map[string]any{keyApplication: r.PathValue(pathApp), keyEnv: req.Name, "after": req.After}, err)
	h.answer(w, a, err)
}

// setEnvironment changes whether an environment requires approval.
//
// @Summary      Change an application environment
// @Description  Sets requires_approval: an agent's promote or rollback into the environment is refused, for a person to carry out here. For administrators.
// @Param        app   path  string            true  "Application name or id"
// @Param        env   path  string            true  "Environment"
// @Param        body  body  environmentPatch  true  "The setting"
// @Success      200  {object}  application.Application
// @Tags         Applications
// @Accept       json
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/environments/{env} [patch]
func (h *handler) setEnvironment(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	req, ok2 := decode[environmentPatch](w, r)
	if !ok || !ok2 {
		return
	}
	a, err := h.cfg.Service.SetEnvironmentApproval(r.Context(), actor, r.PathValue(pathApp), r.PathValue(pathEnv), req.RequiresApproval)
	h.record(r, actor, apptool.CmdSetEnvironment, map[string]any{keyApplication: r.PathValue(pathApp), keyEnv: r.PathValue(pathEnv), "requires_approval": req.RequiresApproval}, err)
	h.answer(w, a, err)
}

// removeEnvironment removes an environment other than the last.
//
// @Summary      Remove an application environment
// @Description  Removes an environment and its variables. The last environment, which /endpoints serves, cannot be removed. For administrators.
// @Param        app  path  string  true  "Application name or id"
// @Param        env  path  string  true  "Environment"
// @Success      200  {object}  application.Application
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/environments/{env} [delete]
func (h *handler) removeEnvironment(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	a, err := h.cfg.Service.RemoveEnvironment(r.Context(), actor, r.PathValue(pathApp), r.PathValue(pathEnv))
	h.record(r, actor, apptool.CmdRemoveEnvironment, map[string]any{keyApplication: r.PathValue(pathApp), keyEnv: r.PathValue(pathEnv)}, err)
	h.answer(w, a, err)
}

// promote copies an environment's release to the next.
//
// @Summary      Promote an application environment
// @Description  The environment after {env} runs the release {env} runs. It applies no migration: an application's migrations move with its first environment. For administrators.
// @Param        app  path  string  true  "Application name or id"
// @Param        env  path  string  true  "The environment promoted from"
// @Success      200  {object}  appsvc.Promoted
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/environments/{env}/promote [post]
func (h *handler) promote(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	p, err := h.cfg.Service.Promote(r.Context(), actor, r.PathValue(pathApp), r.PathValue(pathEnv))
	h.record(r, actor, apptool.CmdPromote, map[string]any{keyApplication: r.PathValue(pathApp), keyEnv: r.PathValue(pathEnv)}, err)
	h.answer(w, p, err)
}

// rollbackEnvironment moves one environment back.
//
// @Summary      Roll back an application environment
// @Description  {env} runs an earlier release: to_release, or the one before the release it runs. In the last environment it is the application's rollback, recorded as a release; in another it points the environment at the earlier release. For administrators.
// @Param        app   path  string           true  "Application name or id"
// @Param        env   path  string           true  "Environment"
// @Param        body  body  rollbackRequest  true  "The release"
// @Success      200  {object}  application.Release
// @Tags         Applications
// @Accept       json
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/environments/{env}/rollback [post]
func (h *handler) rollbackEnvironment(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	req, ok2 := decode[rollbackRequest](w, r)
	if !ok || !ok2 {
		return
	}
	rel, err := h.cfg.Service.RollbackIn(r.Context(), actor, r.PathValue(pathApp), r.PathValue(pathEnv), req.ToRelease)
	h.record(r, actor, apptool.CmdRollback, map[string]any{keyApplication: r.PathValue(pathApp), keyEnv: r.PathValue(pathEnv), "to_release": req.ToRelease}, err)
	h.answer(w, rel, err)
}

// variables lists an application's variables.
//
// @Summary      List an application's variables
// @Description  Each NAME=value, per environment, and the application-wide defaults (environment empty). A value is plain text; one referencing a stored secret holds {{secret:<name>}}, never the secret. For the owner and administrators.
// @Param        app  path  string  true  "Application name or id"
// @Success      200  {object}  variablesResponse
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/variables [get]
func (h *handler) variables(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	vars, err := h.cfg.Service.Variables(r.Context(), actor, r.PathValue(pathApp))
	h.answer(w, variablesResponse{Variables: vars}, err)
}

// setVariable writes one variable.
//
// @Summary      Set an application variable
// @Description  Writes NAME=value for an environment, or the application-wide default when environment is empty. The next run reads it; no release is needed. For the owner and administrators.
// @Param        app   path  string           true  "Application name or id"
// @Param        name  path  string           true  "Variable name"
// @Param        body  body  variableRequest  true  "The value"
// @Success      200  {object}  variablesResponse
// @Tags         Applications
// @Accept       json
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/variables/{name} [put]
func (h *handler) setVariable(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	req, ok2 := decode[variableRequest](w, r)
	if !ok || !ok2 {
		return
	}
	v := application.Variable{Environment: req.Environment, Name: r.PathValue(pathVar), Value: req.Value}
	err := h.cfg.Service.SetVariable(r.Context(), actor, r.PathValue(pathApp), v)
	// The value is never recorded: an audit row names what changed.
	h.record(r, actor, apptool.CmdSetVariable, map[string]any{keyApplication: r.PathValue(pathApp), keyEnv: req.Environment, keyVariable: v.Name}, err)
	h.answerVariables(w, r, actor, err)
}

// deleteVariable removes one variable.
//
// @Summary      Delete an application variable
// @Description  Removes NAME from an environment (?environment=), or the application-wide default when it is absent. For the owner and administrators.
// @Param        app          path   string  true   "Application name or id"
// @Param        name         path   string  true   "Variable name"
// @Param        environment  query  string  false  "Environment; the default when absent"
// @Success      200  {object}  variablesResponse
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/variables/{name} [delete]
func (h *handler) deleteVariable(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	env := r.URL.Query().Get(keyEnv)
	err := h.cfg.Service.DeleteVariable(r.Context(), actor, r.PathValue(pathApp), env, r.PathValue(pathVar))
	h.record(r, actor, apptool.CmdDeleteVariable, map[string]any{keyApplication: r.PathValue(pathApp), keyEnv: env, keyVariable: r.PathValue(pathVar)}, err)
	h.answerVariables(w, r, actor, err)
}

// answerVariables answers a variable write with the variables as they now
// stand.
func (h *handler) answerVariables(w http.ResponseWriter, r *http.Request, actor application.Actor, err error) {
	if err != nil {
		h.answer(w, nil, err)
		return
	}
	vars, err := h.cfg.Service.Variables(r.Context(), actor, r.PathValue(pathApp))
	h.answer(w, variablesResponse{Variables: vars}, err)
}
