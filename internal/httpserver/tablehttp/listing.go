package tablehttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// The cross-source listing (#1472).
//
// Every other read in this package is keyed by one source, so the only way to
// find out what a deployment has registered was to open every asset and every
// resource in turn. The scratch schema is shared -- everyone granted the
// connection sees every table in it -- so that left a reader able to query a
// table the portal gave them no way to find, identify, or tell was current.
//
// Visibility here follows the CONNECTION, which is what Trino itself will
// apply: a caller sees the registrations on the connections their persona is
// granted, and an administrator sees all of them. It is deliberately not the
// register form's connection list, which narrows further to connections that
// carry a scratch target and accept writes; a connection turned read-only
// after a registration would otherwise hide that table from the person who
// made it, while they could still query it.

// Visibility reports the connections a caller may see registrations on. all
// lifts the boundary entirely, which is what an administrator gets.
//
// A nil Visibility on the handler shows an administrator everything and
// everyone else nothing, which is the fail-closed reading of a deployment that
// cannot enumerate its connections.
type Visibility func(ctx context.Context, caller tableregister.Caller) (connections []string, all bool)

// scratchSource names the record a registration was built over, as the listing
// renders it. The portal turns kind and id into the address it opens; the
// server does not know the portal's routes and does not need to.
type scratchSource struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Missing says the source record is no longer there. Deleting a file
	// unregisters its tables, so this is the residue of a cleanup that did not
	// complete -- a table over a directory whose object is gone, which a
	// reader has to be told about rather than shown as an ordinary row.
	Missing bool `json:"missing"`
}

// scratchTableView is one registration as the cross-source listing renders it:
// the record, what the per-source panel already adds to it, and the two things
// only a cross-source read can answer.
type scratchTableView struct {
	registrationView
	Source scratchSource `json:"source"`
	// CanUnregister is whether this caller is offered the action, by the rule
	// the per-kind DELETE route applies: authority over the source, and having
	// registered the table or being an administrator.
	CanUnregister bool `json:"can_unregister"`
}

// scratchTableList is a page of the listing.
type scratchTableList struct {
	Data    []scratchTableView `json:"data"`
	Total   int                `json:"total"`
	Page    int                `json:"page"`
	PerPage int                `json:"per_page"`
}

