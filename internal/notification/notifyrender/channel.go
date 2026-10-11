package notifyrender

import (
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// channelLinkText is the button label on a channel document's email. The
// document's own link points at whatever produced it -- an asset, a script
// run -- so the label says "open", not what is being opened, which only the
// sender knows.
const channelLinkText = "Open"

// channelSubject is the subject line of a document delivered to an email
// channel: the document's own title, unchanged.
//
// It is not prefixed with who sent it, unlike a share or a comment. A channel
// document is a report or an alert written to be read on its own — its title
// is a sentence the sender composed for exactly this line — and wrapping it in
// "so-and-so sent you" would push the words that matter past where a mail
// client stops showing them.
func channelSubject(p notification.Payload) string {
	if p.Document != nil && p.Document.Title != "" {
		return p.Document.Title
	}
	return p.ItemTitle
}

// channelBody is the document's markdown, opened by the line saying how many
// repeats of the alert were held back when there were any.
//
// It is Body rather than Message because Message renders as a quotation, and
// a report is not something a colleague said. The text part of the email
// carries this markdown as written; the HTML part carries it rendered.
func channelBody(p notification.Payload) string {
	if p.Document == nil {
		return ""
	}
	note := p.Document.SuppressionNote()
	switch {
	case note == "":
		return p.Document.Body
	case p.Document.Body == "":
		return note
	default:
		return note + "\n\n" + p.Document.Body
	}
}

// AttachmentLink is an attachment a message names by link instead of carrying:
// one over the size a file may be, or one that could not be read.
type AttachmentLink struct {
	Name string
	URL  string
}

// RenderDocument renders one channel document to one email recipient with
// its attachments: files is what travels as MIME parts, links what the message
// names by link instead.
func (r *Renderer) RenderDocument(n notification.Notification, files []notification.File, links []AttachmentLink) (*Email, error) {
	data := r.buildData([]notification.Notification{n})
	if r.unsubURL != nil {
		data.UnsubURL = r.unsubURL(n.Recipient)
	}
	data.Items[0].Links = links
	email, err := r.execute(n.Recipient, data)
	if err != nil {
		return nil, err
	}
	email.Attachments = files
	return email, nil
}

// AlertDocument renders a platform alert's payload as the document a channel
// carries: the line its email's subject carries as the title, the prose its
// email's body carries as the body, and its link. A channel reader and an
// email reader are told the same thing in the same words.
func AlertDocument(p notification.Payload) notification.Document {
	n := notification.Notification{Payload: p}
	item := buildItem(n)
	body := item.Body
	if body == "" {
		body = item.Message
	}
	return notification.Document{Title: subjectFor(n), Body: body, Link: p.Link}
}
