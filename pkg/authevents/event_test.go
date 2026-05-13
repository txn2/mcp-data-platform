package authevents

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestEventJSONWireFormat is the regression guard for the History
// panel: without explicit snake_case json tags, Go would marshal field
// names verbatim and the portal SPA's `event.event_type` /
// `event.occurred_at` / `event.connection_kind` lookups would return
// undefined. Round 2 of #395's review caught this; this test ensures
// it can't silently come back.
func TestEventJSONWireFormat(t *testing.T) {
	ev := Event{
		ID:         "row-id",
		OccurredAt: time.Date(2026, 5, 13, 17, 0, 0, 0, time.UTC),
		Kind:       "mcp", Name: "alpha",
		Type:    TypeRefreshSucceeded,
		Actor:   SystemBackgroundRefresh,
		IDPHost: "idp.example.com",
		Detail:  json.RawMessage(`{"duration_ms":42}`),
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	wire := string(b)
	want := []string{
		`"id":"row-id"`,
		`"occurred_at":"2026-05-13T17:00:00Z"`,
		`"connection_kind":"mcp"`,
		`"connection_name":"alpha"`,
		`"event_type":"refresh_succeeded"`,
		`"actor":"system:background-refresh"`,
		`"idp_host":"idp.example.com"`,
		`"detail":{"duration_ms":42}`,
	}
	for _, frag := range want {
		if !strings.Contains(wire, frag) {
			t.Errorf("wire format missing %q\nfull JSON: %s", frag, wire)
		}
	}
}

func TestTypeIsValid(t *testing.T) {
	valid := []Type{
		TypeConnectStarted, TypeConnectCompleted,
		TypeRefreshSucceeded, TypeRefreshFailedTransient,
		TypeRefreshFailedRevoked, TypeRefreshSkippedNoToken,
		TypeRefreshSkippedExpired, TypeRefreshRotationPersistenceFailed,
		TypeTokenDeletedRevoked, TypeTokenDeletedAdmin,
	}
	for _, ty := range valid {
		if !ty.IsValid() {
			t.Errorf("Type %q should be valid", ty)
		}
	}
	invalid := []Type{
		"", "unknown", "refresh_typo", "Connect_Started",
	}
	for _, ty := range invalid {
		if ty.IsValid() {
			t.Errorf("Type %q should be invalid", ty)
		}
	}
}

func TestEventIsValid(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		want bool
	}{
		{
			name: "complete event valid",
			ev:   Event{Kind: "mcp", Name: "x", Type: TypeConnectStarted, Actor: "u@e.com"},
			want: true,
		},
		{"missing kind", Event{Name: "x", Type: TypeConnectStarted, Actor: "u@e.com"}, false},
		{"missing name", Event{Kind: "mcp", Type: TypeConnectStarted, Actor: "u@e.com"}, false},
		{"missing type", Event{Kind: "mcp", Name: "x", Actor: "u@e.com"}, false},
		{"unknown type", Event{Kind: "mcp", Name: "x", Type: "made_up", Actor: "u@e.com"}, false},
		{"missing actor", Event{Kind: "mcp", Name: "x", Type: TypeConnectStarted}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ev.IsValid(); got != tc.want {
				t.Errorf("IsValid() = %v, want %v", got, tc.want)
			}
		})
	}
}
