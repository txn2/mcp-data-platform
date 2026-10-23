package imapmail

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // decodes the charsets mail is sent in
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"

	"github.com/txn2/mcp-data-platform/internal/untrusted"
)

// Headers is what the platform reads from a message's header block: the
// fields the trust policy judges by and the fields a tool reports.
type Headers struct {
	From        string
	To          []string
	Cc          []string
	Subject     string
	Date        time.Time
	MessageID   string
	InReplyTo   string
	References  []string
	AuthResults []string
}

// ReadHeaders parses a message's header block (all of a message, or the
// header section alone as BODY.PEEK[HEADER] returns it).
func ReadHeaders(raw []byte) (Headers, error) {
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return Headers{}, fmt.Errorf("imapmail: reading headers: %w", err)
	}
	return headersOf(mail.Header{Header: message.Header{Header: h}}), nil
}

func headersOf(mh mail.Header) Headers {
	out := Headers{
		From:      mh.Get("From"),
		To:        addressList(mh, "To"),
		Cc:        addressList(mh, "Cc"),
		MessageID: strings.Trim(strings.TrimSpace(mh.Get("Message-Id")), "<>"),
		InReplyTo: strings.Trim(strings.TrimSpace(mh.Get("In-Reply-To")), "<>"),
	}
	if subj, err := mh.Subject(); err == nil {
		out.Subject = subj
	} else {
		out.Subject = mh.Get("Subject")
	}
	if d, err := mh.Date(); err == nil {
		out.Date = d
	}
	for r := range strings.FieldsSeq(mh.Get("References")) {
		out.References = append(out.References, strings.Trim(r, "<>"))
	}
	fields := mh.FieldsByKey("Authentication-Results")
	for fields.Next() {
		out.AuthResults = append(out.AuthResults, fields.Value())
	}
	return out
}

func addressList(mh mail.Header, key string) []string {
	addrs, err := mh.AddressList(key)
	if err != nil {
		if v := mh.Get(key); v != "" {
			return []string{v}
		}
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}

// Attachment is a part a message carries as a file.
type Attachment struct {
	// Index addresses the attachment within its message, for
	// mail_attachment; it is the attachment's position, from zero.
	Index       int    `json:"index"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

// Body is a message's readable text and the files it carries.
type Body struct {
	Text string
	// Format is "text" when the message had a plain-text part, "html" when
	// the text was read from its HTML, and "none" when it had neither.
	Format      string
	Attachments []Attachment
}

// ErrAttachmentNotFound is the refusal of an attachment index the message
// does not have.
var ErrAttachmentNotFound = errors.New("imapmail: the message has no attachment at that index")

// maxPartBytes bounds how much of one part is read.
const maxPartBytes = 32 << 20

// ReadBody walks a whole message: the first text/plain part is the body, else
// the first text/html part read as text; every part with a filename, or an
// attachment disposition, is listed.
func ReadBody(raw []byte) (Body, error) {
	var plain, htmlText string
	var atts []Attachment
	err := walk(raw, func(p part) error {
		switch {
		case p.attachment:
			atts = append(atts, Attachment{Index: len(atts), Name: p.name, ContentType: p.contentType, Size: int64(len(p.data))})
		case p.contentType == "text/plain" && plain == "":
			plain = string(p.data)
		case p.contentType == "text/html" && htmlText == "":
			htmlText = string(p.data)
		}
		return nil
	})
	if err != nil {
		return Body{}, err
	}
	b := Body{Attachments: atts, Format: "none"}
	switch {
	case strings.TrimSpace(plain) != "":
		b.Text, b.Format = strings.TrimSpace(untrusted.Clean(plain)), "text"
	case htmlText != "":
		b.Text, b.Format = HTMLToText(htmlText), "html"
	}
	if b.Attachments == nil {
		b.Attachments = []Attachment{}
	}
	return b, nil
}

// ReadAttachment returns the bytes of attachment index.
func ReadAttachment(raw []byte, index int) (Attachment, []byte, error) {
	i := 0
	var found Attachment
	var data []byte
	errFound := errors.New("found")
	err := walk(raw, func(p part) error {
		if !p.attachment {
			return nil
		}
		if i == index {
			found = Attachment{Index: i, Name: p.name, ContentType: p.contentType, Size: int64(len(p.data))}
			data = p.data
			return errFound
		}
		i++
		return nil
	})
	if errors.Is(err, errFound) {
		return found, data, nil
	}
	if err != nil {
		return Attachment{}, nil, err
	}
	return Attachment{}, nil, ErrAttachmentNotFound
}

type part struct {
	contentType string
	name        string
	attachment  bool
	data        []byte
}

func walk(raw []byte, visit func(part) error) error {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && mr == nil {
		return fmt.Errorf("imapmail: reading message: %w", err)
	}
	defer mr.Close() //nolint:errcheck // reader over memory
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("imapmail: reading part: %w", err)
		}
		pt, err := readPart(p)
		if err != nil {
			return err
		}
		if err := visit(pt); err != nil {
			return err
		}
	}
}

// readPart reads one MIME part: its bytes, its type, and whether it is a
// file, with a file's name made safe.
func readPart(p *mail.Part) (part, error) {
	data, err := io.ReadAll(io.LimitReader(p.Body, maxPartBytes))
	if err != nil {
		return part{}, fmt.Errorf("imapmail: reading part body: %w", err)
	}
	pt := part{data: data}
	switch h := p.Header.(type) {
	case *mail.InlineHeader:
		pt.contentType, _, _ = h.ContentType()
		if _, params, err := h.ContentDisposition(); err == nil && params["filename"] != "" {
			pt.name, pt.attachment = params["filename"], true
		}
	case *mail.AttachmentHeader:
		pt.contentType, _, _ = h.ContentType()
		pt.name, _ = h.Filename()
		pt.attachment = true
	}
	if pt.contentType == "" {
		pt.contentType = "text/plain"
	}
	if pt.attachment {
		pt.name = safeFilename(pt.name, pt.contentType)
	}
	return pt, nil
}

// maxFilenameRunes bounds a saved attachment's name, the resource library's
// display-name limit.
const maxFilenameRunes = 200

// safeFilename is an attachment's name as the platform will store it: cleaned
// of hidden runes and path separators, and named from its type when the
// sender gave none.
func safeFilename(name, contentType string) string {
	name = untrusted.Line(name)
	name = strings.NewReplacer("/", "_", "\\", "_", "\x00", "").Replace(name)
	name = strings.TrimLeft(name, ".")
	if name == "" {
		name = "attachment"
		if exts, _ := mime.ExtensionsByType(contentType); len(exts) > 0 {
			name += exts[0]
		}
	}
	if r := []rune(name); len(r) > maxFilenameRunes {
		name = string(r[:maxFilenameRunes])
	}
	return name
}
