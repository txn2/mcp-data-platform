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
