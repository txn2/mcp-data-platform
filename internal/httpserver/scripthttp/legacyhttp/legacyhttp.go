// Package legacyhttp serves administrators the scripts saved before the
// authoring harness (#1943) that it has not yet caught up with: a script that
// still carries lint findings, or has no tests. Each is listed with how many
// of each it carries, so the older set can be brought up over time; a script
// with no finding and at least one test drops off the list.
//
// A script saved before the harness keeps saving as it did (#1938, #1939):
// its findings are warnings and its tests optional. This view is how an
// administrator sees what that leaves behind. It is mounted on the admin
// routes, so nobody else reaches it.
package legacyhttp

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlint"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Route is where the view is served, under the admin prefix. A literal
// segment outranks the {id} wildcard, so a script whose id is "legacy" cannot
// shadow it.
const Route = "/scripts/legacy"

// ScriptLister reads the scripts a listing filter selects, and counts them.
type ScriptLister interface {
	List(ctx context.Context, filter script.ListFilter) ([]script.Script, error)
	Count(ctx context.Context, filter script.ListFilter) (int, error)
}

// Handler serves the view.
type Handler struct {
	scripts ScriptLister
}

// New builds the handler over the script store.
func New(scripts ScriptLister) *Handler { return &Handler{scripts: scripts} }

// RegisterAdmin mounts the route under prefix, wrapped in the admin
// authentication middleware.
func (h *Handler) RegisterAdmin(mux *http.ServeMux, prefix string, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET "+prefix+Route, wrap(http.HandlerFunc(h.list)))
}

// Script is one script saved before the harness that it has not caught up
// with.
type Script struct {
	ID          string    `json:"id" example:"3f2b6c1e-8d4a-4b8e-9f1a-2c3d4e5f6a7b"`
	Name        string    `json:"name" example:"weekly-sales"`
	DisplayName string    `json:"display_name" example:"Weekly sales"`
	OwnerEmail  string    `json:"owner_email" example:"jane@example.com"`
	UpdatedAt   time.Time `json:"updated_at" example:"2026-08-13T14:30:00Z"`
	// LintFindings is how many findings the lint reports on its source, and
	// Tests how many test_* functions it has.
	LintFindings int `json:"lint_findings" example:"3"`
	Tests        int `json:"tests" example:"0"`
}

// Listing is the view. Examined is how many scripts saved before the harness
// were read, and PreHarness how many there are: a listing reads at most the
// store's page, and when the two differ the scripts past it were not
// examined.
type Listing struct {
	Data       []Script `json:"data"`
	Total      int      `json:"total" example:"2"`
	Examined   int      `json:"examined" example:"40"`
	PreHarness int      `json:"pre_harness" example:"40"`
}

// list returns the scripts saved before the harness that still carry lint
// findings or have no tests.
//
// @Summary      List the scripts saved before the authoring harness that it has not caught up with
// @Description  Returns every script saved before the formatter, lint and required tests (#1913) that still has a lint finding or no test, with the count of each. A script with no finding and at least one test is not listed. examined is how many such scripts were read (at most one store page) and pre_harness how many exist. Restricted to administrators.
// @Tags         Scripts
// @Produce      json
// @Success      200  {object}  legacyhttp.Listing
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/legacy [get]
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	filter := script.ListFilter{PreHarness: true, Sort: script.SortName}
	scripts, err := h.scripts.List(r.Context(), filter)
	if err != nil {
		slog.Error("failed to list the scripts saved before the harness", "error", err)
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list scripts")
		return
	}
	all, err := h.scripts.Count(r.Context(), filter)
	if err != nil {
		// The page is still right; only the count of what lies past it is
		// unknown, and the page's own length is the most that can be said.
		slog.Warn("failed to count the scripts saved before the harness", "error", err)
		all = len(scripts)
	}
	out := Listing{Data: []Script{}, Examined: len(scripts), PreHarness: all}
	for i := range scripts {
		sc := &scripts[i]
		row := Script{
			ID: sc.ID, Name: sc.Name, DisplayName: sc.DisplayName, OwnerEmail: sc.OwnerEmail, UpdatedAt: sc.UpdatedAt,
			LintFindings: Findings(sc), Tests: Tests(sc.Source),
		}
		if row.LintFindings > 0 || row.Tests == 0 {
			out.Data = append(out.Data, row)
		}
	}
	out.Total = len(out.Data)
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// Findings is how many lint findings sc's source carries, read as a save of
// it would read them.
func Findings(sc *script.Script) int {
	return len(scriptlint.Check(sc.Source, scriptlint.Save{Legacy: sc.Legacy, Previous: sc.Source}).Findings)
}

// Tests is how many test_* functions source defines; none when it does not
// parse.
func Tests(source string) int {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	if err != nil {
		return 0
	}
	return len(scriptdialect.Tests(file))
}
