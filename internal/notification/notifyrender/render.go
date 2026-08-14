// Package notifyrender turns queued notifications into branded multipart
// emails. It owns the embedded templates, the deployment branding they render
// with, and the Email value a sender delivers.
//
// Rendering is the only layer that decides what a message looks like: the
// worker hands it a claimed batch and passes the result to a sender unchanged,
// so every message the platform emits carries the same footer, logo and
// Reply-To treatment.
package notifyrender

import (
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"strings"
	texttemplate "text/template"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// Branding carries the deployment identity emails render with. Values come
// from the same PortalConfig the portal UI uses, so emails match the portal.
// The brand name always renders as a styled wordmark linked to BaseURL; the
// portal's SVG logo is never usable here because email clients strip inline
// SVG, so LogoPNG carries a separate raster asset when the operator supplies
// one.
type Branding struct {
	// Name is the portal brand/title, e.g. "ACME Data Platform".
	Name string
	// BaseURL is the portal's public base URL used for deep links.
	BaseURL string
	// ImplementorName and ImplementorURL render in the footer when set.
	ImplementorName string
	ImplementorURL  string
	// TermsURL and PrivacyURL render as small legal links in the footer when
	// set. When the portal runs on a different domain than the mail From
	// address, they give recipients and content filters body links that
	// associate with the sending identity.
	TermsURL   string
	PrivacyURL string
	// AboutText and SupportContact render as a help/about footer block on
	// all outgoing mail when set (#1023). AboutText is a sentence or two
	// describing the platform; SupportContact is an email address or http(s)
	// URL for help. Implementor-owned YAML config (portal.about_text,
	// portal.support_contact), like the rest of the branding: in fully
	// managed deployments these are not admin-editable.
	AboutText      string
	SupportContact string
	// ReplyTo, when set, is stamped on every rendered Email so the sender
	// applies it as the Reply-To header and recipient replies reach a
	// monitored mailbox (#1023). From portal.reply_to.
	ReplyTo string
	// LogoPNG is the raster logo from portal.logo_email, resolved once at
	// startup. When non-empty it is attached to every message as an inline
	// (cid:) part and rendered above the wordmark; recipients never fetch it
	// remotely, so it survives the image blocking most clients apply by
	// default. Empty renders the wordmark alone.
	LogoPNG []byte
}

// LogoContentID is the Content-ID of the inline logo part. The HTML template
// references it as cid:<this>, so the two must stay in sync.
const LogoContentID = "logo.png"

// supportHref returns the link target for the support contact: mailto: for an
// address, the URL itself for http(s), empty (render as plain text) otherwise.
func supportHref(contact string) string {
	if strings.HasPrefix(contact, "http://") || strings.HasPrefix(contact, "https://") {
		return contact
	}
	if strings.Contains(contact, "@") {
		return "mailto:" + contact
	}
	return ""
}

// Email is one rendered message ready for an SMTP sender.
type Email struct {
	To      string
	Subject string
	HTML    string
	Text    string
	// LogoPNG is the inline logo the sender must attach under LogoContentID
	// when non-empty. The HTML references it but cannot carry the bytes.
	LogoPNG []byte
	// UnsubURL is the recipient's no-login unsubscribe link when the message
	// carries the footer opt-out (notifications only; transactional sends
	// leave it empty). The sender emits the RFC 8058 List-Unsubscribe headers
	// from it, so header presence tracks the footer link exactly.
	UnsubURL string
	// ReplyTo is the Reply-To address the sender must apply when non-empty
	// (#1023). Stamped from Branding.ReplyTo on every rendered message.
	ReplyTo string
}

// Renderer renders queued notifications into branded multipart emails.
type Renderer struct {
	branding Branding
	html     *htmltemplate.Template
	text     *texttemplate.Template
	// unsubURL builds the no-login unsubscribe link for a recipient address
	// (#1001). nil omits the footer link.
	unsubURL func(email string) string
}

// SetUnsubscribeURLFn installs the builder for the footer's no-login
// unsubscribe link. The composition root supplies it when it can mint the
// HMAC tokens the endpoint verifies; without it emails keep only the
// signed-in preferences link.
func (r *Renderer) SetUnsubscribeURLFn(fn func(email string) string) {
	r.unsubURL = fn
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
func (r *Renderer) Render(ns []notification.Notification) (*Email, error) {
	if len(ns) == 0 {
		return nil, errors.New("rendering email: no notifications")
	}
	data := r.buildData(ns)
	if r.unsubURL != nil {
		data.UnsubURL = r.unsubURL(ns[0].Recipient)
	}
	return r.execute(ns[0].Recipient, data)
}

// execute runs both templates over data and assembles the Email. It is the
// one place the inline logo, the branding footer block, and the Reply-To are
// applied, so every message the renderer produces carries them uniformly.
func (r *Renderer) execute(to string, data emailData) (*Email, error) {
	if len(r.branding.LogoPNG) > 0 {
		data.LogoCID = LogoContentID
	}
	data.AboutText = r.branding.AboutText
	data.SupportContact = r.branding.SupportContact
	data.SupportHref = supportHref(r.branding.SupportContact)
	var htmlBuf, textBuf strings.Builder
	if err := r.html.Execute(&htmlBuf, data); err != nil {
		return nil, fmt.Errorf("rendering html email: %w", err)
	}
	if err := r.text.Execute(&textBuf, data); err != nil {
		return nil, fmt.Errorf("rendering text email: %w", err)
	}
	return &Email{
		To:       to,
		Subject:  data.Subject,
		HTML:     htmlBuf.String(),
		Text:     textBuf.String(),
		LogoPNG:  r.branding.LogoPNG,
		UnsubURL: data.UnsubURL,
		ReplyTo:  r.branding.ReplyTo,
	}, nil
}

// RenderGuestLink renders the one-time view link email a share recipient
// requests from the landing page (#1001). It is transactional, not a
// notification: the recipient asked for it, so it carries no unsubscribe
// footer and its delivery bypasses the queue and preference gating.
func (r *Renderer) RenderGuestLink(to, link string) (*Email, error) {
	data := emailData{
		Brand:   r.branding,
		Subject: fmt.Sprintf("%s: your one-time view link", r.branding.Name),
		Heading: "Your one-time view link",
		Items: []emailItem{{
			Message: "Use this link to open the item shared with you. " +
				"It works once and expires in 15 minutes. " +
				"You can request another from the share page whenever you need one.",
			Link:     link,
			LinkText: "Open the shared item",
		}},
	}
	return r.execute(to, data)
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
	Brand   Branding
	Subject string
	Heading string
	Digest  bool
	Items   []emailItem
	// LogoCID is the Content-ID the HTML template points its logo <img> at.
	// Empty when no raster logo is configured, which renders the wordmark
	// alone. The text template ignores it.
	LogoCID  string
	PrefsURL string
	// UnsubURL is the no-login unsubscribe link for this recipient. Empty
	// omits the footer line; see SetUnsubscribeURLFn.
	UnsubURL string
	// AboutText, SupportContact, and SupportHref render the branding's
	// help/about footer block (#1023); all empty omits the block entirely.
	// Stamped from Branding in execute.
	AboutText      string
	SupportContact string
	SupportHref    string
}

// emailItem is one event line in an email.
type emailItem struct {
	Title  string
	Detail string
	// Body is prose the platform wrote about the event, rendered plainly.
	// Message is text a person wrote, rendered as a quotation -- an alert
	// must not appear to be quoting someone.
	Body    string
	Message string
	Link    string
	// LinkText overrides the default "Open ..." button label.
	LinkText string
}

// buildData assembles the template context for a batch of notifications.
func (r *Renderer) buildData(ns []notification.Notification) emailData {
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
func buildItem(n notification.Notification) emailItem {
	item := emailItem{
		Title:   n.Payload.ItemTitle,
		Detail:  subjectFor(n),
		Message: n.Payload.Message,
		Link:    n.Payload.Link,
	}
	switch n.Payload.Kind {
	case notification.KindReviewQueue:
		item.Body = reviewQueueBody(n.Payload.Review)
		item.LinkText = reviewQueueLinkText
	case notification.KindScriptReview:
		item.Body = scriptReviewBody(n.Payload.Review)
		item.LinkText = scriptReviewLinkText
	case notification.KindScriptRun:
		// The failure detail moves from Message to Body: Message renders as a
		// quotation, and a backtrace is not something a colleague said.
		item.Body = scriptRunBody(n.Payload)
		item.Message = ""
	}
	return item
}

// Subject returns the one-line summary of a single notification: the subject
// its email carries, and the same line the admin monitoring tab and a user's
// own notification history show for the row.
//
// Those surfaces read it from here rather than composing their own so a
// reader comparing a queue row against the mail they received sees one
// sentence, not two spellings of it.
func Subject(n notification.Notification) string { return subjectFor(n) }

// subjectFor returns the single-event subject/heading line.
func subjectFor(n notification.Notification) string {
	switch n.Payload.Kind {
	case notification.KindAsset, notification.KindCollection, notification.KindPrompt:
		return fmt.Sprintf("%s shared the %s %q with you", n.Payload.Actor, n.Payload.Kind, n.Payload.ItemTitle)
	case notification.KindFeedback:
		return fmt.Sprintf("%s left feedback on %q", n.Payload.Actor, n.Payload.ItemTitle)
	case notification.KindMention:
		return fmt.Sprintf("%s mentioned you on %q", n.Payload.Actor, n.Payload.ItemTitle)
	case notification.KindReviewQueue:
		return reviewQueueSubject(n.Payload.Review)
	case notification.KindScriptReview:
		return scriptReviewSubject(n.Payload.Review)
	case notification.KindScriptRun:
		return scriptRunSubject(n.Payload)
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
