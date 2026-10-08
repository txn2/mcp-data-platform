package limitsinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	export = Export{MaxRows: 100000, MaxBytes: 1 << 20, DefaultTimeoutSeconds: 300, MaxTimeoutSeconds: 600}
	query  = &Query{DefaultRows: 1000, MaxRows: 10000, TimeoutSeconds: 120}
	api    = WalkBounds{DefaultMaxPages: 100, MaxPages: 10000}
	gql    = WalkBounds{DefaultMaxPages: 10, MaxPages: 1000}
)

func forCaller(tools ...string) *Limits { return Build(tools, export, query, api, gql) }

func TestBuildReportsWhatTheCallerReaches(t *testing.T) {
	assert.Equal(t, &Limits{
		Export: &Export{
			MaxRows: 100000, MaxRowsKey: "portal.export.max_rows", MaxBytes: 1 << 20, MaxBytesKey: "portal.export.max_bytes",
			DefaultTimeoutSeconds: 300, MaxTimeoutSeconds: 600, MaxTimeoutKey: "portal.export.max_timeout",
		},
		Query: &Query{DefaultRows: 1000, MaxRows: 10000, TimeoutSeconds: 120},
		PageWalk: &PageWalk{
			API:     &WalkBounds{DefaultMaxPages: 100, MaxPages: 10000},
			GraphQL: &WalkBounds{DefaultMaxPages: 10, MaxPages: 1000},
		},
	}, forCaller("trino_export", "trino_query", "api_export", "graphql_query"))

	apiOnly := forCaller("api_export")
	assert.Zero(t, apiOnly.Export.MaxRows, "the row cap is trino_export's, so a caller without it is not told one")
	assert.Empty(t, apiOnly.Export.MaxRowsKey)
	assert.Nil(t, apiOnly.Query)
	assert.Nil(t, apiOnly.PageWalk.GraphQL)

	assert.Nil(t, Build([]string{"trino_query"}, export, nil, api, gql), "no Trino toolkit, nothing to bound")
	assert.Nil(t, forCaller("platform_info", "search"), "a caller reaching no bounded tool gets no block")
}
