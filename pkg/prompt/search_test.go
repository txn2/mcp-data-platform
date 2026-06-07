package prompt

import "testing"

func TestIndexText(t *testing.T) {
	tests := []struct {
		name string
		in   Prompt
		want string
	}{
		{
			name: "all fields",
			in: Prompt{
				DisplayName: "Daily Sales",
				Name:        "daily-sales",
				Description: "Summarize sales",
				Content:     "Analyze {date}",
				Tags:        []string{"sales", "reporting"},
			},
			want: "Daily Sales\nSummarize sales\nAnalyze {date}\nsales reporting",
		},
		{
			name: "display name falls back to name",
			in:   Prompt{Name: "daily-sales", Content: "body"},
			want: "daily-sales\nbody",
		},
		{
			name: "empty fields are skipped",
			in:   Prompt{Name: "only-name"},
			want: "only-name",
		},
		{
			name: "no tags",
			in:   Prompt{DisplayName: "Title", Description: "Desc", Content: "Body"},
			want: "Title\nDesc\nBody",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IndexText(&tt.in); got != tt.want {
				t.Errorf("IndexText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearchQueryEffectiveLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"unset defaults", 0, DefaultSearchLimit},
		{"negative defaults", -5, DefaultSearchLimit},
		{"over max defaults", maxSearchLimit + 1, DefaultSearchLimit},
		{"in range passes through", 7, 7},
		{"at max passes through", maxSearchLimit, maxSearchLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := SearchQuery{Limit: tt.limit}
			if got := q.EffectiveLimit(); got != tt.want {
				t.Errorf("EffectiveLimit() = %d, want %d", got, tt.want)
			}
		})
	}
}
