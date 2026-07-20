package notification

import (
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"strings"
	texttemplate "text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// Branding carries the deployment identity emails render with. Values come
// from the same PortalConfig the portal UI uses, so emails match the portal.
// The logo is deliberately text-based: most email clients strip inline SVG,
// so the brand name renders as a styled wordmark linked to BaseURL.
type Branding struct {
	// Name is the portal brand/title, e.g. "ACME Data Platform".
	Name string
	// BaseURL is the portal's public base URL used for deep links.
	BaseURL string
	// ImplementorName and ImplementorURL render in the footer when set.
	ImplementorName string
	ImplementorURL  string
}

// Email is one rendered message ready for an SMTP sender.
type Email struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// Renderer renders queued notifications into branded multipart emails.
type Renderer struct {
	branding Branding
	html     *htmltemplate.Template
	text     *texttemplate.Template
}

// NewRenderer parses the embedded templates for the given branding.
func NewRenderer(b Branding) (*Renderer, error) {
	return newRendererFromFS(templateFS, b)
}

// newRendererFromFS parses the templates from fsys; split from NewRenderer
// so the parse and execution failure paths are testable against a broken
// template set.
func newRendererFromFS(fsys fs.FS, b Branding) (*Renderer, error) {
	if b.Name == "" {
		b.Name = "Data Platform"
	}
	html, err := htmltemplate.ParseFS(fsys, "templates/email.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parsing html email template: %w", err)
	}
	text, err := texttemplate.ParseFS(fsys, "templates/email.txt.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parsing text email template: %w", err)
	}
	return &Renderer{branding: b, html: html, text: text}, nil
}

// Render renders one email covering the given notifications. A single
// notification renders the per-event template; multiple render the digest
// layout. All notifications must target the same recipient.
func (r *Renderer) Render(ns []Notification) (*Email, error) {
	if len(ns) == 0 {
		return nil, errors.New("rendering email: no notifications")
	}
	return r.execute(ns[0].Recipient, r.buildData(ns))
}

// execute runs both templates over data and assembles the Email.
func (r *Renderer) execute(to string, data emailData) (*Email, error) {
	var htmlBuf, textBuf strings.Builder
	if err := r.html.Execute(&htmlBuf, data); err != nil {
		return nil, fmt.Errorf("rendering html email: %w", err)
	}
	if err := r.text.Execute(&textBuf, data); err != nil {
		return nil, fmt.Errorf("rendering text email: %w", err)
	}
	return &Email{To: to, Subject: data.Subject, HTML: htmlBuf.String(), Text: textBuf.String()}, nil
}

// RenderTest renders the admin "send test email" message used to verify a
// new SMTP configuration end to end.
func (r *Renderer) RenderTest(to string) (*Email, error) {
	data := emailData{
		Brand:    r.branding,
		Subject:  fmt.Sprintf("%s SMTP test", r.branding.Name),
		Heading:  "SMTP configuration test",
		PrefsURL: joinURL(r.branding.BaseURL, "/portal/settings"),
		Items: []emailItem{{
			Message: "This is a test email. Receiving it confirms the SMTP configuration works.",
		}},
	}
	return r.execute(to, data)
}

// emailData is the template context shared by the HTML and text templates.
type emailData struct {
	Brand    Branding
	Subject  string
	Heading  string
	Digest   bool
	Items    []emailItem
	PrefsURL string
}

// emailItem is one event line in an email.
type emailItem struct {
	Title   string
	Detail  string
	Message string
	Link    string
}

// buildData assembles the template context for a batch of notifications.
func (r *Renderer) buildData(ns []Notification) emailData {
	data := emailData{
		Brand:    r.branding,
		Digest:   len(ns) > 1,
		PrefsURL: joinURL(r.branding.BaseURL, "/portal/settings"),
	}
	for _, n := range ns {
		data.Items = append(data.Items, buildItem(n))
	}
	if data.Digest {
		data.Subject = fmt.Sprintf("%s: %d updates in your daily digest", r.branding.Name, len(ns))
		data.Heading = "Your daily digest"
	} else {
		data.Subject = subjectFor(ns[0])
		data.Heading = data.Items[0].Detail
	}
	return data
}

// buildItem converts one notification into an email line item.
func buildItem(n Notification) emailItem {
	return emailItem{
		Title:   n.Payload.ItemTitle,
		Detail:  subjectFor(n),
		Message: n.Payload.Message,
		Link:    n.Payload.Link,
	}
}

// subjectFor returns the single-event subject/heading line.
func subjectFor(n Notification) string {
	switch n.Payload.Kind {
	case KindAsset, KindCollection, KindPrompt:
		return fmt.Sprintf("%s shared the %s %q with you", n.Payload.Actor, n.Payload.Kind, n.Payload.ItemTitle)
	case KindFeedback:
		return fmt.Sprintf("%s left feedback on %q", n.Payload.Actor, n.Payload.ItemTitle)
	default:
		return fmt.Sprintf("%s commented on %q", n.Payload.Actor, n.Payload.ItemTitle)
	}
}

// joinURL joins a base URL and a path without doubling slashes. An empty
// base yields an empty string so templates can omit the link.
func joinURL(base, path string) string {
	if base == "" {
		return ""
	}
	return strings.TrimSuffix(base, "/") + path
}
