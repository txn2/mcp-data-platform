package endpointapi

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Traffic is an endpoint's answered requests over a range, read from the
// audit row each one writes (event_kind endpoint_request, tool_name
// endpoint:<name>).
type Traffic struct {
	Requests int64 `json:"requests"`
	// ByStatusClass counts the requests by the class of status the handler
	// answered: 2xx, 3xx, 4xx, 5xx.
	ByStatusClass StatusClasses `json:"by_status_class"`
	// Failures are the requests whose handler failed.
	Failures int64 `json:"failures"`
	// P50MS and P95MS are the requests' durations.
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	// Callers is how many distinct signed-in callers sent requests.
	Callers int64 `json:"callers"`
	// LastRequestAt is the newest request's time, nil when there was none.
	LastRequestAt *time.Time `json:"last_request_at,omitempty"`
}

// StatusClasses counts by status class.
type StatusClasses struct {
	S2xx int64 `json:"2xx"`
	S3xx int64 `json:"3xx"`
	S4xx int64 `json:"4xx"`
	S5xx int64 `json:"5xx"`
}

// add counts n under class, the status's first digit.
func (s *StatusClasses) add(class string, n int64) {
	switch class {
	case "2":
		s.S2xx += n
	case "3":
		s.S3xx += n
	case "4":
		s.S4xx += n
	case "5":
		s.S5xx += n
	}
}

// Bucket is one stretch of the request chart.
type Bucket struct {
	Start time.Time `json:"start"`
	StatusClasses
	Refused int64 `json:"refused"`
}

// Caller is one signed-in caller and how many requests they sent.
type Caller struct {
	Caller   string `json:"caller"`
	Requests int64  `json:"requests"`
}

// trafficReader reads Traffic out of audit_logs.
type trafficReader struct{ db *sql.DB }

// endpointRows is the predicate every read shares: endpoint request rows from
// since on. created_date is the table's partition key, so naming it lets the
// planner skip the partitions before since.
const endpointRows = `event_kind = 'endpoint_request' AND timestamp >= $1 AND created_date >= ($1::timestamptz AT TIME ZONE 'UTC')::date`

// byEndpoint reads every endpoint's traffic from since on, keyed by the
// endpoint's name.
func (t trafficReader) byEndpoint(ctx context.Context, since time.Time) (map[string]*Traffic, error) {
	rows, err := t.db.QueryContext(ctx, `
		SELECT substr(tool_name, 10) AS endpoint,
		       COUNT(*),
		       COUNT(*) FILTER (WHERE left(parameters->>'status', 1) = '2'),
		       COUNT(*) FILTER (WHERE left(parameters->>'status', 1) = '3'),
		       COUNT(*) FILTER (WHERE left(parameters->>'status', 1) = '4'),
		       COUNT(*) FILTER (WHERE left(parameters->>'status', 1) = '5'),
		       COUNT(*) FILTER (WHERE NOT success),
		       COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY duration_ms), 0),
		       COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0),
		       COUNT(DISTINCT parameters->>'caller_id') FILTER (WHERE COALESCE(parameters->>'caller_id', '') <> ''),
		       MAX(timestamp)
		  FROM audit_logs
		 WHERE `+endpointRows+`
		 GROUP BY 1`, since)
	if err != nil {
		return nil, fmt.Errorf("reading endpoint traffic: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cursor; rows.Err below reports a failed read
	out := map[string]*Traffic{}
	for rows.Next() {
		var (
			name string
			tr   Traffic
			last time.Time
		)
		if err := rows.Scan(&name, &tr.Requests, &tr.ByStatusClass.S2xx, &tr.ByStatusClass.S3xx, &tr.ByStatusClass.S4xx,
			&tr.ByStatusClass.S5xx, &tr.Failures, &tr.P50MS, &tr.P95MS, &tr.Callers, &last); err != nil {
			return nil, fmt.Errorf("reading endpoint traffic: %w", err)
		}
		tr.LastRequestAt = &last
		out[name] = &tr
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading endpoint traffic: %w", err)
	}
	return out, nil
}

// series reads one endpoint's requests from since on in buckets of step,
// counted by status class.
func (t trafficReader) series(ctx context.Context, name string, since time.Time, step time.Duration) (map[int64]*StatusClasses, error) {
	rows, err := t.db.QueryContext(ctx, `
		SELECT floor(extract(epoch FROM timestamp) / $3)::bigint, left(parameters->>'status', 1), COUNT(*)
		  FROM audit_logs
		 WHERE `+endpointRows+` AND tool_name = $2
		 GROUP BY 1, 2`, since, toolName(name), int64(step/time.Second))
	if err != nil {
		return nil, fmt.Errorf("reading endpoint requests: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cursor; rows.Err below reports a failed read
	out := map[int64]*StatusClasses{}
	for rows.Next() {
		var (
			bucket int64
			class  sql.NullString
			n      int64
		)
		if err := rows.Scan(&bucket, &class, &n); err != nil {
			return nil, fmt.Errorf("reading endpoint requests: %w", err)
		}
		if out[bucket] == nil {
			out[bucket] = &StatusClasses{}
		}
		out[bucket].add(class.String, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading endpoint requests: %w", err)
	}
	return out, nil
}

// topCallers reads one endpoint's busiest signed-in callers from since on.
func (t trafficReader) topCallers(ctx context.Context, name string, since time.Time, limit int) ([]Caller, error) {
	rows, err := t.db.QueryContext(ctx, `
		SELECT parameters->>'caller', COUNT(*)
		  FROM audit_logs
		 WHERE `+endpointRows+` AND tool_name = $2 AND COALESCE(parameters->>'caller', '') <> ''
		 GROUP BY 1
		 ORDER BY 2 DESC, 1
		 LIMIT $3`, since, toolName(name), limit)
	if err != nil {
		return nil, fmt.Errorf("reading endpoint callers: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cursor; rows.Err below reports a failed read
	out := make([]Caller, 0)
	for rows.Next() {
		var c Caller
		if err := rows.Scan(&c.Caller, &c.Requests); err != nil {
			return nil, fmt.Errorf("reading endpoint callers: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading endpoint callers: %w", err)
	}
	return out, nil
}

// toolName is the audit tool_name an endpoint's requests are recorded under.
func toolName(endpoint string) string { return "endpoint:" + endpoint }
