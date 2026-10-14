// Package catalogimportapi is the admin REST surface for the import of a
// deployment's DataHub into the platform catalog (#1923): start an import from
// the DataHub the configuration names (#2124) or one an administrator names,
// and read what each run did.
package catalogimportapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/catalogimport"
)

// importsPath is the route an import is started at and its runs are read from.
const importsPath = "/api/v1/admin/catalog/imports"

// maxBodyBytes bounds a start request.
const maxBodyBytes = 16 << 10

// listedRuns is how many runs a read returns.
const listedRuns = 10

// Runner starts imports and lists their runs. *catalogimport.Runner
// satisfies it.
type Runner interface {
	Start(ctx context.Context, address, token, by string) (catalogimport.Run, error)
	StartConfigured(ctx context.Context, by string) (catalogimport.Run, error)
	Runs(ctx context.Context, limit int) ([]catalogimport.Run, error)
	Configured() string
}

// Config carries the runner and the parent-owned helpers.
type Config struct {
	// Runner runs imports. nil mounts nothing.
	Runner Runner
	// Author resolves the acting admin.
	Author func(*http.Request) string
}

// StartRequest names the DataHub an import reads. An empty request, or no
// body, reads the DataHub the configuration names.
type StartRequest struct {
	// URL is DataHub's GMS address, such as https://datahub.example.com:8080.
	URL string `json:"url,omitempty" example:"https://datahub.example.com:8080"`
	// Token is a DataHub personal access token. It is used for this run and
	// not kept.
	Token string `json:"token,omitempty"`
}

// RunList is the latest runs, newest first, and the DataHub the
// configuration names.
type RunList struct {
	Imports []catalogimport.Run `json:"imports"`
	// Configured is the address of the DataHub the configuration names in
	// toolkits.datahub, which the platform imports from after its first
	// catalog scan; empty when it names none.
	Configured string `json:"configured,omitempty" example:"https://datahub.example.com:8080"`
}

type handler struct{ cfg Config }

// Register mounts the routes, each behind wrap, the admin API's
// authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Runner == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+importsPath, wrap(http.HandlerFunc(h.list)))
	mux.Handle("POST "+importsPath, wrap(http.HandlerFunc(h.start)))
}

// list handles GET /api/v1/admin/catalog/imports.
//
// @Summary      List DataHub imports
// @Description  Returns the latest imports from DataHub into the platform catalog, newest first, and the DataHub the configuration names, which the platform imports from after its first catalog scan until a run against it finishes without error: who started each, against which DataHub, whether it is running, and its report when it finished. The report counts the definitions and datasets imported, the aspects written, left unchanged, and kept because something other than the import wrote them in the catalog, the lineage edges recorded, the stored references rewritten, and lists what was skipped and why.
// @Tags         Catalog
// @Produce      json
// @Success      200  {object}  RunList
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/catalog/imports [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	runs, err := h.cfg.Runner.Runs(r.Context(), listedRuns)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading the imports failed")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, RunList{Imports: runs, Configured: h.cfg.Runner.Configured()})
}

// start handles POST /api/v1/admin/catalog/imports.
//
// @Summary      Import from DataHub
// @Description  Starts an import of a DataHub into the platform catalog and answers 202 at once; GET reports the run. An empty body reads the DataHub the configuration names, and answers 400 when it names none. It reads every dataset, the ones DataHub marked removed included, and every glossary node, glossary term, tag, domain and data product. A dataset's curation lands on the catalog dataset with the same path, so scan the connections first; a removed record fills what the live record of the same path lacks. An aspect something other than the import wrote in the catalog is kept. It then rewrites the DataHub URNs stored in memories, insights, changesets, knowledge pages and call records into catalog references. Running it again writes only what changed. Answers 409 while an import is running.
// @Tags         Catalog
// @Accept       json
// @Produce      json
// @Param        body  body      StartRequest  false  "The DataHub to import; empty for the configured one"
// @Success      202   {object}  catalogimport.Run
// @Failure      400   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Failure      500   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/catalog/imports [post]
func (h *handler) start(w http.ResponseWriter, r *http.Request) {
	var in StartRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		httpjson.WriteError(w, http.StatusBadRequest, fmt.Sprintf("the body is not an import request: %v", err))
		return
	}
	var run catalogimport.Run
	var err error
	if in.configured() {
		run, err = h.cfg.Runner.StartConfigured(r.Context(), h.author(r))
	} else {
		if verr := in.validate(); verr != nil {
			httpjson.WriteError(w, http.StatusBadRequest, verr.Error())
			return
		}
		run, err = h.cfg.Runner.Start(r.Context(), strings.TrimSpace(in.URL), in.Token, h.author(r))
	}
	switch {
	case errors.Is(err, catalogimport.ErrRunning):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	case err != nil:
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpjson.WriteJSON(w, http.StatusAccepted, run)
	}
}

// configured reports whether the request names no DataHub, so the one the
// configuration names is read.
func (in StartRequest) configured() bool {
	return strings.TrimSpace(in.URL) == "" && strings.TrimSpace(in.Token) == ""
}

// validate requires an http or https URL and a token.
func (in StartRequest) validate() error {
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("url must be DataHub's http or https address, such as https://datahub.example.com:8080")
	}
	if strings.TrimSpace(in.Token) == "" {
		return errors.New("token is required: a DataHub personal access token")
	}
	return nil
}

// author resolves the acting admin, or "" with no resolver.
func (h *handler) author(r *http.Request) string {
	if h.cfg.Author == nil {
		return ""
	}
	return h.cfg.Author(r)
}
