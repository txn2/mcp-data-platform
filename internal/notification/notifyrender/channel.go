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

// channelBody is the document's markdown, rendered as the plain prose this
// stage delivers.
//
// It is Body rather than Message because Message renders as a quotation, and
// a report is not something a colleague said. The markdown arrives as its own
// source: rendering it to HTML is the next stage's work, and a body shown as
// written is legible in the meantime, where a body silently stripped of its
// markup would not be.
func channelBody(p notification.Payload) string {
	if p.Document == nil {
		return ""
	}
	return p.Document.Body
}
