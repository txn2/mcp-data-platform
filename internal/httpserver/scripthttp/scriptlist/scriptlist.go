// Package scriptlist is what the scripts listing is narrowed and ordered by.
//
// It holds the query string half of GET /portal/scripts: the scope that
// decides which scripts a caller is asking about, the axes that narrow
// whatever that scope selected, and the ordering the store applies ahead of
// the page cap (#1795). It takes the caller as an address and a flag rather
// than as the HTTP layer's identity type, so it can be read and tested without
// the handler and cannot import it back.
package scriptlist

import (
	"net/url"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// nonEmpty drops the empty values a repeated query parameter can carry, so
// `?tag=&tag=sales` narrows by one tag rather than by one tag and an empty
// string no script has.
func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Filter turns the query string into the listing's predicate and its
// ordering.
//
// Two different things meet here and are kept apart. The SCOPE decides which
// scripts the caller is asking about -- their own by default, every script on
// request, and every script unconditionally for an administrator. The NARROWING
// axes (category and tag, #1369; free text, #1405; author, status and enabled,
// #1795) narrow whatever that scope selected, and apply to an administrator
// exactly as they do to everybody else, because they narrow what a reader asked
// for rather than what they are entitled to. Every one of them runs in the
// store, so each covers every script the scope admits rather than the page of
// them a listing happens to hold.
//
// The ORDERING is carried through for the same reason: the store applies the
// page cap, so a column sorted anywhere else would sort the page.
//
// A nil query names no axis, which is how a caller asks for the scope alone.
func Filter(owner string, isAdmin bool, query url.Values) script.ListFilter {
	filter := script.ListFilter{
		Category: query.Get("category"),
		Tags:     nonEmpty(query["tag"]),
		Search:   query.Get("search"),
		Status:   query.Get("status"),
	}
	// A column the store does not order on leaves Sort EMPTY, which is the
	// store's "most recently updated first" -- the ordering every caller had
	// before this existed. Substituting updated_at here instead would leave
	// the DIRECTION to the caller's `dir`, so `?sort=nonsense` would answer
	// oldest-first, which is neither what was asked for nor the default.
	if col, ok := script.ParseSortColumn(query.Get("sort")); ok {
		filter.Sort = col
		filter.Desc = query.Get("dir") != "asc"
	}
	if enabled, ok := parseBool(query.Get("enabled")); ok {
		filter.Enabled = &enabled
	}
	// owner narrows to one author, and naming one is itself a way of asking
	// about somebody other than yourself: it selects the population rather
	// than being intersected with the default scope, which would answer the
	// empty set for every author but the caller.
	if owner := query.Get("owner"); owner != "" {
		filter.OwnerEmail = owner
		return filter
	}
	if isAdmin {
		return filter
	}
	// A script is visible to everyone; what is readable is not (#1795). A
	// non-admin listing is their own by default and every script on request,
	// with reportableScript withholding the source of a row they do not own
	// and attachLastRuns leaving its run state empty. scope=mine is what the
	// listing did unconditionally before.
	if query.Get("scope") == "all" {
		return filter
	}
	filter.OwnerEmail = owner
	return filter
}

// parseBool reads a query parameter that is present or absent, rather than
// true or false: an unset "enabled" must not narrow the listing to the
// disabled scripts, which is what a bare strconv.ParseBool of "" would do.
func parseBool(v string) (value, ok bool) {
	switch v {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	default:
		return false, false
	}
}
