package audit

import "testing"

func TestBreakdownDimensionNames(t *testing.T) {
	const want = "connection, error_category, event_kind, persona, source, tool_name, toolkit_kind, user_id"
	if got := BreakdownDimensionNames(); got != want {
		t.Errorf("BreakdownDimensionNames() = %q, want %q", got, want)
	}
}
