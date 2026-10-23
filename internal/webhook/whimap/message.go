package whimap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/imapmail"
	"github.com/txn2/mcp-data-platform/internal/mailtrust"
	"github.com/txn2/mcp-data-platform/internal/untrusted"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// EventType is the event_type of every message an imap source files.
const EventType = "mail"

// Payload is one message as its event's payload: what the platform read of it
// and, under External, everything its sender wrote.
type Payload struct {
	Folder      string `json:"folder"`
	UID         uint32 `json:"uid"`
	UIDValidity uint32 `json:"uid_validity"`
	MessageID   string `json:"message_id,omitempty"`
	InReplyTo   string `json:"in_reply_to,omitempty"`
	// References are the thread's earlier Message-IDs.
	References []string `json:"references,omitempty"`
	Date       string   `json:"date,omitempty"`
	Arrived    string   `json:"arrived"`
	// FromAddress is the sender's address as the From header names it, and
	// SenderAuth what the receiving server recorded of it.
	FromAddress string `json:"from_address"`
	SenderAuth  string `json:"sender_auth"`
	Size        int64  `json:"size"`
	BodyFormat  string `json:"body_format"`
	// BodySkipped says why the body was not read, empty when it was.
	BodySkipped string       `json:"body_skipped,omitempty"`
	Attachments []Attachment `json:"attachments"`
	External    External     `json:"external"`
	Notice      string       `json:"notice"`
}

// External is what the message's sender wrote, cleaned of invisible and
// reordering characters, hidden HTML removed from the body.
type External struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Cc      []string `json:"cc,omitempty"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
}

// Attachment is one file a message carried: the resource it was saved as, or
// why it was not.
type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	URI         string `json:"uri,omitempty"`
	ResourceID  string `json:"resource_id,omitempty"`
	Skipped     string `json:"skipped,omitempty"`
}

// File is one attachment to save: the library folder it goes in, its name,
// type and bytes.
type File struct {
	Folder      string
	Name        string
	ContentType string
	Data        []byte
}

// Files saves an attachment as a managed resource in the source's library.
type Files interface {
	PutFile(ctx context.Context, src whsource.Source, f File) (uri, id string, err error)
}

// message is one message read from the server, before it is an event.
type message struct {
	src      whsource.Source
	folder   string
	validity uint32
	env      imapmail.Envelope
	headers  imapmail.Headers
	judgment mailtrust.Judgment
	// raw is the whole message, nil when it was over max_message_bytes.
	raw []byte
}

// event turns a message into its event, saving its attachments first.
func (m message) event(ctx context.Context, files Files, replica string) (whevent.Event, error) {
	arrived := m.env.Arrived.UTC()
	if arrived.IsZero() {
		arrived = m.headers.Date.UTC()
	}
	p := Payload{
		Folder: m.folder, UID: m.env.UID, UIDValidity: m.validity,
		MessageID: untrusted.Line(m.headers.MessageID), InReplyTo: untrusted.Line(m.headers.InReplyTo),
		Arrived: arrived.Format(time.RFC3339), FromAddress: m.judgment.From, SenderAuth: m.judgment.Auth,
		Size: m.env.Size, BodyFormat: "none", Attachments: []Attachment{}, Notice: untrusted.Notice,
		External: External{
			From: untrusted.Line(m.headers.From), To: lines(m.headers.To), Cc: lines(m.headers.Cc),
			Subject: untrusted.Line(m.headers.Subject),
		},
	}
	for _, r := range m.headers.References {
		p.References = append(p.References, untrusted.Line(r))
	}
	if !m.headers.Date.IsZero() {
		p.Date = m.headers.Date.UTC().Format(time.RFC3339)
	}
	if err := m.readBody(ctx, files, &p, arrived); err != nil {
		return whevent.Event{}, err
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return whevent.Event{}, fmt.Errorf("encoding the message's payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	return whevent.Event{
		ReceivedAt: arrived, EventID: m.id(), EventType: EventType, Key: m.folder,
		ContentHash: hex.EncodeToString(sum[:]), Replica: replica, Payload: payload,
	}, nil
}

// id is the event's id: the Message-ID, so the same message read twice (a
// renumbered folder, a retried poll) is one event after compaction. A message
// with none is named by where it is.
func (m message) id() string {
	if id := untrusted.Line(m.headers.MessageID); id != "" {
		return id
	}
	return fmt.Sprintf("imap:%s:%d:%d", m.folder, m.validity, m.env.UID)
}

// readBody fills the body and saves the attachments, or says why it did not.
func (m message) readBody(ctx context.Context, files Files, p *Payload, arrived time.Time) error {
	if m.raw == nil {
		p.BodySkipped = fmt.Sprintf("the message is %d bytes, over the source's max_message_bytes", m.env.Size)
		for _, a := range m.env.Attachments {
			p.Attachments = append(p.Attachments, Attachment{Name: a.Name, ContentType: a.ContentType, Size: a.Size, Skipped: p.BodySkipped})
		}
		return nil
	}
	body, err := imapmail.ReadBody(m.raw)
	if err != nil {
		p.BodySkipped = "the message could not be read: " + err.Error()
		return nil //nolint:nilerr // an unreadable message is filed with the reason, not retried forever
	}
	p.BodyFormat, p.External.Body = body.Format, body.Text
	for _, a := range body.Attachments {
		att, err := m.save(ctx, files, a, arrived)
		if err != nil {
			return err
		}
		p.Attachments = append(p.Attachments, att)
	}
	return nil
}

// save writes one attachment as a resource under the source's attachments
// folder, in a folder of the message's own.
func (m message) save(ctx context.Context, files Files, a imapmail.Attachment, arrived time.Time) (Attachment, error) {
	out := Attachment{Name: a.Name, ContentType: a.ContentType, Size: a.Size}
	if a.Size > m.src.Config.IMAP.MaxAttachmentBytes {
		out.Skipped = "over the source's max_attachment_bytes"
		return out, nil
	}
	_, data, err := imapmail.ReadAttachment(m.raw, a.Index)
	if err != nil {
		out.Skipped = "the attachment could not be read: " + err.Error()
		return out, nil //nolint:nilerr // an unreadable attachment is listed with the reason, the message still filed
	}
	dt, _, _ := whlayout.PartitionValues(arrived)
	folder := AttachmentsFolder(m.src.Name) + "/" + dt + "/" + m.folderKey()
	out.URI, out.ResourceID, err = files.PutFile(ctx, m.src, File{Folder: folder, Name: a.Name, ContentType: a.ContentType, Data: data})
	if err != nil {
		return out, fmt.Errorf("saving attachment %q: %w", a.Name, err)
	}
	return out, nil
}

// AttachmentsFolder is the library folder every attachment a source saved
// sits under, removed with the source.
func AttachmentsFolder(source string) string {
	return "webhooks/" + source + "/attachments"
}

// folderKey names the folder a message's attachments are saved in: its UID
// validity and UID, which are the server's and contain nothing a sender wrote.
func (m message) folderKey() string {
	return fmt.Sprintf("%s-%d-%d", safeSegment(m.folder), m.validity, m.env.UID)
}

func safeSegment(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			_, _ = b.WriteRune(r)
		default:
			_ = b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func lines(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, untrusted.Line(s))
	}
	return out
}
