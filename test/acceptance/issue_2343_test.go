//go:build integration

package acceptance

import (
	"net/http"
	"regexp"
	"strconv"
	"testing"
)

// Acceptance for #2343: a deck that insets its slides with padding on the
// section prints them inset as on screen, and a background the section draws
// itself fills the whole slide rather than stopping at its content.
//
// The decks are saved through save_asset as an agent saves one, on the served
// runtime, and printed through the route the viewer's Export PDF button calls.
// One deck pads its sections and one does not; the same left-aligned line
// opens each, so where the line is printed in the one is where it is printed
// in the other, moved by the padding.
//
// Wire forms: save_asset's content is a string, its one form; the PDF route is
// a GET with no body.

// issue2343Deck is a one-slide deck whose section carries style, opening with
// the only text on the slide.
func issue2343Deck(section string) string {
	return `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Padding</title>
<link rel="stylesheet" href="/portal/vendor/reveal/reset.css">
<link rel="stylesheet" href="/portal/vendor/reveal/reveal.css">
<link rel="stylesheet" href="/portal/vendor/reveal/theme/black.css">
<style>
.reveal .slides > section { ` + section + ` }
.reveal .slides > section::before { content: ""; position: absolute; left: 0; top: 0; width: 100%; height: 100%; background: #123456; }
.reveal .mark { position: relative; text-align: left; margin: 0; }
</style>
</head><body>
<div class="reveal"><div class="slides">
<section><p class="mark">Inset</p></section>
</div></div>
<script src="/portal/vendor/reveal/reveal.js"></script>
<script>Reveal.initialize({ hash: false, width: 1280, height: 720, margin: 0, center: false });</script>
</body></html>`
}

// issue2343TextAt is where a page's first line of text is set, in the
// slide's own pixels: Chrome scales each page's content to CSS pixels and
// places every run of text with a text matrix.
var issue2343TextAt = regexp.MustCompile(`1 0 0 -1 (-?[\d.]+) (-?[\d.]+) Tm`)

// issue2343Fill is a rectangle filled in #123456, the section's own
// background, with its width and height in the slide's pixels.
var issue2343Fill = regexp.MustCompile(`0?\.0706\d* 0?\.2039\d* 0?\.3373\d* rg\s+(?:\S+ gs\s+)?(?:[^\n]*BDC\s+|EMC\s+)*-?[\d.]+ -?[\d.]+ ([\d.]+) ([\d.]+) re`)

// issue2343Print saves the deck, exports it, and returns where its line was
// printed and the size of the section's background.
func issue2343Print(t *testing.T, c *client, section string) (x, y, w, h float64) {
	t.Helper()
	id := issue1983Save(t, c, "text/html", issue2343Deck(section))
	status, _, pdf := issue1983Get(t, "/api/v1/portal/assets/"+id+"/pdf", c.apiKey)
	if status != http.StatusOK {
		t.Fatalf("GET pdf: HTTP %d: %s", status, pdf)
	}
	if pages := issue1983Pages(pdf); pages != 1 {
		t.Fatalf("the one-slide deck exported as %d pages", pages)
	}
	streams := issue1983Streams(pdf)
	at := issue2343TextAt.FindStringSubmatch(streams)
	fill := issue2343Fill.FindStringSubmatch(streams)
	if at == nil || fill == nil {
		t.Fatalf("the page sets no text (%v) or draws no #123456 background (%v):\n%s", at != nil, fill != nil, streams)
	}
	num := func(s string) float64 {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("%q in the content stream is not a number", s)
		}
		return f
	}
	return num(at[1]), num(at[2]), num(fill[1]), num(fill[2])
}

// TestIssue2343_SectionPaddingIsPrinted is the ticket's first expectation: the
// slide's content is inset on the page by the section's padding, 80px from the
// left and 64px from the top, as it is on screen.
func TestIssue2343_SectionPaddingIsPrinted(t *testing.T) {
	c := connect(t)
	flushX, flushY, _, _ := issue2343Print(t, c, "height: 100%; box-sizing: border-box;")
	x, y, _, _ := issue2343Print(t, c, "padding: 64px 80px 72px; height: 100%; box-sizing: border-box;")
	if dx, dy := x-flushX, y-flushY; dx < 79 || dx > 81 || dy < 63 || dy > 65 {
		t.Errorf("the padded slide's line is printed %.1fpx right and %.1fpx down of the unpadded one's, want 80 and 64: "+
			"the print view dropped the section's padding", dx, dy)
	}
}

// TestIssue2343_TheSectionsBackgroundFillsTheSlide is the ticket's second
// symptom of the same cause: a background the padded section draws itself
// covers the whole 1280x720 slide, not only the height of its content.
func TestIssue2343_TheSectionsBackgroundFillsTheSlide(t *testing.T) {
	c := connect(t)
	_, _, w, h := issue2343Print(t, c, "padding: 64px 80px 72px; height: 100%; box-sizing: border-box;")
	if w < 1279 || h < 719 {
		t.Errorf("the section's background is %.0fx%.0fpx, want the slide's 1280x720", w, h)
	}
}
