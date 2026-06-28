package knowledgepage

import "testing"

// TestPageGuardsConfig_Resolve covers the write-guard default resolution (#705):
// unset selects the documented default, an explicit value passes through, the
// disable flag / a negative value turns an arm off.
func TestPageGuardsConfig_Resolve(t *testing.T) {
	tests := []struct {
		name string
		cfg  PageGuardsConfig
		want PageGuards
	}{
		{
			name: "all unset selects defaults",
			cfg:  PageGuardsConfig{},
			want: PageGuards{DefaultDedupThreshold, DefaultOversizeBytes, DefaultOversizeSections},
		},
		{
			name: "explicit values pass through",
			cfg:  PageGuardsConfig{DedupThreshold: 0.9, OversizeBytes: 999, OversizeSections: 3},
			want: PageGuards{0.9, 999, 3},
		},
		{
			name: "dedup disabled yields zero threshold",
			cfg:  PageGuardsConfig{DedupThreshold: 0.9, DedupDisabled: true},
			want: PageGuards{0, DefaultOversizeBytes, DefaultOversizeSections},
		},
		{
			name: "negative oversize disables those arms",
			cfg:  PageGuardsConfig{OversizeBytes: -1, OversizeSections: -1},
			want: PageGuards{DefaultDedupThreshold, 0, 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.Resolve(); got != tt.want {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
