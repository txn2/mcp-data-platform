package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// These tests cover #1219: the review path shows a pending claim beside the
// warehouse state the platform can observe for the entities the claim is about.
// They drive the assembled admin handler (real mux, real routes) so what they
// assert is the payload a reviewer's browser receives.

const observedURN = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.orders,PROD)"

// availabilityProvider answers GetTableAvailability from a fixed table and
// leaves the rest of the provider surface at its noop behavior.
type availabilityProvider struct {
	query.NoopProvider
	byURN map[string]*query.TableAvailability
}

func (p *availabilityProvider) GetTableAvailability(_ context.Context, urn string) (*query.TableAvailability, error) {
	if avail, ok := p.byURN[urn]; ok {
		return avail, nil
	}
	return &query.TableAvailability{Available: false}, nil
}

func observedProvider(estimate *int64) *availabilityProvider {
	return &availabilityProvider{byURN: map[string]*query.TableAvailability{
		observedURN: {
			Available:     true,
			QueryTable:    "iceberg.retail.orders",
			Connection:    "primary",
			EstimatedRows: estimate,
		},
	}}
}

func int64p(n int64) *int64 { return new(n) }

func pendingInsight(text string) knowledge.Insight {
	return knowledge.Insight{
		ID:               "ins-1",
		InsightText:      text,
		Status:           knowledge.StatusPending,
		EntityURNs:       []string{observedURN},
		RelatedColumns:   []knowledge.RelatedColumn{},
		SuggestedActions: []knowledge.SuggestedAction{},
	}
}

// serveInsights drives one request through the assembled admin handler.
func serveInsights(t *testing.T, store *mockInsightStore, provider query.Provider, path string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(Deps{Knowledge: NewKnowledgeHandler(store, nil, nil, nil, nil, provider)}, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	return w
}

// observedOf reads the observed_entities array off one insight object.
func observedOf(t *testing.T, insight map[string]any) []any {
	t.Helper()
	obs, ok := insight["observed_entities"].([]any)
	require.True(t, ok, "insight carries observed_entities: %v", insight)
	return obs
}

func TestListInsightsCarriesObservedWarehouseState(t *testing.T) {
	store := &mockInsightStore{listResult: []mockListResult{{
		insights: []knowledge.Insight{pendingInsight("The orders table is the system of record.")},
		total:    1,
	}}}

	w := serveInsights(t, store, observedProvider(int64p(1200)), "/api/v1/admin/knowledge/insights")

	var body struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)

	obs := observedOf(t, body.Data[0])
	require.Len(t, obs, 1)
	entity, ok := obs[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, observedURN, entity["urn"])
	assert.Equal(t, "iceberg.retail.orders", entity["query_table"])
	assert.Equal(t, "primary", entity["connection"])
	assert.InDelta(t, 1200, entity["estimated_rows"], 0)
	assert.NotContains(t, entity, "conflict", "a claim stating no number conflicts with nothing")
}

func TestGetInsightCarriesObservedWarehouseState(t *testing.T) {
	insight := pendingInsight("The orders table is the system of record.")
	store := &mockInsightStore{getResult: &insight}

	w := serveInsights(t, store, observedProvider(int64p(1200)), "/api/v1/admin/knowledge/insights/ins-1")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ins-1", body["id"], "the stored record is served unchanged beside the observation")

	obs := observedOf(t, body)
	require.Len(t, obs, 1)
	entity, ok := obs[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "iceberg.retail.orders", entity["query_table"])
}

func TestListInsightsFlagsClaimAgainstObservedRows(t *testing.T) {
	tests := []struct {
		name         string
		claim        string
		estimate     *int64
		wantConflict bool
	}{
		{
			name:         "stated count differs from the estimate",
			claim:        "The orders table holds 1140 rows.",
			estimate:     int64p(1200),
			wantConflict: true,
		},
		{
			name:     "stated count matches the estimate",
			claim:    "The orders table holds 1200 rows.",
			estimate: int64p(1200),
		},
		{
			name:     "claim states no count",
			claim:    "The amount column is gross margin, not revenue.",
			estimate: int64p(1200),
		},
		{
			name:     "provider estimates no rows",
			claim:    "The orders table holds 1140 rows.",
			estimate: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &mockInsightStore{listResult: []mockListResult{{
				insights: []knowledge.Insight{pendingInsight(tt.claim)},
				total:    1,
			}}}

			w := serveInsights(t, store, observedProvider(tt.estimate), "/api/v1/admin/knowledge/insights")

			var body struct {
				Data []map[string]any `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			entity, ok := observedOf(t, body.Data[0])[0].(map[string]any)
			require.True(t, ok)

			if !tt.wantConflict {
				assert.NotContains(t, entity, "conflict")
				return
			}
			conflict, ok := entity["conflict"].(map[string]any)
			require.True(t, ok, "the advisory marker is present: %v", entity)
			assert.InDelta(t, 1140, conflict["claimed_rows"], 0)
			assert.InDelta(t, 1200, conflict["observed_rows"], 0)
			assert.Equal(t, "claim states 1140; the table currently estimates 1200", conflict["message"])
		})
	}
}

// TestInsightPayloadUnchangedWithoutObservation pins the degrade path: with no
// query provider, a noop one, or a URN nothing resolves, the review path must
// serve the bytes it served before it could observe anything.
func TestInsightPayloadUnchangedWithoutObservation(t *testing.T) {
	insights := []knowledge.Insight{
		pendingInsight("The orders table holds 1140 rows."),
		{ID: "ins-2", InsightText: "no entities", Status: knowledge.StatusPending},
	}

	// The payload as it was before insights could carry an observation.
	type legacyListResponse struct {
		Data    []knowledge.Insight `json:"data"`
		Total   int                 `json:"total"`
		Page    int                 `json:"page"`
		PerPage int                 `json:"per_page"`
	}
	want, err := json.Marshal(legacyListResponse{Data: insights, Total: 2, Page: 1, PerPage: 20})
	require.NoError(t, err)

	providers := map[string]query.Provider{
		"no provider":     nil,
		"noop provider":   query.NewNoopProvider(),
		"nothing resolve": &availabilityProvider{},
	}
	for name, provider := range providers {
		t.Run(name, func(t *testing.T) {
			store := &mockInsightStore{listResult: []mockListResult{{insights: insights, total: 2}}}
			w := serveInsights(t, store, provider, "/api/v1/admin/knowledge/insights")
			// TrimSpace only drops the encoder's trailing newline; every byte
			// of the payload itself must match.
			assert.Equal(t, string(want), strings.TrimSpace(w.Body.String()),
				"byte-identical, not merely equivalent")
		})
	}
}

// TestListInsightsSkipsDecidedInsights: only a pending claim is still a
// decision, so a reviewed one costs no warehouse lookup and carries no block.
func TestListInsightsSkipsDecidedInsights(t *testing.T) {
	decided := pendingInsight("The orders table holds 1140 rows.")
	decided.ID = "ins-2"
	decided.Status = knowledge.StatusApplied

	store := &mockInsightStore{listResult: []mockListResult{{
		insights: []knowledge.Insight{decided},
		total:    1,
	}}}

	w := serveInsights(t, store, observedProvider(int64p(1200)), "/api/v1/admin/knowledge/insights")

	var body struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.NotContains(t, body.Data[0], "observed_entities")
}
