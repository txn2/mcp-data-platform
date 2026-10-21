package notification

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Document is what a channel carries: a title, a markdown body, a link back to
// whatever produced it, and the files that travel with it. Every kind renders
// these as far as it can and falls back to an excerpt plus the link past its
// own cap, so a long report always arrives as a summary and a pointer.
type Document struct {
	// Title is the message's subject line: the email subject, the
	// Mattermost first-line heading.
	Title string `json:"title"`
	// Body is markdown. What a reader sees of it depends on the kind.
	Body string `json:"body,omitempty"`
	// Link is the absolute URL a reader follows to the asset, the run, or
	// whatever else produced the message.
	Link string `json:"link,omitempty"`
	// Attachments name the portal assets and managed resources that travel
	// with the document as files. They are references, never bytes: the
	// queue row stays small and the worker reads each one at the version
	// recorded here when it delivers.
	Attachments []Attachment `json:"attachments,omitempty"`
	// Key identifies "the same alert again" on its channel. A second send
	// with the same key inside the channel's repeat window is counted and
	// not delivered. Empty means every send is delivered.
	Key string `json:"key,omitempty"`
	// Suppressed is how many sends with this document's key were absorbed
	// since SuppressedSince, written by the queue when this send went
	// through after them. Every kind states it on the message's first line.
	Suppressed      int        `json:"suppressed,omitempty"`
	SuppressedSince *time.Time `json:"suppressed_since,omitempty"`
	// Type is one of the Document* types: what produced the document.
	// Empty on a document queued before types existed.
	Type string `json:"type,omitempty"`
	// Source is who produced it.
	Source *DocumentSource `json:"source,omitempty"`
	// Data is the sender's structured payload, JSON, delivered verbatim by a
	// JSON webhook channel and ignored by every other kind.
	Data json.RawMessage `json:"data,omitempty"`
	// Target is the chat channel the post goes to on the channel's
	// connection, in place of the channel's own Target (#2241): a person
	// sharing an asset picks one of the channels the connection's bot is in.
	// Empty posts to the channel's Target. The slack and mattermost kinds read
	// it; the others carry no channel choice.
	Target string `json:"target,omitempty"`
}

// Attachment names one file a document carries: a portal asset or a managed
// resource, exactly one of the two.
type Attachment struct {
	// AssetID names a portal asset.
	AssetID string `json:"asset_id,omitempty"`
	// ResourceID names a managed resource.
	ResourceID string `json:"resource_id,omitempty"`
	// Version is the asset version the attachment was made from, recorded
	// at enqueue so a later edit of the asset does not change what a
	// queued message sends. Zero for a resource, which has no versions.
	Version int `json:"version,omitempty"`
	// Preview, with AssetID, sends the asset's light tile image instead of
	// its content: what a chat reader sees of a report before opening it.
	Preview bool `json:"preview,omitempty"`
	// Name is the file name the reader sees.
	Name string `json:"name,omitempty"`
	// Link is where a reader opens the asset or resource, used in place of
	// the file by a kind that carries none or when the file is over the
	// attachment cap.
	Link string `json:"link,omitempty"`
}

// File is one attachment's content, read by the worker at delivery.
type File struct {
	Name        string
	ContentType string
	Data        []byte
	// Preview marks the asset's tile image, which a kind that can shows in
	// the channel beside the message rather than in its thread (#2241).
	Preview bool
}

// SuppressionNote is the line a document delivered after suppressed sends
// opens with, or empty when nothing was suppressed. Every kind renders the
// same sentence so a reader sees one wording wherever the alert lands.
func (d Document) SuppressionNote() string {
	if d.Suppressed <= 0 {
		return ""
	}
	noun, verb := "repeats", "were"
	if d.Suppressed == 1 {
		noun, verb = "repeat", "was"
	}
	if d.SuppressedSince == nil {
		return fmt.Sprintf("%d %s of this alert %s not posted.", d.Suppressed, noun, verb)
	}
	return fmt.Sprintf("%d %s of this alert since %s %s not posted.",
		d.Suppressed, noun, d.SuppressedSince.UTC().Format("2006-01-02 15:04 UTC"), verb)
}

