package notification

import (
	"strings"
	"testing"
)

func TestSnippet(t *testing.T) {
	if got := Snippet("short"); got != "short" {
		t.Errorf("short message altered: %q", got)
	}
	long := strings.Repeat("x", 500)
	got := Snippet(long)
	if len([]rune(got)) != snippetLimit+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("truncation wrong: len=%d", len(got))
	}
	multibyte := strings.Repeat("e", 300) + strings.Repeat("é", 300)
	got = Snippet(multibyte)
	if !strings.HasSuffix(got, "...") || strings.ContainsRune(got, '�') {
		t.Error("multibyte truncation corrupted the string")
	}
}

func TestPortalLink(t *testing.T) {
	if got := PortalLink("https://x.io/", "/assets/a1"); got != "https://x.io/portal/assets/a1" {
		t.Errorf("PortalLink = %q", got)
	}
	if got := PortalLink("", "/assets/a1"); got != "" {
		t.Errorf("empty base must yield empty link, got %q", got)
	}
}

func TestRecipientsExcluding(t *testing.T) {
	tests := []struct {
		name       string
		actor      string
		candidates []string
		want       []string
	}{
		{
			name: "owner and author", actor: "x@b.io",
			candidates: []string{"o@b.io", "t@b.io"}, want: []string{"o@b.io", "t@b.io"},
		},
		{
			name: "actor excluded case-insensitively", actor: "o@B.io",
			candidates: []string{"O@b.io", "t@b.io"}, want: []string{"t@b.io"},
		},
		{
			name: "duplicates collapsed", actor: "x@b.io",
			candidates: []string{"same@b.io", "SAME@b.io"}, want: []string{"same@b.io"},
		},
		{
			name: "empties dropped", actor: "x@b.io",
			candidates: []string{"", ""}, want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RecipientsExcluding(tc.actor, tc.candidates...)
			if len(got) != len(tc.want) {
				t.Fatalf("recipients = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("recipients = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
