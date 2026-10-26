// Package submissionshttp serves a form's stored submissions on its script's
// portal page (#2180): the newest rows of the form's source, sensitive fields
// decrypted for the reader and the read audited (#2179), and the same rows as
// CSV or an Excel workbook.
//
// It is mounted by scripthttp, whose ownership gate refuses everybody but the
// script's owner and an administrator before any handler here runs.
package submissionshttp

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epsubmit"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// Deps carries the reader and the ownership gate.
type Deps struct {
	Reader *epsubmit.Reader
	// Owned resolves the script in the path for a caller who owns it, or
	// writes the refusal and reports false.
	Owned func(w http.ResponseWriter, r *http.Request) (scriptID, actor string, ok bool)
	// Viewer is who the request is from, as a decrypt is audited.
	Viewer func(r *http.Request) epsubmit.Viewer
}

// Handler serves the submission routes.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// ForPortal builds the handler over the scripts handler's ownership gate and
// the portal's identity of the caller; nil, which mounts nothing, without a
// reader.
func ForPortal(scripts *scripthttp.Handler, deps scripthttp.Deps, reader *epsubmit.Reader) *Handler {
	if !reader.Available() || deps.PortalUser == nil {
		return nil
	}
	return New(Deps{
		Reader: reader, Owned: scripts.OwnedByCaller,
		Viewer: func(r *http.Request) epsubmit.Viewer {
			if u := deps.PortalUser(r); u != nil {
				return epsubmit.Viewer{UserID: u.UserID, Email: u.Email, Persona: u.Persona}
			}
			return epsubmit.Viewer{}
		},
	})
}

// Register mounts the routes, each wrapped in the portal authentication
// middleware. A nil handler mounts nothing.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if h == nil {
		return
	}
	mux.Handle("GET /api/v1/portal/scripts/{id}/submissions", wrap(http.HandlerFunc(h.list)))
	mux.Handle("GET /api/v1/portal/scripts/{id}/submissions/export", wrap(http.HandlerFunc(h.export)))
}

// list returns a form's newest submissions.
//
// @Summary      List a form's submissions
// @Description  Returns the newest submissions stored in the webhook source of the form this script is, newest first, read from webhook_form_<endpoint> on the source's Trino connection. Sensitive fields are decrypted for the reader, and a read that decrypted any is audited as form_decrypt with the fields it opened. Restricted to the script's owner and to administrators. 404 when the script was never bound as a form.
// @Tags         Scripts
// @Produce      json
// @Param        id     path   string   true   "Script ID"
// @Param        limit  query  integer  false  "How many submissions (default 50, at most 10000)"
// @Success      200  {object}  epsubmit.Page
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/submissions [get]
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, ok := h.read(w, r)
	if !ok {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, page)
}

// export returns a form's newest submissions as a file.
//
// @Summary      Export a form's submissions
// @Description  Returns the newest submissions of the form this script is as CSV or an Excel workbook: received_at, the submitter's address on a signed-in form, then each field in the order the form declares them. Sensitive fields are decrypted and the read audited, as on the list. A CSV cell a spreadsheet would read as a formula is written with a leading apostrophe. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      text/csv
// @Produce      application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Param        id      path   string   true   "Script ID"
// @Param        format  query  string   false  "csv (default) or xlsx"
// @Param        limit   query  integer  false  "How many submissions (default 10000, at most 10000)"
// @Success      200  {file}    file
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/submissions/export [get]
func (h *Handler) export(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = epsubmit.FormatCSV
	}
	if format != epsubmit.FormatCSV && format != epsubmit.FormatXLSX {
		httpjson.WriteError(w, http.StatusBadRequest, "format is csv or xlsx")
		return
	}
	if r.URL.Query().Get("limit") == "" {
		q := r.URL.Query()
		q.Set("limit", strconv.Itoa(epsubmit.MaxLimit))
		r.URL.RawQuery = q.Encode()
	}
	page, ok := h.read(w, r)
	if !ok {
		return
	}
	file, err := epsubmit.Export(page, format)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "the export could not be written")
		return
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+file.Name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(file.Data) // #nosec G705 -- an attachment of the platform's own CSV or workbook, served nosniff
}

// read is the page of submissions the request asks for, writing the refusal
// when there is none.
func (h *Handler) read(w http.ResponseWriter, r *http.Request) (epsubmit.Page, bool) {
	scriptID, _, ok := h.deps.Owned(w, r)
	if !ok {
		return epsubmit.Page{}, false
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := h.deps.Reader.Recent(r.Context(), scriptID, limit, h.deps.Viewer(r))
	switch {
	case errors.Is(err, epsubmit.ErrNoForm):
		httpjson.WriteError(w, http.StatusNotFound, err.Error())
		return epsubmit.Page{}, false
	case err != nil:
		slog.WarnContext(r.Context(), "forms: reading submissions failed", "script_id", logsan.SanitizeForLog(scriptID),
			"error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "the submissions could not be read: "+err.Error())
		return epsubmit.Page{}, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return page, true
}
