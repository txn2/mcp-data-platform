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
	"fmt"
	"net/url"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The kinds the listing narrows to with kind=, in the words of the listing's
// Kind column (#1970): a script is the kind that runs, a library the kind
// other scripts load.
const (
	KindScript  = "script"
	KindLibrary = "library"
)

// ParseKind reads kind=: whether it names libraries, and whether it names a
// kind at all (an absent value lists both), or an error for a value that names
// no kind.
func ParseKind(v string) (library, named bool, err error) {
	switch v {
	case "":
		return false, false, nil
	case KindLibrary:
		return true, true, nil
	case KindScript:
		return false, true, nil
	default:
		return false, false, fmt.Errorf("unknown kind %q: must be %s or %s", v, KindScript, KindLibrary)
	}
}

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
// scripts the caller is asking about -- every script by default, their own
// with scope=mine (#1994), and every script unconditionally for an
// administrator. The NARROWING
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
	// kind=library lists libraries and kind=script the scripts that run
	// (#1941, #1970). A value that names no kind is refused by the handler
	// through ParseKind before this runs; here it lists both.
	if library, named, err := ParseKind(query.Get("kind")); err == nil && named {
		filter.Library = &library
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
	// A script is visible to everyone, and so is how it is going (#1795,
	// #1994): a non-admin listing is every script by default, its schedule and
	// its runs' status included, and scope=mine narrows it to their own.
	// What a run was given and printed stays with its owner (the run
	// projections in the handler).
	if query.Get("scope") == ScopeMine {
		filter.OwnerEmail = owner
	}
	return filter
}

// ScopeMine is the scope value that narrows a listing to the caller's own
// scripts.
const ScopeMine = "mine"

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