// DocumentSource is who produced a document: a managed script's run, or a
// person.
type DocumentSource struct {
	// Kind is DocumentSourceScript, DocumentSourceUser or DocumentSourceAdmin.
	Kind string `json:"kind"`
	// Script is the producing script's reference, mcp:script:<id>.
	Script string `json:"script,omitempty"`
	// RunID is the producing run.
	RunID string `json:"run_id,omitempty"`
}

// Delivery is one document as a channel transport sends it: the document, the
// identity of the queue row it came from, which is the same on every attempt
// so a receiver can recognize a retry, and the attachments' content.
type Delivery struct {
	// ID is the delivery's stable id: "ntf_<row id>" for a queued row.
	ID string
	// OccurredAt is when the document was queued.
	OccurredAt time.Time
	// Files are the document's attachments that were read for this
	// delivery, in the document's order.
	Files []File
	// Linked are the attachments that travel as a link instead: over
	// MaxAttachmentBytes, unreadable, or on a kind that carries no files.
	Linked []Attachment
	Document
}

// ValidateDocument bounds what may be enqueued for a channel. The body cap is
// applied here, at enqueue, so an oversized document is refused to its sender
// rather than discovered by the worker when nobody is listening.
func ValidateDocument(d Document) error {
	if strings.TrimSpace(d.Title) == "" {
		return errors.New("a notification document needs a title")
	}
	if len(d.Title) > MaxDocumentTitleBytes {
		return fmt.Errorf("a notification title is at most %d bytes, not %d", MaxDocumentTitleBytes, len(d.Title))
	}
	if len(d.Body) > MaxDocumentBytes {
		return fmt.Errorf("a notification body is at most %d bytes, not %d; link to the asset instead", MaxDocumentBytes, len(d.Body))
	}
	if len(d.Link) > MaxDocumentLinkBytes {
		return fmt.Errorf("a notification link is at most %d bytes, not %d", MaxDocumentLinkBytes, len(d.Link))
	}
	if len(d.Key) > MaxDocumentKeyBytes {
		return fmt.Errorf("a notification key is at most %d bytes, not %d", MaxDocumentKeyBytes, len(d.Key))
	}
	if len(d.Data) > MaxDocumentDataBytes {
		return fmt.Errorf("notification data is at most %d bytes of JSON, not %d; link to an asset for more", MaxDocumentDataBytes, len(d.Data))
	}
	if len(d.Data) > 0 && !json.Valid(d.Data) {
		return errors.New("notification data must be JSON")
	}
	return validateAttachments(d.Attachments)
}

// validateAttachments refuses an attachment list a channel cannot carry.
func validateAttachments(atts []Attachment) error {
	if len(atts) > MaxAttachments {
		return fmt.Errorf("a notification carries at most %d attachments, not %d", MaxAttachments, len(atts))
	}
	for i, a := range atts {
		switch {
		case a.AssetID != "" && a.ResourceID != "":
			return fmt.Errorf("attachment %d names both an asset and a resource; name one", i+1)
		case a.AssetID == "" && a.ResourceID == "":
			return fmt.Errorf("attachment %d names neither an asset nor a resource", i+1)
		case a.Preview && a.AssetID == "":
			return fmt.Errorf("attachment %d asks for a preview, which only an asset has", i+1)
		case a.Version < 0:
			return fmt.Errorf("attachment %d has a negative version", i+1)
		}
	}
	return nil
}

// Document bounds. The body cap is what a queue row may carry; each kind's
// own cap, which is smaller, decides how much of it a reader sees.
const (
	// MaxDocumentTitleBytes bounds a document title.
	MaxDocumentTitleBytes = 512
	// MaxDocumentBytes bounds the markdown body a queue row carries.
	MaxDocumentBytes = 256 * 1024
	// MaxDocumentLinkBytes bounds the link.
	MaxDocumentLinkBytes = 2048
	// MaxDocumentKeyBytes bounds a suppression key.
	MaxDocumentKeyBytes = 256
	// MaxDocumentDataBytes bounds the structured data a document carries
	// to a JSON webhook channel.
	MaxDocumentDataBytes = 64 * 1024
	// MaxAttachments bounds the files one document carries: five, the
	// Mattermost per-post limit and the lowest of the kinds.
	MaxAttachments = 5
	// MaxAttachmentBytes bounds one attachment's content. A file over it
	// reaches the reader as a link instead.
	MaxAttachmentBytes = 8 << 20
)
