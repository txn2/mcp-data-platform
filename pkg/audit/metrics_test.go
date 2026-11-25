package audit

import "testing"

func TestBreakdownDimensionNames(t *testing.T) {
	const want = "connection, event_kind, persona, tool_name, toolkit_kind, user_id"
	if got := BreakdownDimensionNames(); got != want {
		t.Errorf("BreakdownDimensionNames() = %q, want %q", got, want)
	}
}
