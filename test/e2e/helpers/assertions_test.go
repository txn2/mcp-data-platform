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
	standalone := []string{"platform_info", "list_connections", "platform_find_tools"}

	tests := []struct {
		name           string
		got            []string
		want           []string
		wantMissing    []string
		wantUnexpected []string
	}{
		{
			name: "exact set passes",
			got:  []string{"platform_info", "list_connections", "platform_find_tools"},
			want: standalone,
		},
		{
			name: "order does not matter",
			got:  []string{"platform_find_tools", "platform_info", "list_connections"},
			want: standalone,
		},
		{
			name:           "leaked toolkit tool is named",
			got:            []string{"platform_info", "list_connections", "platform_find_tools", "trino_execute"},
			want:           standalone,
			wantUnexpected: []string{"trino_execute"},
		},
		{
			name:        "missing platform tool is named",
			got:         []string{"platform_info", "list_connections"},
			want:        standalone,
			wantMissing: []string{"platform_find_tools"},
		},
		{
			name:           "leaks are reported sorted",
			got:            []string{"platform_info", "s3_list", "list_connections", "platform_find_tools", "datahub_browse"},
			want:           standalone,
			wantUnexpected: []string{"datahub_browse", "s3_list"},
		},
		{
			name:        "empty listing is all missing",
			got:         nil,
			want:        standalone,
			wantMissing: []string{"list_connections", "platform_find_tools", "platform_info"},
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
