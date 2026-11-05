package pdfhttp

import (
	"net/url"
	"strings"
	"testing"
)

// TestPDFPage_DrawsAnEmbeddedFormAsACard: the printed page answers an
// embedded form, named at the deployment's public address as a served
// document names it, with a card naming the form, and never loads it
// (#2431).
func TestPDFPage_DrawsAnEmbeddedFormAsACard(t *testing.T) {
	page := New(Deps{Routes: routes(t), Printer: &fakePrinter{}}).page([]byte("<html><head></head><body></body></html>"))
	if page.Substitute == nil {
		t.Fatal("the printed page draws an embedded form by loading it")
	}
	form, _ := url.Parse("https://platform.example.com/forms/signup?form_embed=abc")
	if f, ok := page.Substitute(form, false); !ok || !strings.Contains(string(f.Body), ">signup<") {
		t.Errorf("an embedded form was answered %v %q, want its card", ok, f.Body)
	}
	ref, _ := url.Parse("https://platform.example.com/portal/refs/x")
	if _, ok := page.Substitute(ref, true); ok {
		t.Error("a reference was substituted")
	}
}
