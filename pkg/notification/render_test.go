package notification

import (
	"strings"
	"testing"
	"testing/fstest"
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
	email, err := r.Render([]Notification{{
		Recipient: "a@b.io",
		Category:  CategoryShare,
		Payload: Payload{
			Kind:      KindAsset,
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
		{KindAsset, `x@y.z shared the asset "T" with you`},
		{KindCollection, `x@y.z shared the collection "T" with you`},
		{KindPrompt, `x@y.z shared the prompt "T" with you`},
		{KindFeedback, `x@y.z left feedback on "T"`},
		{KindComment, `x@y.z commented on "T"`},
	}
	r := testRenderer(t)
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			email, err := r.Render([]Notification{{
				Recipient: "a@b.io",
				Payload:   Payload{Kind: tc.kind, ItemTitle: "T", Actor: "x@y.z"},
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
	email, err := r.Render([]Notification{
		{Recipient: "a@b.io", Payload: Payload{Kind: KindAsset, ItemTitle: "One", Actor: "x@y.z"}},
		{Recipient: "a@b.io", Payload: Payload{Kind: KindComment, ItemTitle: "Two", Actor: "q@y.z"}},
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
	email, err := r.Render([]Notification{{
		Recipient: "a@b.io",
		Payload: Payload{
			Kind: KindComment, ItemTitle: "T",
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
	if _, err := r.Render([]Notification{{Recipient: "a@b.io"}}); err == nil {
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