// listAll handles GET /api/v1/tables.
func (h *Handler) listAll(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	filter, permitted := h.filterFor(r.Context(), caller, q)
	if !permitted {
		// The caller named a connection they do not reach. It is answered as
		// an empty page rather than as a refusal: the parameter is a facet of
		// a listing they are allowed to read, and a 403 on it would confirm
		// that the connection exists.
		writeJSON(w, http.StatusOK, emptyListing(filter))
		return
	}

	regs, total, err := h.deps.Registrar.List(r.Context(), filter)
	if err != nil {
		problem(w, http.StatusInternalServerError, "could not read the registered tables")
		slog.Warn("table registrations: listing failed", "error", logsan.SanitizeForLog(err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, scratchTableList{
		Data:    h.viewsOf(r.Context(), caller, regs),
		Total:   total,
		Page:    pageOf(filter),
		PerPage: filter.EffectiveLimit(),
	})
}

// getOne handles GET /api/v1/tables/{regID}.
func (h *Handler) getOne(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	reg, err := h.deps.Registrar.Visible(r.Context(), caller, r.PathValue("regID"))
	if err != nil {
		if errors.Is(err, tableregister.ErrNotFound) {
			problem(w, http.StatusNotFound, "no such registered table")
			return
		}
		refuse(w, "reading a registered table", err)
		return
	}
	views := h.viewsOf(r.Context(), caller, []tableregister.Registration{*reg})
	writeJSON(w, http.StatusOK, views[0])
}

// caller authenticates the request and builds the registrar's view of who is
// asking.
func (h *Handler) caller(w http.ResponseWriter, r *http.Request) (tableregister.Caller, bool) {
	user := portal.GetUser(r.Context())
	if user == nil {
		problem(w, http.StatusUnauthorized, "authentication required")
		return tableregister.Caller{}, false
	}
	return h.callerOf(user), true
}

// filterFor builds the store filter from the caller's reach and the query
// parameters. permitted is false when the caller asked for a connection they
// do not reach, which matches nothing.
func (h *Handler) filterFor(
	ctx context.Context, caller tableregister.Caller, q url.Values,
) (filter tableregister.Filter, permitted bool) {
	reachable, all := h.reach(ctx, caller)
	filter = tableregister.Filter{
		AllConnections: all,
		Connections:    reachable,
		SourceKind:     sourceKindParam(q.Get("kind")),
		Query:          q.Get("q"),
		Limit:          httpjson.ParseLimit(q),
	}
	filter.Offset = httpjson.ParsePageOffset(q, filter.EffectiveLimit())

	if name := strings.TrimSpace(q.Get("connection")); name != "" {
		if !all && !slices.Contains(reachable, name) {
			return filter, false
		}
		filter.AllConnections = false
		filter.Connections = []string{name}
	}
	return filter, true
}

// reach is the connections this caller may see registrations on.
func (h *Handler) reach(ctx context.Context, caller tableregister.Caller) (connections []string, all bool) {
	if h.deps.Visible == nil {
		return nil, caller.IsAdmin
	}
	return h.deps.Visible(ctx, caller)
}

// sourceKindParam keeps the kind facet to the two kinds that exist. Anything
// else is dropped rather than passed through, so a typed parameter cannot
// silently empty a listing.
func sourceKindParam(kind string) string {
	switch kind {
	case tableregister.KindResource, tableregister.KindAsset:
		return kind
	default:
		return ""
	}
}

// pageOf renders the 1-based page a filter is on.
func pageOf(f tableregister.Filter) int {
	return f.Offset/f.EffectiveLimit() + 1
}

// emptyListing is the page a filter that matches nothing produces.
func emptyListing(f tableregister.Filter) scratchTableList {
	return scratchTableList{
		Data:    []scratchTableView{},
		Page:    pageOf(f),
		PerPage: f.EffectiveLimit(),
	}
}

// viewsOf renders a page of registrations, resolving the sources they name in
// one read per kind rather than one per row.
func (h *Handler) viewsOf(
	ctx context.Context, caller tableregister.Caller, regs []tableregister.Registration,
) []scratchTableView {
	sources := h.sourcesFor(ctx, caller, regs)
	views := make([]scratchTableView, 0, len(regs))
	for _, reg := range regs {
		ref, found := sources[sourceKey(reg.SourceKind, reg.SourceID)]
		views = append(views, scratchViewOf(reg, ref, found, caller))
	}
	return views
}

// sourcesFor resolves every source a page names, keyed by kind and id.
func (h *Handler) sourcesFor(
	ctx context.Context, caller tableregister.Caller, regs []tableregister.Registration,
) map[string]tableregister.SourceRef {
	out := make(map[string]tableregister.SourceRef, len(regs))
	if h.deps.Sources == nil {
		return out
	}
	for kind, ids := range idsByKind(regs) {
		for id, ref := range h.deps.Sources(ctx, kind, ids, caller) {
			out[sourceKey(kind, id)] = ref
		}
	}
	return out
}

// idsByKind groups the distinct source ids a page names by their kind.
func idsByKind(regs []tableregister.Registration) map[string][]string {
	byKind := make(map[string][]string, 2)
	seen := make(map[string]struct{}, len(regs))
	for _, reg := range regs {
		key := sourceKey(reg.SourceKind, reg.SourceID)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		byKind[reg.SourceKind] = append(byKind[reg.SourceKind], reg.SourceID)
	}
	return byKind
}

// sourceKey addresses one source across both kinds. Ids are opaque and minted
// per kind, so the kind is part of the key rather than assumed unique.
func sourceKey(kind, id string) string { return kind + "\x00" + id }

// scratchViewOf renders one registration for the listing.
//
// A source that no longer resolves leaves staleness reported as true, which is
// what IsStale answers for a head key that is not there. The Missing flag is
// what a surface leads with: "the file this was built over is gone" is a
// different thing to tell somebody than "the table is behind the file".
func scratchViewOf(
	reg tableregister.Registration, ref tableregister.SourceRef, found bool, caller tableregister.Caller,
) scratchTableView {
	return scratchTableView{
		registrationView: registrationView{
			Registration: reg,
			QueryTable:   reg.QualifiedName(),
			SampleSQL:    tableregister.SampleJoinSQL(reg),
			Stale:        reg.IsStale(ref.Bucket, ref.HeadKey),
		},
		Source: scratchSource{
			Kind:    reg.SourceKind,
			ID:      reg.SourceID,
			Name:    ref.Name,
			Missing: !found,
		},
		CanUnregister: found && ref.CanModify && mayDrop(reg, caller),
	}
}

// mayDrop is the registrar's own half of the unregister rule: the person who
// registered the table, or an administrator.
func mayDrop(reg tableregister.Registration, caller tableregister.Caller) bool {
	if caller.IsAdmin {
		return true
	}
	return caller.Email != "" && reg.RegisteredBy == caller.Email
}
