package audit

import "time"

// Resolution defines the time bucketing granularity for timeseries queries.
type Resolution string

const (
	// ResolutionMinute buckets by minute.
	ResolutionMinute Resolution = "minute"

	// ResolutionHour buckets by hour.
	ResolutionHour Resolution = "hour"

	// ResolutionDay buckets by day.
	ResolutionDay Resolution = "day"
)

// ValidResolutions is the set of allowed resolution values.
var ValidResolutions = map[Resolution]bool{
	ResolutionMinute: true,
	ResolutionHour:   true,
	ResolutionDay:    true,
}

// TimeseriesFilter controls timeseries query parameters.
type TimeseriesFilter struct {
	Resolution Resolution
	StartTime  *time.Time
	EndTime    *time.Time
	UserID     string
}

// TimeseriesBucket holds counts for a single time bucket.
type TimeseriesBucket struct {
	Bucket        time.Time `json:"bucket" example:"2026-04-15T14:30:00Z"`
	Count         int       `json:"count" example:"12"`
	SuccessCount  int       `json:"success_count" example:"11"`
	ErrorCount    int       `json:"error_count" example:"1"`
	AvgDurationMS float64   `json:"avg_duration_ms" example:"245.5"`
}

// BreakdownDimension defines valid group-by dimensions.
type BreakdownDimension string

const (
	// BreakdownByToolName groups by tool name.
	BreakdownByToolName BreakdownDimension = "tool_name"

	// BreakdownByUserID groups by user ID.
	BreakdownByUserID BreakdownDimension = "user_id"

	// BreakdownByPersona groups by persona.
	BreakdownByPersona BreakdownDimension = "persona"

	// BreakdownByToolkitKind groups by toolkit kind.
	BreakdownByToolkitKind BreakdownDimension = "toolkit_kind"

	// BreakdownByConnection groups by connection.
	BreakdownByConnection BreakdownDimension = "connection"
)

// ValidBreakdownDimensions is the set of allowed group-by values.
var ValidBreakdownDimensions = map[BreakdownDimension]bool{
	BreakdownByToolName:    true,
	BreakdownByUserID:      true,
	BreakdownByPersona:     true,
	BreakdownByToolkitKind: true,
	BreakdownByConnection:  true,
}

// BreakdownFilter controls breakdown query parameters.
type BreakdownFilter struct {
	GroupBy   BreakdownDimension
	Limit     int
	StartTime *time.Time
	EndTime   *time.Time
	UserID    string
	// ToolName scopes the aggregation to a specific tool. Set when a
	// caller wants per-tool stats regardless of breakdown ranking — on
	// platforms with more than Limit distinct tools active in the
	// window, low-frequency tools would otherwise fall off the top-N
	// breakdown and appear as "no calls recorded" even when they have
	// activity (#343 bug 2).
	ToolName string
}

// MetricsFilter provides common filtering for aggregate metric queries.
type MetricsFilter struct {
	StartTime *time.Time
	EndTime   *time.Time
	UserID    string
}

// BreakdownEntry holds aggregated stats for a single dimension value.
type BreakdownEntry struct {
	Dimension     string  `json:"dimension" example:"trino_query"`
	Count         int     `json:"count" example:"65"`
	SuccessRate   float64 `json:"success_rate" example:"0.95"`
	AvgDurationMS float64 `json:"avg_duration_ms" example:"320.0"`
}

// Overview holds aggregate statistics for the audit log.
type Overview struct {
	TotalCalls     int     `json:"total_calls" example:"196"`
	SuccessRate    float64 `json:"success_rate" example:"0.949"`
	AvgDurationMS  float64 `json:"avg_duration_ms" example:"522"`
	UniqueUsers    int     `json:"unique_users" example:"12"`
	UniqueTools    int     `json:"unique_tools" example:"12"`
	EnrichmentRate float64 `json:"enrichment_rate" example:"0.85"`
	ErrorCount     int     `json:"error_count" example:"10"`
}

// EnrichmentStats holds aggregate enrichment statistics.
type EnrichmentStats struct {
	TotalCalls       int     `json:"total_calls" example:"1500"`
	EnrichedCalls    int     `json:"enriched_calls" example:"1200"`
	EnrichmentRate   float64 `json:"enrichment_rate" example:"0.80"`
	FullCount        int     `json:"full_count" example:"800"`
	SummaryCount     int     `json:"summary_count" example:"300"`
	ReferenceCount   int     `json:"reference_count" example:"100"`
	NoneCount        int     `json:"none_count" example:"0"`
	TotalTokensFull  int64   `json:"total_tokens_full" example:"450000"`
	TotalTokensDedup int64   `json:"total_tokens_dedup" example:"120000"`
	TokensSaved      int64   `json:"tokens_saved" example:"330000"`
	AvgTokensFull    float64 `json:"avg_tokens_full" example:"375.0"`
	AvgTokensDedup   float64 `json:"avg_tokens_dedup" example:"100.0"`
	UniqueSessions   int     `json:"unique_sessions" example:"45"`
}

// DiscoveryStats holds discovery-before-query pattern statistics.
type DiscoveryStats struct {
	TotalSessions         int              `json:"total_sessions" example:"100"`
	DiscoverySessions     int              `json:"discovery_sessions" example:"75"`
	QuerySessions         int              `json:"query_sessions" example:"80"`
	DiscoveryBeforeQuery  int              `json:"discovery_before_query" example:"60"`
	DiscoveryRate         float64          `json:"discovery_rate" example:"0.75"`
	QueryWithoutDiscovery int              `json:"query_without_discovery" example:"20"`
	TopDiscoveryTools     []BreakdownEntry `json:"top_discovery_tools"`
}

// PerformanceStats holds latency percentile statistics.
type PerformanceStats struct {
	P50MS            float64 `json:"p50_ms" example:"320"`
	P95MS            float64 `json:"p95_ms" example:"1450"`
	P99MS            float64 `json:"p99_ms" example:"2400"`
	AvgMS            float64 `json:"avg_ms" example:"522"`
	MaxMS            float64 `json:"max_ms" example:"5200"`
	AvgResponseChars float64 `json:"avg_response_chars" example:"1850"`
	AvgRequestChars  float64 `json:"avg_request_chars" example:"120"`
}
