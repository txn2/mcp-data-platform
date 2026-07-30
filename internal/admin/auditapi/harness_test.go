package auditapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// mockAuditQuerier is the canned-result double for EventQuerier.
type mockAuditQuerier struct {
	queryResult         []audit.Event
	queryErr            error
	countResult         int
	countErr            error
	distinctResult      []string
	distinctErr         error
	distinctPairsResult map[string]string
	distinctPairsErr    error
	// lastQueryFilter records the filter passed to the most recent Query
	// call so handler tests can assert query-param extraction.
	lastQueryFilter audit.QueryFilter
}

func (m *mockAuditQuerier) Query(_ context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	m.lastQueryFilter = filter
	return m.queryResult, m.queryErr
}

func (m *mockAuditQuerier) Count(_ context.Context, _ audit.QueryFilter) (int, error) {
	return m.countResult, m.countErr
}

func (m *mockAuditQuerier) Distinct(_ context.Context, _ string, _, _ *time.Time) ([]string, error) {
	return m.distinctResult, m.distinctErr
}

func (m *mockAuditQuerier) DistinctPairs(_ context.Context, _, _ string, _, _ *time.Time) (map[string]string, error) {
	return m.distinctPairsResult, m.distinctPairsErr
}

// Verify interface compliance.
var _ EventQuerier = (*mockAuditQuerier)(nil)

// recordingAuditQuerier captures the most recent filter passed to Query so
// tests can assert on the parameters the handler builds.
type recordingAuditQuerier struct {
	lastQueryFilter    audit.QueryFilter
	lastDistinctColumn string
	distinctResults    map[string][]string
}

func (r *recordingAuditQuerier) Query(_ context.Context, f audit.QueryFilter) ([]audit.Event, error) {
	r.lastQueryFilter = f
	return []audit.Event{}, nil
}

func (*recordingAuditQuerier) Count(_ context.Context, _ audit.QueryFilter) (int, error) {
	return 0, nil
}

func (r *recordingAuditQuerier) Distinct(_ context.Context, column string, _, _ *time.Time) ([]string, error) {
	r.lastDistinctColumn = column
	if r.distinctResults != nil {
		if v, ok := r.distinctResults[column]; ok {
			return v, nil
		}
	}
	return []string{}, nil
}

func (*recordingAuditQuerier) DistinctPairs(_ context.Context, _, _ string, _, _ *time.Time) (map[string]string, error) {
	return map[string]string{}, nil
}

// testMux mounts the audit routes over cfg's stores.
func testMux(cfg Config) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, cfg)
	return mux
}

// decodeProblem unmarshals an RFC 9457 error body for assertions.
func decodeProblem(body []byte) httpjson.ProblemDetail {
	var pd httpjson.ProblemDetail
	_ = json.Unmarshal(body, &pd)
	return pd
}
