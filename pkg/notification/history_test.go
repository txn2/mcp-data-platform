package notification

import "testing"

// TestHistoryFilterEffectiveLimit pins the paging contract both history
// surfaces depend on: an unasked-for page size is the default, and a
// caller-supplied one is clamped rather than honored without bound.
func TestHistoryFilterEffectiveLimit(t *testing.T) {
	tests := map[string]struct {
		limit int
		want  int
	}{
		"unset":         {0, DefaultHistoryLimit},
		"negative":      {-5, DefaultHistoryLimit},
		"below default": {10, 10},
		"at the cap":    {MaxHistoryLimit, MaxHistoryLimit},
		"above the cap": {MaxHistoryLimit + 1, MaxHistoryLimit},
		"absurd":        {1 << 30, MaxHistoryLimit},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := (HistoryFilter{Limit: tc.limit}).EffectiveLimit(); got != tc.want {
				t.Errorf("EffectiveLimit() = %d, want %d", got, tc.want)
			}
		})
	}
}
