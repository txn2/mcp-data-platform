package knowledgepage

import (
	"strings"
	"testing"
)

func TestSplitSuggestion(t *testing.T) {
	bigBody := strings.Repeat("x", 100)
	manyHeadings := strings.Repeat("# Section\n\ntext\n\n", 5) // 5 headings

	tests := []struct {
		name             string
		body             string
		byteThreshold    int
		sectionThreshold int
		wantOK           bool
		wantContains     string
	}{
		{
			name:          "under both thresholds",
			body:          "# One\n\nsmall",
			byteThreshold: 1000, sectionThreshold: 10,
			wantOK: false,
		},
		{
			name:          "over byte threshold only",
			body:          bigBody,
			byteThreshold: 50, sectionThreshold: 10,
			wantOK: true, wantContains: "100 bytes",
		},
		{
			name:          "over section threshold only",
			body:          manyHeadings,
			byteThreshold: 100000, sectionThreshold: 5,
			wantOK: true, wantContains: "5 sections",
		},
		{
			name:          "over both",
			body:          bigBody + "\n" + manyHeadings,
			byteThreshold: 50, sectionThreshold: 5,
			wantOK: true, wantContains: "sections",
		},
		{
			name:          "byte threshold boundary (equal) fires",
			body:          strings.Repeat("y", 50),
			byteThreshold: 50, sectionThreshold: 0,
			wantOK: true,
		},
		{
			name:          "disabled thresholds never fire",
			body:          bigBody + manyHeadings,
			byteThreshold: 0, sectionThreshold: 0,
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := SplitSuggestion(tt.body, tt.byteThreshold, tt.sectionThreshold)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (msg=%q)", ok, tt.wantOK, msg)
			}
			if tt.wantOK && tt.wantContains != "" && !strings.Contains(msg, tt.wantContains) {
				t.Errorf("message %q does not contain %q", msg, tt.wantContains)
			}
			if !ok && msg != "" {
				t.Errorf("expected empty message when not ok, got %q", msg)
			}
		})
	}
}

func TestCountMarkdownHeadings(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"atx headings of varying depth", "# A\n## B\n### C\ntext", 3},
		{"hash without space is not a heading", "#tag not heading\n# Real", 1},
		{"seven hashes is not a heading", "####### too deep\n# ok", 1},
		{"headings inside fenced code ignored", "# Real\n```\n# not a heading\n## also not\n```\n## Real2", 2},
		{"tilde fence ignored", "~~~\n# nope\n~~~\n# yes", 1},
		{"indented heading still counts", "   # Indented", 1},
		{"empty body", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countMarkdownHeadings(tt.body); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}
