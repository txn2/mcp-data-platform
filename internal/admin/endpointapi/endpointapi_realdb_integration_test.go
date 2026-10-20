//go:build integration

package endpointapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprefusal"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epstore"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestRealDB_ListAndDetailReadTheAuditRowsAndRefusals is #2208 against the
// migrated schema: N requests answered (succeeded and failed) and refused at
// the bot check reach the list and the endpoint's page, each counted by
// status class or reason, with the script and its owner named.
func TestRealDB_ListAndDetailReadTheAuditRowsAndRefusals(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	scripts, bindings := scriptstore.New(db), epstore.New(db)
	sc := &script.Script{Name: "contact-form", Source: "def handle(req):\n    return {\"status\": 200}\n", OwnerEmail: "jane@example.com", Enabled: true}
	require.NoError(t, scripts.Create(ctx, sc, script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}))
	b := &epbinding.Binding{
		ScriptID: sc.ID, Name: "contact", Methods: []string{"POST"}, AuthMode: epbinding.AuthPublic,
		TimeoutSeconds: 10, MaxMemoryBytes: 64 << 20, MaxBodyBytes: 1 << 20, RateLimit: 600, CallerRateLimit: 120,
		Enabled: true, UpdatedBy: "admin@example.com",
	}
	require.NoError(t, bindings.Set(ctx, b))

	now := time.Now().UTC()
	request := func(i, status int, success bool, caller string) {
		params := map[string]any{"endpoint": "contact", "status": status, "auth_mode": "public"}
		if caller != "" {
			params["caller"], params["caller_id"] = caller, caller
		}
		raw, err := json.Marshal(params)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `
			INSERT INTO audit_logs (id, timestamp, session_id, tool_name, success, duration_ms, parameters, event_kind, created_date)
			VALUES ($1, $2, 's', 'endpoint:contact', $3, $4, $5, 'endpoint_request', ($2::timestamptz AT TIME ZONE 'UTC')::date)`,
			fmt.Sprintf("evt_%d", i), now.Add(-time.Duration(i)*time.Minute), success, int64(10*(i+1)), raw)
		require.NoError(t, err)
	}
	for i := range 5 {
		request(i, 200, true, "ann@example.com")
	}
	request(5, 404, true, "")
	request(6, 500, false, "bo@example.com")

	counter := eprefusal.NewCounter(eprefusal.NewStore(db), time.Hour)
	for range 30 {
		counter.Refused(b.ID, eprefusal.ReasonBotCheck)
	}
	counter.Refused(b.ID, eprefusal.ReasonBodyLimit)
	counter.Refused("00000000-0000-0000-0000-000000000000", eprefusal.ReasonBotCheck) // an endpoint deleted since
	require.NoError(t, counter.Flush(ctx))
	require.NoError(t, bindings.RecordFailure(ctx, epbinding.Failure{EndpointID: b.ID, Version: 1, Method: "POST", Status: 500, Error: "boom", DurationMS: 70}))

	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		DB: db, Bindings: bindings, Script: scripts.GetByID, BaseURL: "https://data.example.com",
	})
	get := func(path string, into any) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), into))
	}

	var list List
	get("/api/v1/admin/endpoints", &list)
	require.Len(t, list.Endpoints, 1)
	e := list.Endpoints[0]
	assert.Equal(t, Script{ID: sc.ID, Name: "contact-form", Owner: "jane@example.com"}, e.Script)
	assert.Equal(t, epbinding.AuthPublic, e.AuthMode)
	assert.Equal(t, int64(7), e.Traffic.Requests)
	assert.Equal(t, StatusClasses{S2xx: 5, S4xx: 1, S5xx: 1}, e.Traffic.ByStatusClass)
	assert.Equal(t, int64(1), e.Traffic.Failures)
	assert.Equal(t, int64(2), e.Traffic.Callers)
	assert.Positive(t, e.Traffic.P95MS)
	require.NotNil(t, e.Traffic.LastRequestAt)
	assert.Equal(t, int64(31), e.Refused)
	assert.Equal(t, map[string]int64{"bot_check": 30, "body_limit": 1}, e.RefusedByReason)

	var d Detail
	get("/api/v1/admin/endpoints/contact?range=week", &d)
	var answered, refused int64
	for _, bucket := range d.Series {
		answered += bucket.S2xx + bucket.S4xx + bucket.S5xx
		refused += bucket.Refused
	}
	assert.Equal(t, int64(7), answered, "every answered request is in the chart")
	assert.Equal(t, int64(31), refused, "and every refusal beside it")
	assert.Equal(t, []Caller{{Caller: "ann@example.com", Requests: 5}, {Caller: "bo@example.com", Requests: 1}}, d.TopCallers)
	require.Len(t, d.Failures, 1)
	assert.Equal(t, "boom", d.Failures[0].Error)

	swept, err := eprefusal.NewStore(db).Sweep(ctx, now.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(2), swept, "the sweep deletes the hours before its cutoff")
}
