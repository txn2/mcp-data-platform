package apigateway

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// PaginationInfo is the structured pagination state api_invoke_endpoint
// surfaces to the model on every response. The model uses HasMore +
// NextCursor (or NextURL) to decide whether to issue a follow-up call;
// the gateway does NOT auto-follow so each loop iteration stays
// observable in the conversation and audit log.
//
// Fields are populated only when the upstream response carries a
// recognizable pagination signal. When none are populated the field
// is omitted from the JSON response — the model sees no pagination
// envelope and treats the response as terminal.
type PaginationInfo struct {
	HasMore    bool   `json:"has_more,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
	NextURL    string `json:"next_url,omitempty"`
	Source     string `json:"source,omitempty"`
}

// detectPagination inspects a response's Link header and parsed JSON
// body for the common cursor patterns. Returns nil when no signal
// is found — the InvokeOutput.Pagination field stays unset and the
// JSON envelope omits it via omitempty.
//
// Detection order is significant: Link header (RFC 5988) is the
// authoritative pagination protocol and most-trustworthy when
// present, so it takes precedence over body-level cursor fields.
// Within the body, OData's `@odata.nextLink` is checked before the
// generic cursor names because OData responses commonly carry both
// (a `value` array AND a stray `next` field that means something
// else).
func detectPagination(headers http.Header, body any) *PaginationInfo {
	if info := paginationFromLinkHeader(headers); info != nil {
		return info
	}
	return paginationFromBody(body)
}

// paginationLinkRe matches one entry in an RFC 5988 Link header. The
// entire URL (including any nested commas inside parameters) lives
// between `<` and `>`; everything after the closing `>` up to the
// next entry's `<` is the relation+parameters. Compiled once per
// process — the package-level var is safe because the regex is read-
// only.
//
//nolint:gochecknoglobals // compiled regex, initialized once
var paginationLinkRe = regexp.MustCompile(`<([^>]+)>\s*;\s*([^,]+)`)

// paginationFromLinkHeader parses an RFC 5988 Link header looking
// for `rel="next"`. Returns the URL when found.
//
// Multi-value Link headers (the same header sent multiple times by
// the upstream) are joined into a single comma-separated string by
// the std library's http.Header before this function sees them.
func paginationFromLinkHeader(headers http.Header) *PaginationInfo {
	link := headers.Get("Link")
	if link == "" {
		return nil
	}
	for _, m := range paginationLinkRe.FindAllStringSubmatch(link, -1) {
		url := strings.TrimSpace(m[1])
		params := strings.ToLower(m[2])
		if !strings.Contains(params, `rel="next"`) && !strings.Contains(params, "rel=next") {
			continue
		}
		return &PaginationInfo{
			HasMore: true,
			NextURL: url,
			Source:  "link_header",
		}
	}
	return nil
}

// paginationFromBody walks a parsed JSON body looking for the
// common cursor fields. Only the top-level object is inspected;
// deeply-nested cursor fields exist in the wild but are rare enough
// that a config knob (deferred) is the right place to add them.
//
// Recognized fields, in order of specificity:
//   - "@odata.nextLink" — OData v4 (Microsoft Graph, Dynamics).
//   - "next_cursor" — Slack, Twitter, Stripe v1.
//   - "nextCursor" — camelCase variant (some Stripe / Notion APIs).
//   - "next_page_token" — Google Cloud APIs.
//   - "nextPageToken" — camelCase Google variant.
//   - "next" — Salesforce REST, generic; checked LAST because the
//     name collides with hateoas link objects that aren't true cursors.
func paginationFromBody(body any) *PaginationInfo {
	obj, ok := body.(map[string]any)
	if !ok {
		return nil
	}
	candidates := []string{
		"@odata.nextLink",
		"next_cursor",
		"nextCursor",
		"next_page_token",
		"nextPageToken",
		"next",
	}
	for _, key := range candidates {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		val := stringifyCursor(raw)
		if val == "" {
			continue
		}
		info := &PaginationInfo{
			HasMore: true,
			Source:  "body:" + key,
		}
		// URL-shaped cursor values (always for @odata.nextLink, sometimes for
		// the generic `next` field) populate NextURL so the model issues a
		// fresh GET against the URL rather than passing it as a `?cursor=`
		// param against the original path. Mis-routing a fully-qualified URL
		// into NextCursor was a real bug observed during gate review.
		if isURLLikeCursor(key, val) {
			info.NextURL = val
		} else {
			info.NextCursor = val
		}
		return info
	}
	return nil
}

// isURLLikeCursor reports whether the cursor value should be
// surfaced as a URL (NextURL) rather than as a cursor token
// (NextCursor). @odata.nextLink is always a URL per OData v4 §11.2.5.7;
// other fields are URLs only when the value parses as one with an
// http(s) scheme — defends against a stray `next: "https://..."`
// field from a non-OData API getting mis-routed.
func isURLLikeCursor(key, value string) bool {
	if key == "@odata.nextLink" {
		return true
	}
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

// stringifyCursor coerces the cursor value to a non-empty string.
// Most APIs return strings, but a few (older REST APIs) return
// integers; coerce defensively so the field is always a string in
// the JSON envelope. Anything that isn't string / int / float /
// bool returns "" so paginationFromBody skips it.
func stringifyCursor(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		// JSON numbers decode as float64; only return a value if
		// it's actually present (non-zero) so we don't synthesize
		// pagination from a `count: 0` field that happened to be
		// named `next`.
		if s == 0 {
			return ""
		}
		return formatFloatCursor(s)
	case bool:
		// Rare but seen: APIs that return `next: true` to indicate
		// "more available" without disclosing the cursor. Treat
		// true as a marker — the model can issue a follow-up but
		// has no cursor value to pass. Return a sentinel.
		if s {
			return "true"
		}
	}
	return ""
}

// base10 / float64Bits are the strconv knobs in formatFloatCursor.
// Pulling them into named constants satisfies revive's add-constant
// rule without obscuring intent.
const (
	base10      = 10
	float64Bits = 64
)

// formatFloatCursor stringifies a JSON number cursor without
// introducing decimal noise on integer values. JSON has no integer
// type so `next: 1234` decodes as float64(1234); rendering it as
// "1234" (not "1234.000000") is what every paginated API expects.
func formatFloatCursor(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), base10)
	}
	return strconv.FormatFloat(f, 'f', -1, float64Bits)
}
