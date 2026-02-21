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
}

// TimeseriesBucket holds counts for a single time bucket.
type TimeseriesBucket struct {
	Bucket        time.Time `json:"bucket"`
	Count         int       `json:"count"`
	SuccessCount  int       `json:"success_count"`
	ErrorCount    int       `json:"error_count"`
	AvgDurationMS float64   `json:"avg_duration_ms"`
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
}

// BreakdownEntry holds aggregated stats for a single dimension value.
type BreakdownEntry struct {
	Dimension     string  `json:"dimension"`
	Count         int     `json:"count"`
	SuccessRate   float64 `json:"success_rate"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
}

// Overview holds aggregate statistics for the audit log.
type Overview struct {
	TotalCalls     int     `json:"total_calls"`
	SuccessRate    float64 `json:"success_rate"`
	AvgDurationMS  float64 `json:"avg_duration_ms"`
	UniqueUsers    int     `json:"unique_users"`
	UniqueTools    int     `json:"unique_tools"`
	EnrichmentRate float64 `json:"enrichment_rate"`
	ErrorCount     int     `json:"error_count"`
}

// EnrichmentStats holds aggregate enrichment statistics.
type EnrichmentStats struct {
	TotalCalls       int     `json:"total_calls"`
	EnrichedCalls    int     `json:"enriched_calls"`
	EnrichmentRate   float64 `json:"enrichment_rate"`
	FullCount        int     `json:"full_count"`
	SummaryCount     int     `json:"summary_count"`
	ReferenceCount   int     `json:"reference_count"`
	NoneCount        int     `json:"none_count"`
	TotalTokensFull  int64   `json:"total_tokens_full"`
	TotalTokensDedup int64   `json:"total_tokens_dedup"`
	TokensSaved      int64   `json:"tokens_saved"`
	AvgTokensFull    float64 `json:"avg_tokens_full"`
	AvgTokensDedup   float64 `json:"avg_tokens_dedup"`
	UniqueSessions   int     `json:"unique_sessions"`
}

// DiscoveryStats holds discovery-before-query pattern statistics.
type DiscoveryStats struct {
	TotalSessions         int              `json:"total_sessions"`
	DiscoverySessions     int              `json:"discovery_sessions"`
	QuerySessions         int              `json:"query_sessions"`
	DiscoveryBeforeQuery  int              `json:"discovery_before_query"`
	DiscoveryRate         float64          `json:"discovery_rate"`
	QueryWithoutDiscovery int              `json:"query_without_discovery"`
	TopDiscoveryTools     []BreakdownEntry `json:"top_discovery_tools"`
}

// PerformanceStats holds latency percentile statistics.
type PerformanceStats struct {
	P50MS            float64 `json:"p50_ms"`
	P95MS            float64 `json:"p95_ms"`
	P99MS            float64 `json:"p99_ms"`
	AvgMS            float64 `json:"avg_ms"`
	MaxMS            float64 `json:"max_ms"`
	AvgResponseChars float64 `json:"avg_response_chars"`
	AvgRequestChars  float64 `json:"avg_request_chars"`
}
