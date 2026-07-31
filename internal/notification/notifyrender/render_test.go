package notifyrender

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func testRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := NewRenderer(Branding{
		Name:            "ACME Data Platform",
		BaseURL:         "https://data.example.com",
		ImplementorName: "ACME Corp",
		ImplementorURL:  "https://acme.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewRenderer_DefaultBrandName(t *testing.T) {
	r, err := NewRenderer(Branding{})
	if err != nil {
		t.Fatal(err)
	}
	if r.branding.Name != "Data Platform" {
		t.Errorf("empty brand name must default: %q", r.branding.Name)
	}
}

func TestRender_ShareEmail(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{{
		Recipient: "a@b.io",
		Category:  notification.CategoryShare,
		Payload: notification.Payload{
			Kind:      notification.KindAsset,
			ItemTitle: "Quarterly Revenue",
			Actor:     "owner@example.com",
			Message:   "Take a look before Friday.",
			Link:      "https://data.example.com/portal/view/tok123",
		},
	}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if email.To != "a@b.io" {
		t.Errorf("To = %q", email.To)
	}
	wantSubject := `owner@example.com shared the asset "Quarterly Revenue" with you`
	if email.Subject != wantSubject {
		t.Errorf("Subject = %q, want %q", email.Subject, wantSubject)
	}
	for _, want := range []string{
		"ACME Data Platform", "Quarterly Revenue",
		"https://data.example.com/portal/view/tok123", "Take a look before Friday.",
		"ACME Corp", "https://data.example.com/portal/settings",
	} {
		if !strings.Contains(email.HTML, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	for _, want := range []string{
		"ACME Data Platform",
		"https://data.example.com/portal/view/tok123", "Take a look before Friday.",
	} {
		if !strings.Contains(email.Text, want) {
			t.Errorf("Text missing %q", want)
		}
	}
}

func TestRender_SubjectVariants(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{notification.KindAsset, `x@y.z shared the asset "T" with you`},
		{notification.KindCollection, `x@y.z shared the collection "T" with you`},
		{notification.KindPrompt, `x@y.z shared the prompt "T" with you`},
		{notification.KindFeedback, `x@y.z left feedback on "T"`},
		{notification.KindComment, `x@y.z commented on "T"`},
	}
	r := testRenderer(t)
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			email, err := r.Render([]notification.Notification{{
				Recipient: "a@b.io",
				Payload:   notification.Payload{Kind: tc.kind, ItemTitle: "T", Actor: "x@y.z"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if email.Subject != tc.want {
				t.Errorf("Subject = %q, want %q", email.Subject, tc.want)
			}
		})
	}
}

func TestRender_Digest(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{
		{Recipient: "a@b.io", Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "One", Actor: "x@y.z"}},
		{Recipient: "a@b.io", Payload: notification.Payload{Kind: notification.KindComment, ItemTitle: "Two", Actor: "q@y.z"}},
	})
	if err != nil {
		t.Fatalf("Render digest: %v", err)
	}
	if !strings.Contains(email.Subject, "2 updates") {
		t.Errorf("digest subject = %q", email.Subject)
	}
	for _, want := range []string{"Your daily digest", "One", "Two"} {
		if !strings.Contains(email.HTML, want) {
			t.Errorf("digest HTML missing %q", want)
		}
	}
	if !strings.Contains(email.Text, `q@y.z commented on "Two"`) {
		t.Errorf("digest text missing item detail:\n%s", email.Text)
	}
}

func TestRender_Empty(t *testing.T) {
	r := testRenderer(t)
	if _, err := r.Render(nil); err == nil {
		t.Fatal("expected error for empty batch")
	}
}

func TestRender_EscapesHTML(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{{
		Recipient: "a@b.io",
		Payload: notification.Payload{
			Kind: notification.KindComment, ItemTitle: "T",
			Actor: "x@y.z", Message: `<script>alert("x")</script>`,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(email.HTML, "<script>") {
		t.Error("HTML body must escape injected markup")
	}
}

func TestRenderTest(t *testing.T) {
	r := testRenderer(t)
	email, err := r.RenderTest("admin@example.com")
	if err != nil {
		t.Fatalf("RenderTest: %v", err)
	}
	if email.To != "admin@example.com" {
		t.Errorf("To = %q", email.To)
	}
	if !strings.Contains(email.Subject, "SMTP test") {
		t.Errorf("Subject = %q", email.Subject)
	}
	if !strings.Contains(email.HTML, "confirms the SMTP configuration") {
		t.Error("HTML missing test body")
	}
	if !strings.Contains(email.Text, "confirms the SMTP configuration") {
		t.Error("Text missing test body")
	}
}

// TestRender_InlineLogo covers the branded header in both configurations. The
// logo is additive: the wordmark must survive alongside it, because a client
// that blocks or fails the inline part still has to identify the sender.
func TestRender_InlineLogo(t *testing.T) {
	logo := []byte("\x89PNG\r\n\x1a\nfake-raster-bytes")
	r, err := NewRenderer(Branding{
		Name:    "ACME Data Platform",
		BaseURL: "https://data.example.com",
		LogoPNG: logo,
	})
	if err != nil {
		t.Fatal(err)
	}

	email, err := r.RenderTest("admin@example.com")
	if err != nil {
		t.Fatalf("RenderTest: %v", err)
	}
	if !strings.Contains(email.HTML, `src="cid:`+LogoContentID+`"`) {
		t.Errorf("HTML missing inline logo reference: %s", email.HTML)
	}
	if !strings.Contains(email.HTML, `alt="ACME Data Platform"`) {
		t.Error("logo must carry the brand name as alt text for image-blocking clients")
	}
	if !strings.Contains(email.HTML, ">ACME Data Platform</a>") {
		t.Error("wordmark must remain alongside the logo, not be replaced by it")
	}
	if !bytes.Equal(email.LogoPNG, logo) {
		t.Error("Email must carry the logo bytes for the sender to embed")
	}
}

// TestRender_NoLogoWithoutConfig pins the default: an operator who configures
// no raster logo gets the wordmark alone and no dangling cid: reference, which
// would otherwise render as a broken image.
func TestRender_NoLogoWithoutConfig(t *testing.T) {
	email, err := testRenderer(t).RenderTest("admin@example.com")
	if err != nil {
		t.Fatalf("RenderTest: %v", err)
	}
	if strings.Contains(email.HTML, "cid:") {
		t.Errorf("unconfigured logo must not emit a cid: reference: %s", email.HTML)
	}
	if len(email.LogoPNG) != 0 {
		t.Error("unconfigured logo must leave Email.LogoPNG empty")
	}
	if !strings.Contains(email.HTML, ">ACME Data Platform</a>") {
		t.Error("wordmark must render when no logo is configured")
	}
}

// TestRender_LegalFooterLinks covers the optional terms/privacy footer links
// in both templates, including the separator that renders only when both are
// set.
func TestRender_LegalFooterLinks(t *testing.T) {
	r, err := NewRenderer(Branding{
		Name:       "ACME Data Platform",
		TermsURL:   "https://legal.example.com/terms",
		PrivacyURL: "https://legal.example.com/privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	email, err := r.RenderTest("admin@example.com")
	if err != nil {
		t.Fatalf("RenderTest: %v", err)
	}
	for _, want := range []string{
		`<a href="https://legal.example.com/terms"`, "Terms of Service",
		`<a href="https://legal.example.com/privacy"`, "Privacy Policy",
		"&middot;",
	} {
		if !strings.Contains(email.HTML, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	for _, want := range []string{
		"Terms of service: https://legal.example.com/terms",
		"Privacy policy: https://legal.example.com/privacy",
	} {
		if !strings.Contains(email.Text, want) {
			t.Errorf("Text missing %q", want)
		}
	}
}

// TestRender_LegalFooterLinksPartial pins one-of-two rendering: no dangling
// separator, and the unset link stays absent.
func TestRender_LegalFooterLinksPartial(t *testing.T) {
	r, err := NewRenderer(Branding{
		Name:       "ACME Data Platform",
		PrivacyURL: "https://legal.example.com/privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	email, err := r.RenderTest("admin@example.com")
	if err != nil {
		t.Fatalf("RenderTest: %v", err)
	}
	if !strings.Contains(email.HTML, "Privacy Policy") {
		t.Error("HTML missing the configured privacy link")
	}
	if strings.Contains(email.HTML, "Terms of Service") {
		t.Error("HTML renders an unconfigured terms link")
	}
	if strings.Contains(email.HTML, "&middot;") {
		t.Error("separator must not render with only one legal link")
	}
	if strings.Contains(email.Text, "Terms of service") {
		t.Error("Text renders an unconfigured terms link")
	}
}

// TestRender_NoLegalFooterWithoutConfig pins the default: no legal footer
// line at all when neither URL is configured.
func TestRender_NoLegalFooterWithoutConfig(t *testing.T) {
	email, err := testRenderer(t).RenderTest("admin@example.com")
	if err != nil {
		t.Fatalf("RenderTest: %v", err)
	}
	for _, absent := range []string{"Terms of Service", "Privacy Policy"} {
		if strings.Contains(email.HTML, absent) {
			t.Errorf("unconfigured legal link %q must not render", absent)
		}
	}
}

func TestNewRendererFromFS_ParseErrors(t *testing.T) {
	goodText := &fstest.MapFile{Data: []byte("ok")}
	tests := []struct {
		name string
		fsys fstest.MapFS
	}{
		{name: "missing html", fsys: fstest.MapFS{"templates/email.txt.tmpl": goodText}},
		{name: "bad html", fsys: fstest.MapFS{
			"templates/email.html.tmpl": {Data: []byte("{{.Broken")},
			"templates/email.txt.tmpl":  goodText,
		}},
		{name: "bad text", fsys: fstest.MapFS{
			"templates/email.html.tmpl": {Data: []byte("ok")},
			"templates/email.txt.tmpl":  {Data: []byte("{{.Broken")},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newRendererFromFS(tc.fsys, Branding{}); err == nil {
				t.Error("expected parse error")
			}
		})
	}
}

func TestExecute_TemplateErrors(t *testing.T) {
	// A template referencing a nonexistent field fails at execution time.
	badHTML := fstest.MapFS{
		"templates/email.html.tmpl": {Data: []byte("{{.NoSuchField}}")},
		"templates/email.txt.tmpl":  {Data: []byte("ok")},
	}
	r, err := newRendererFromFS(badHTML, Branding{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Render([]notification.Notification{{Recipient: "a@b.io"}}); err == nil {
		t.Error("expected html execution error")
	}

	badText := fstest.MapFS{
		"templates/email.html.tmpl": {Data: []byte("ok")},
		"templates/email.txt.tmpl":  {Data: []byte("{{.NoSuchField}}")},
	}
	r2, err := newRendererFromFS(badText, Branding{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r2.RenderTest("a@b.io"); err == nil {
		t.Error("expected text execution error")
	}
}

func TestJoinURL(t *testing.T) {
	tests := []struct {
		base, path, want string
	}{
		{"", "/portal/settings", ""},
		{"https://x.io", "/portal/settings", "https://x.io/portal/settings"},
		{"https://x.io/", "/portal/settings", "https://x.io/portal/settings"},
	}
	for _, tc := range tests {
		if got := joinURL(tc.base, tc.path); got != tc.want {
			t.Errorf("joinURL(%q, %q) = %q, want %q", tc.base, tc.path, got, tc.want)
		}
	}
}
