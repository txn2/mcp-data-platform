package notifyrender

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// update rewrites the golden files from the current output.
var update = flag.Bool("update", false, "rewrite golden files")

// reportBody is the document every kind's golden test renders: a heading, a
// table, a list, a code block and a link, the shapes a weekly report holds.
const reportBody = "## Revenue by region\n\n" +
	"| Region | Revenue | Change |\n| --- | ---: | ---: |\n| West | 1,204,000 | +4% |\n| East | 988,500 | ~~-1%~~ +1% |\n\n" +
	"- West led for the third week\n- East recovered\n\n" +
	"```sql\nSELECT region, SUM(amount) FROM sales GROUP BY 1\n```\n\n" +
	"See the [full report](https://portal.example.com/portal/assets/a1)."

// golden compares got with testdata/name, or rewrites it under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) // #nosec G304 -- a fixed name under testdata
	if err != nil {
		t.Fatalf("reading golden %s (run with -update to create it): %v", name, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from its golden file:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestMarkdownHTML_RendersTheReportShapes(t *testing.T) {
	golden(t, "report.email.html", string(markdownHTML(reportBody)))
}

func TestMarkdownHTML_EmitsNoMarkupFromTheSource(t *testing.T) {
	// A sender's markdown must not put markup of its own into the message:
	// the renderer writes raw HTML as an omission, and drops a script URL.
	for _, src := range []string{
		"<script>alert(1)</script>",
		"before <script>alert(1)</script> after",
		"<img src=x onerror=alert(1)>",
		"[click](javascript:alert(1))",
	} {
		out := string(markdownHTML(src))
		low := strings.ToLower(out)
		if strings.Contains(low, "<script") || strings.Contains(low, "onerror") || strings.Contains(low, "javascript:") {
			t.Errorf("markdownHTML(%q) = %q; it carries markup from the source", src, out)
		}
	}
}

func TestMarkdownHTML_EmptyIsEmpty(t *testing.T) {
	if got := markdownHTML("  \n"); got != "" {
		t.Errorf("markdownHTML(blank) = %q, want empty", got)
	}
}

func TestChannelEmail_TheBodyRendersAsHTMLAndTheTextKeepsTheSource(t *testing.T) {
	r, err := NewRenderer(Branding{Name: "ACME Data"})
	if err != nil {
		t.Fatal(err)
	}
	email, err := r.Render([]notification.Notification{channelNotification("Weekly", reportBody, "")})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(email.HTML, "<table>") || !strings.Contains(email.HTML, "<h2>Revenue by region</h2>") {
		t.Errorf("the HTML part did not carry the rendered table and heading:\n%s", email.HTML)
	}
	if strings.Contains(email.HTML, "| Region |") {
		t.Error("the HTML part carried the markdown source")
	}
	if !strings.Contains(email.Text, "| Region | Revenue | Change |") {
		t.Error("the text part did not carry the markdown source")
	}
}

func TestChannelEmail_StatesTheSuppressedCount(t *testing.T) {
	n := channelNotification("Feed stale", "Last row 41 minutes ago.", "")
	since := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	n.Payload.Document.Suppressed, n.Payload.Document.SuppressedSince = 3, &since
	item := buildItem(n)
	want := "3 repeats of this alert since 2026-10-10 09:00 UTC were not posted."
	if !strings.HasPrefix(item.Body, want) {
		t.Errorf("Body = %q, want it to open with %q", item.Body, want)
	}
	if !strings.Contains(string(item.BodyHTML), want) {
		t.Error("the HTML body did not state the suppressed count")
	}
}

func TestChannelEmail_ADigestShowsEachDocumentsFirstParagraph(t *testing.T) {
	r, err := NewRenderer(Branding{Name: "ACME Data"})
	if err != nil {
		t.Fatal(err)
	}
	a := channelNotification("Feed stale", "First paragraph A.\n\nSecond paragraph A.", "https://p.example.com/a")
	b := channelNotification("Report ready", "First paragraph B.\n\nSecond paragraph B.", "https://p.example.com/b")
	a.Digest, b.Digest = true, true
	email, err := r.Render([]notification.Notification{a, b})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"First paragraph A.", "First paragraph B.", "https://p.example.com/a", "https://p.example.com/b"} {
		if !strings.Contains(email.HTML, want) {
			t.Errorf("the digest did not carry %q", want)
		}
	}
	if strings.Contains(email.HTML, "Second paragraph") || strings.Contains(email.Text, "Second paragraph") {
		t.Error("the digest carried more than each document's first paragraph")
	}
}

func TestRenderDocument_CarriesFilesAndNamesTheRestByLink(t *testing.T) {
	r, err := NewRenderer(Branding{Name: "ACME Data"})
	if err != nil {
		t.Fatal(err)
	}
	r.SetUnsubscribeURLFn(func(email string) string { return "https://p.example.com/unsub?e=" + email })
	files := []notification.File{{Name: "chart.png", ContentType: "image/png", Data: []byte("png")}}
	links := []AttachmentLink{{Name: "raw.csv", URL: "https://p.example.com/portal/resources/r1"}, {Name: "gone.pdf"}}
	email, err := r.RenderDocument(channelNotification("Weekly", "body", ""), files, links)
	if err != nil {
		t.Fatalf("RenderDocument: %v", err)
	}
	if len(email.Attachments) != 1 || email.Attachments[0].Name != "chart.png" {
		t.Errorf("Attachments = %+v, want the one file", email.Attachments)
	}
	for _, want := range []string{"Attachment: raw.csv https://p.example.com/portal/resources/r1", "Attachment: gone.pdf"} {
		if !strings.Contains(email.Text, want) {
			t.Errorf("the text part did not name %q:\n%s", want, email.Text)
		}
	}
	if !strings.Contains(email.HTML, `href="https://p.example.com/portal/resources/r1"`) {
		t.Error("the HTML part did not link the attachment sent by link")
	}
	if email.UnsubURL == "" {
		t.Error("the footer opt-out was dropped from a channel document's email")
	}
}

func TestFirstParagraph(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"one":                   "one",
		"  one\n\ntwo\n\nthree": "one",
		"one\nline two\n\nnext": "one\nline two",
	}
	for in, want := range cases {
		if got := firstParagraph(in); got != want {
			t.Errorf("firstParagraph(%q) = %q, want %q", in, got, want)
		}
	}
}
