// Package limitsinfo is the limits block platform_info reports (#2057): the
// ceilings this deployment puts on what a call returns or writes, with the
// configuration key that sets each where one does, so an agent reads them
// rather than finding them by hitting them. Each part is reported only to a
// caller whose persona can reach a tool it bounds.
package limitsinfo

import "slices"

// Limits is the block. A part is absent when the caller reaches no tool it
// bounds.
type Limits struct {
	Export   *Export   `json:"export,omitempty"`
	Query    *Query    `json:"query,omitempty"`
	PageWalk *PageWalk `json:"page_walk,omitempty"`
}

// Export bounds trino_export, api_export and graphql_export. MaxRows is
// trino_export's: a row export cut at it fails unless on_truncation is
// "warn". MaxBytes is every export's, and a result over it is refused whole.
type Export struct {
	MaxRows               int    `json:"max_rows,omitempty"`
	MaxRowsKey            string `json:"max_rows_key,omitempty"`
	MaxBytes              int64  `json:"max_bytes"`
	MaxBytesKey           string `json:"max_bytes_key"`
	DefaultTimeoutSeconds int    `json:"default_timeout_seconds"`
	MaxTimeoutSeconds     int    `json:"max_timeout_seconds"`
	MaxTimeoutKey         string `json:"max_timeout_key"`
}

// Query bounds trino_query: the rows a result holds when the call names no
// limit, the most it may name, and the query timeout. A result past the limit
// is returned cut and says so in stats.truncated.
type Query struct {
	DefaultRows    int `json:"default_rows"`
	MaxRows        int `json:"max_rows"`
	TimeoutSeconds int `json:"timeout_seconds"`
}

// PageWalk is the page bounds of a paginate walk, per tool family.
type PageWalk struct {
	API     *WalkBounds `json:"api,omitempty"`
	GraphQL *WalkBounds `json:"graphql,omitempty"`
}

// WalkBounds is one family's page bounds: the bound a walk runs under when
// paginate.max_pages is unset, and the most max_pages may be. An export walk
// stopped at the default bound fails unless on_truncation is "warn".
type WalkBounds struct {
	DefaultMaxPages int `json:"default_max_pages"`
	MaxPages        int `json:"max_pages"`
}

// Tool names each part bounds.
const (
	toolTrinoExport   = "trino_export"
	toolAPIExport     = "api_export"
	toolGraphQLExport = "graphql_export"
	toolTrinoQuery    = "trino_query"
	toolAPIInvoke     = "api_invoke_endpoint"
	toolGraphQLQuery  = "graphql_query"
)

// Configuration keys the export bounds are set by.
const (
	keyMaxRows    = "portal.export.max_rows"
	keyMaxBytes   = "portal.export.max_bytes"
	keyMaxTimeout = "portal.export.max_timeout"
)

// Build assembles the block for a caller reaching accessibleTools, from the
// configured values: export without its keys, query nil when no Trino toolkit
// is registered, and each page-walk family's bounds. It is nil when the
// caller reaches no tool any part bounds.
func Build(accessibleTools []string, export Export, query *Query, apiWalk, graphqlWalk WalkBounds) *Limits {
	reach := func(tools ...string) bool {
		return slices.ContainsFunc(tools, func(t string) bool { return slices.Contains(accessibleTools, t) })
	}
	l := &Limits{Export: exportPart(reach, export), PageWalk: walkPart(reach, apiWalk, graphqlWalk)}
	if query != nil && reach(toolTrinoQuery) {
		l.Query = query
	}
	if l.Export == nil && l.Query == nil && l.PageWalk == nil {
		return nil
	}
	return l
}

// exportPart is the export bounds with their keys, the row cap only for a
// caller reaching trino_export, or nil for a caller reaching no export.
func exportPart(reach func(...string) bool, e Export) *Export {
	if !reach(toolTrinoExport, toolAPIExport, toolGraphQLExport) {
		return nil
	}
	e.MaxBytesKey, e.MaxTimeoutKey, e.MaxRowsKey = keyMaxBytes, keyMaxTimeout, keyMaxRows
	if !reach(toolTrinoExport) {
		e.MaxRows, e.MaxRowsKey = 0, ""
	}
	return &e
}

// walkPart is the page bounds of each family the caller reaches, or nil.
func walkPart(reach func(...string) bool, api, graphql WalkBounds) *PageWalk {
	walk := &PageWalk{}
	if reach(toolAPIInvoke, toolAPIExport) {
		walk.API = &api
	}
	if reach(toolGraphQLQuery, toolGraphQLExport) {
		walk.GraphQL = &graphql
	}
	if walk.API == nil && walk.GraphQL == nil {
		return nil
	}
	return walk
}
