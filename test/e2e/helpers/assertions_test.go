//go:build integration

package helpers

import (
	"reflect"
	"testing"
)

// TestDiffToolNames covers the three outcomes AssertToolSet reports on: the
// exact set a standalone server lists, a toolkit tool leaking into it, and a
// platform tool going missing from it (#1644).
func TestDiffToolNames(t *testing.T) {
	standalone := []string{"platform_info", "list_connections", "platform_mock_tool"}

	tests := []struct {
		name           string
		got            []string
		want           []string
		wantMissing    []string
		wantUnexpected []string
	}{
		{
			name: "exact set passes",
			got:  []string{"platform_info", "list_connections", "platform_mock_tool"},
			want: standalone,
		},
		{
			name: "order does not matter",
			got:  []string{"platform_mock_tool", "platform_info", "list_connections"},
			want: standalone,
		},
		{
			name:           "leaked toolkit tool is named",
			got:            []string{"platform_info", "list_connections", "platform_mock_tool", "trino_execute"},
			want:           standalone,
			wantUnexpected: []string{"trino_execute"},
		},
		{
			name:        "missing platform tool is named",
			got:         []string{"platform_info", "list_connections"},
			want:        standalone,
			wantMissing: []string{"platform_mock_tool"},
		},
		{
			name:           "leaks are reported sorted",
			got:            []string{"platform_info", "s3_list", "list_connections", "platform_mock_tool", "trino_query"},
			want:           standalone,
			wantUnexpected: []string{"s3_list", "trino_query"},
		},
		{
			name:        "empty listing is all missing",
			got:         nil,
			want:        standalone,
			wantMissing: []string{"list_connections", "platform_info", "platform_mock_tool"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			missing, unexpected := DiffToolNames(tt.got, tt.want)
			if !reflect.DeepEqual(missing, tt.wantMissing) {
				t.Errorf("missing = %v, want %v", missing, tt.wantMissing)
			}
			if !reflect.DeepEqual(unexpected, tt.wantUnexpected) {
				t.Errorf("unexpected = %v, want %v", unexpected, tt.wantUnexpected)
			}
		})
	}
}
