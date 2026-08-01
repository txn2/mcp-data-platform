package notifyrender

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// footerBranding is the configured help/about footer most tests render with.
func footerBranding() Branding {
	return Branding{
		Name:            "ACME Data Platform",
		BaseURL:         "https://data.example.com",
		ImplementorName: "ACME Corp",
		ImplementorURL:  "https://acme.example.com",
		AboutText:       "The ACME data portal delivers curated datasets and reports.",
		SupportContact:  "help@example.com",
	}
}

func footerRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := NewRenderer(footerBranding())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestRender_FooterOnAllMailTypes proves the about/support block reaches the
// HTML and text parts of every outgoing mail type (#1023): notifications,
// one-time guest links, and the admin test send.
func TestRender_FooterOnAllMailTypes(t *testing.T) {
	r := footerRenderer(t)
	about := footerBranding().AboutText

	renderAll := map[string]func() (*Email, error){
		"notification": func() (*Email, error) {
			return r.Render([]notification.Notification{{
				Recipient: "a@b.io",
				Payload:   notification.Payload{Kind: notification.KindAsset, ItemTitle: "T", Actor: "x@y.z"},
			}})
		},
		"guest link": func() (*Email, error) {
			return r.RenderGuestLink("a@b.io", "https://x.io/portal/view/t/guest?otk=o")
		},
		"admin test": func() (*Email, error) { return r.RenderTest("a@b.io") },
	}
	for name, render := range renderAll {
		t.Run(name, func(t *testing.T) {
			email, err := render()
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, want := range []string{about, "help@example.com"} {
				if !strings.Contains(email.HTML, want) {
					t.Errorf("HTML missing %q", want)
				}
				if !strings.Contains(email.Text, want) {
					t.Errorf("Text missing %q", want)
				}
			}
			if !strings.Contains(email.HTML, `href="mailto:help@example.com"`) {
				t.Error("HTML must link an email support contact as mailto:")
			}
		})
	}
}

// TestRender_UnsetFooterIsByteIdentical pins the #1023 acceptance criterion:
// with no footer configured, rendered output carries no footer artifact and
// no whitespace residue at the template seams the footer block was spliced
// into, so the bytes match pre-feature output exactly.
func TestRender_UnsetFooterIsByteIdentical(t *testing.T) {
	r := testRenderer(t)
	withZero, err := r.RenderTest("a@b.io")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, artifact := range []string{"Need help", "border-top:1px solid #e9ebee"} {
		if strings.Contains(withZero.HTML, artifact) {
			t.Errorf("unset footer must leave no artifact %q in HTML", artifact)
		}
	}
	if strings.Contains(withZero.Text, "Need help") {
		t.Error("unset footer must leave no artifact in text part")
	}
	// Whitespace seams: the HTML footer cell must keep exactly its
	// pre-feature byte layout, two whitespace-only lines (the unrendered
	// unsubscribe and legal-link conditionals) between the preferences line
	// and the closing tag, with nothing spliced in after them. The text part
	// must still end at the implementor line with the single trailing
	// newline it always had.
	if !strings.Contains(withZero.HTML, "Manage preferences</a></p>\n            \n            \n          </td>") {
		t.Error("HTML footer cell bytes changed with the footer unset")
	}
	if !strings.HasSuffix(withZero.Text, "Provided by ACME Corp (https://acme.example.com)\n") {
		t.Errorf("text part must end at the implementor line with one newline, got tail %q",
			withZero.Text[max(0, len(withZero.Text)-60):])
	}
}

// TestRender_FooterSpliceSeams pins the configured-footer whitespace: the
// block splices in on its own line after the existing footer content in both
// parts, with no doubled blank lines.
func TestRender_FooterSpliceSeams(t *testing.T) {
	r := footerRenderer(t)
	email, err := r.RenderTest("a@b.io")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(email.HTML, "Manage preferences</a></p>\n            \n            \n            <p style=\"margin:14px 0 0 0;") {
		t.Error("HTML footer block must splice in on its own indented line before the cell closes")
	}
	wantTextTail := "Provided by ACME Corp (https://acme.example.com)\n\n" +
		footerBranding().AboutText + "\nNeed help? Contact: help@example.com\n"
	if !strings.HasSuffix(email.Text, wantTextTail) {
		t.Errorf("text footer block tail wrong, got %q", email.Text[max(0, len(email.Text)-160):])
	}
}

// TestRender_FooterURLContact covers the URL spelling of the support contact.
func TestRender_FooterURLContact(t *testing.T) {
	r, err := NewRenderer(Branding{
		Name:           "ACME Data Platform",
		SupportContact: "https://help.example.com/support",
	})
	if err != nil {
		t.Fatal(err)
	}
	email, err := r.RenderTest("a@b.io")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(email.HTML, `href="https://help.example.com/support"`) {
		t.Error("HTML must link a URL support contact directly")
	}
	if !strings.Contains(email.Text, "Need help? Contact: https://help.example.com/support") {
		t.Error("text part missing the support contact line")
	}
	if strings.Contains(email.HTML, "mailto:") {
		t.Error("a URL contact must not render as mailto:")
	}
}

// TestRender_FooterAboutOnly covers the about-text-only spelling: no support
// line, no dangling "Need help" copy.
func TestRender_FooterAboutOnly(t *testing.T) {
	r, err := NewRenderer(Branding{Name: "ACME Data Platform", AboutText: "About this portal."})
	if err != nil {
		t.Fatal(err)
	}
	email, err := r.RenderTest("a@b.io")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(email.HTML, "About this portal.") {
		t.Error("HTML missing about text")
	}
	if strings.Contains(email.HTML, "Need help") || strings.Contains(email.Text, "Need help") {
		t.Error("no support line without a support contact")
	}
}

func TestSupportHref(t *testing.T) {
	tests := []struct {
		contact, want string
	}{
		{"help@example.com", "mailto:help@example.com"},
		{"https://help.example.com", "https://help.example.com"},
		{"http://help.example.com", "http://help.example.com"},
		{"help desk room 4", ""},
	}
	for _, tc := range tests {
		if got := supportHref(tc.contact); got != tc.want {
			t.Errorf("supportHref(%q) = %q, want %q", tc.contact, got, tc.want)
		}
	}
}

// TestRender_ReplyToStamped proves the branding Reply-To reaches every
// rendered Email so the sender can emit the header, and stays empty when
// unconfigured.
func TestRender_ReplyToStamped(t *testing.T) {
	r, err := NewRenderer(Branding{Name: "ACME Data Platform", ReplyTo: "support@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	email, err := r.RenderTest("a@b.io")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if email.ReplyTo != "support@example.com" {
		t.Errorf("ReplyTo = %q; want the branding address", email.ReplyTo)
	}

	plain, err := testRenderer(t).RenderTest("a@b.io")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if plain.ReplyTo != "" {
		t.Errorf("unconfigured branding must leave ReplyTo empty, got %q", plain.ReplyTo)
	}
}
