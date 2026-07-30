package httpjson

import (
	"net/url"
	"strconv"
	"time"
)

// The admin list endpoints share one query-parameter vocabulary: an RFC3339
// time range, `per_page`, and a 1-based `page`. These live here rather than in
// any one surface because the audit and knowledge routes both parse them and
// now sit in different packages, and two copies of a pagination parser is how
// two surfaces quietly start paginating differently.

// ParseTimeParam parses an RFC3339 time from a query parameter. A missing or
// unparseable value yields nil, which callers treat as "no bound" — a bad
// timestamp widens the query rather than failing it.
func ParseTimeParam(q url.Values, key string) *time.Time {
	v := q.Get(key)
	if v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil
	}
	return &t
}

// ParsePageOffset parses the 1-based `page` parameter and converts it to a
// row offset against effectiveLimit. Anything absent, non-numeric, or below 1
// yields the first page.
func ParsePageOffset(q url.Values, effectiveLimit int) int {
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return (n - 1) * effectiveLimit
		}
	}
	return 0
}

// ParseLimit parses the `per_page` parameter into a limit. Zero means the
// caller expressed no preference and its own default applies.
func ParseLimit(q url.Values) int {
	if v := q.Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}
