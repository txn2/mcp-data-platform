package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

func TestExtractRevocationReasonReasonField(t *testing.T) {
	t.Parallel()
	ev := authevents.Event{
		Type:   authevents.TypeTokenDeletedRevoked,
		Detail: json.RawMessage(`{"reason":"invalid_grant"}`),
	}
	if got := extractRevocationReason(ev); got != "invalid_grant" {
		t.Errorf("extractRevocationReason = %q, want invalid_grant", got)
	}
}

func TestExtractRevocationReasonRefreshDetailField(t *testing.T) {
	t.Parallel()
	ev := authevents.Event{
		Type:   authevents.TypeRefreshFailedRevoked,
		Detail: json.RawMessage(`{"idp_error_code":"invalid_grant","duration_ms":42}`),
	}
	if got := extractRevocationReason(ev); got != "invalid_grant" {
		t.Errorf("extractRevocationReason = %q, want invalid_grant", got)
	}
}

func TestExtractRevocationReasonEmptyDetail(t *testing.T) {
	t.Parallel()
	ev := authevents.Event{Type: authevents.TypeRefreshFailedRevoked}
	if got := extractRevocationReason(ev); got != "" {
		t.Errorf("extractRevocationReason = %q, want empty", got)
	}
}

func TestExtractRevocationReasonMalformedJSON(t *testing.T) {
	t.Parallel()
	ev := authevents.Event{Detail: json.RawMessage(`not json`)}
	if got := extractRevocationReason(ev); got != "" {
		t.Errorf("extractRevocationReason on bad JSON = %q, want empty", got)
	}
}

func TestLastRevocationForNoStore(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	if got := h.lastRevocationFor(context.Background(), "mcp", "x"); got != nil {
		t.Errorf("lastRevocationFor with no store = %+v, want nil", got)
	}
}

func TestLastRevocationForNoEvents(t *testing.T) {
	t.Parallel()
	store := authevents.NewMemoryStore()
	h := &Handler{deps: Deps{AuthEventStore: store}}
	if got := h.lastRevocationFor(context.Background(), "mcp", "x"); got != nil {
		t.Errorf("lastRevocationFor with no events = %+v, want nil", got)
	}
}

func TestLastRevocationForLatestRevoked(t *testing.T) {
	t.Parallel()
	store := authevents.NewMemoryStore()
	// Insert non-revocation event (should be skipped).
	must(t, store.Insert(context.Background(), authevents.Event{
		Kind: "mcp", Name: "x", Type: authevents.TypeRefreshSucceeded,
		Actor: "u",
	}))
	// Insert revocation.
	must(t, store.Insert(context.Background(), authevents.Event{
		Kind: "mcp", Name: "x", Type: authevents.TypeTokenDeletedRevoked,
		Actor: "system:tool-call", IDPHost: "idp.example.com",
		Detail: json.RawMessage(`{"reason":"invalid_grant"}`),
	}))
	h := &Handler{deps: Deps{AuthEventStore: store}}
	got := h.lastRevocationFor(context.Background(), "mcp", "x")
	if got == nil {
		t.Fatal("lastRevocationFor returned nil for a connection with a revocation event")
	}
	if got.Reason != "invalid_grant" {
		t.Errorf("Reason = %q, want invalid_grant", got.Reason)
	}
	if got.IDPHost != "idp.example.com" {
		t.Errorf("IDPHost = %q, want idp.example.com", got.IDPHost)
	}
}

func TestConnectionAuthEventsEndpointEmpty(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/connections/mcp/x/auth-events", http.NoBody)
	r.SetPathValue(pathKeyKind, "mcp")
	r.SetPathValue(pathKeyName, "x")
	h.connectionAuthEvents(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	body, _ := io.ReadAll(w.Body)
	var events []authevents.Event
	require.NoError(t, json.Unmarshal(body, &events))
	assert.Empty(t, events)
}

func TestConnectionAuthEventsEndpointReturnsList(t *testing.T) {
	t.Parallel()
	store := authevents.NewMemoryStore()
	must(t, store.Insert(context.Background(), authevents.Event{
		Kind: "mcp", Name: "y", Type: authevents.TypeConnectStarted, Actor: "u",
	}))
	must(t, store.Insert(context.Background(), authevents.Event{
		Kind: "mcp", Name: "y", Type: authevents.TypeRefreshSucceeded,
		Actor:      authevents.SystemBackgroundRefresh,
		OccurredAt: time.Now(),
	}))
	h := &Handler{deps: Deps{AuthEventStore: store}}
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/connections/mcp/y/auth-events", http.NoBody)
	r.SetPathValue(pathKeyKind, "mcp")
	r.SetPathValue(pathKeyName, "y")
	h.connectionAuthEvents(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var got []authevents.Event
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got, 2)
}

func TestConnOAuthHandlerExportsConnoauthKey(t *testing.T) {
	t.Parallel()
	// Compile-time guard exists in the source; this test makes the
	// dependency on connoauth.Key{} visible in coverage so a future
	// rename or removal is caught by a failing test, not just a build
	// break.
	k := connoauth.Key{Kind: "mcp", Name: "x"}
	if !k.IsValid() {
		t.Fatal("expected valid key")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("must: %v", err)
	}
}
