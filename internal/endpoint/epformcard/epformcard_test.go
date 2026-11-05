package epformcard

import (
	"net/url"
	"strings"
	"testing"
)

// TestEmbeddedPage matches a form page on the rendered page's own origin
// whatever its query (a tile names it with none), and elsewhere only one
// carrying the platform's embed marker (a printed document's public address).
func TestEmbeddedPage(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		own  bool
		want string
		ok   bool
	}{
		{"http://t-0.render.invalid/forms/signup", true, "signup", true},
		{"http://t-0.render.invalid/forms/signup?form_embed=abc", true, "signup", true},
		{"https://platform.example.com/forms/sign_up-2?form_embed=abc&form_theme=dark", false, "sign_up-2", true},
		{"https://platform.example.com/forms/signup", false, "", false},
		{"https://platform.example.com/forms/signup/extra?form_embed=abc", false, "", false},
		{"http://t-0.render.invalid/forms/signup/extra", true, "", false},
		{"https://platform.example.com/forms/?form_embed=abc", false, "", false},
		{"https://platform.example.com/forms/Sign?form_embed=abc", false, "", false},
		{"http://t-0.render.invalid/endpoints/signup", true, "", false},
	} {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.raw, err)
		}
		got, ok := EmbeddedPage(u, tc.own)
		if got != tc.want || ok != tc.ok {
			t.Errorf("EmbeddedPage(%s, own=%v) = %q, %v; want %q, %v", tc.raw, tc.own, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := EmbeddedPage(nil, true); ok {
		t.Error("a nil URL matched")
	}
}

// TestRenderedCard draws the card naming the form, light and dark, in place
// of an embedded page, and declines anything else.
func TestRenderedCard(t *testing.T) {
	u, _ := url.Parse("https://platform.example.com/forms/signup?form_embed=abc")
	f, ok := RenderedCard(u, false)
	if !ok || f.ContentType != "text/html; charset=utf-8" {
		t.Fatalf("RenderedCard = %+v, %v", f, ok)
	}
	body := string(f.Body)
	for _, want := range []string{">Form<", ">signup<", "prefers-color-scheme: dark"} {
		if !strings.Contains(body, want) {
			t.Errorf("the card lacks %q", want)
		}
	}
	if strings.Contains(strings.ToLower(body), "not found") {
		t.Error("the card reads not found")
	}
	other, _ := url.Parse("https://platform.example.com/portal/refs/x")
	if _, ok := RenderedCard(other, true); ok {
		t.Error("a reference was substituted")
	}
}

// TestCard_EscapesTheName: the name is HTML-escaped wherever it is written.
func TestCard_EscapesTheName(t *testing.T) {
	body := string(Card(`a"<b>`))
	if strings.Contains(body, `<b>`) || strings.Contains(body, `"a"`) {
		t.Errorf("the name reached the card unescaped: %s", body)
	}
}
