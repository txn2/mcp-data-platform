package imap

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/imapmail"
	"github.com/txn2/mcp-data-platform/internal/mailtrust"
	"github.com/txn2/mcp-data-platform/internal/untrusted"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Message is one message read in full.
type Message struct {
	Summary
	BodyFormat      string `json:"body_format"`
	QuotedCollapsed int    `json:"quoted_lines_collapsed,omitempty"`
	BodyTruncated   bool   `json:"body_truncated,omitempty"`
}

// MessageOutput is mail_message's result.
type MessageOutput struct {
	Connection string      `json:"connection"`
	Message    MessageView `json:"message"`
	Notice     string      `json:"notice"`
}

// MessageView is a message with its body under external, beside the
// sender's headers.
type MessageView struct {
	Message
	External MessageText `json:"external"`
}

// MessageText is everything the sender wrote.
type MessageText struct {
	SummaryText
	Body string `json:"body"`
}

// ThreadOutput is mail_thread's result.
type ThreadOutput struct {
	Connection            string        `json:"connection"`
	Folder                string        `json:"folder"`
	Messages              []MessageView `json:"messages"`
	HiddenNotAllowed      int           `json:"hidden_not_allowed"`
	HiddenUnauthenticated int           `json:"hidden_unauthenticated"`
	Truncated             bool          `json:"truncated"`
	Notice                string        `json:"notice"`
}

// withheldError is the refusal of a message the sender rules do not admit. It
// says why without saying anything the message contains.
type withheldError struct {
	uid     uint32
	verdict mailtrust.Verdict
}

func (e withheldError) Error() string {
	why := "its sender is not on this connection's allowlist"
	switch e.verdict {
	case mailtrust.NotAuthenticated:
		why = "the receiving server did not record an SPF, DKIM or DMARC pass for its sender, so its From address may be forged"
	case mailtrust.BadFrom:
		why = "its From header does not name exactly one address"
	}
	return fmt.Sprintf("imap: message %d is not shown: %s", e.uid, why)
}

// read fetches one message whole and judges it, refusing one the rules do
// not admit before anything of it is parsed further.
func read(s *imapmail.Session, c Config, folder string, uid uint32, includeQuoted bool) (MessageView, []byte, error) {
	raw, err := s.Whole(uid, c.MaxMessageBytes)
	if err != nil {
		return MessageView{}, nil, err //nolint:wrapcheck // the protocol package names itself
	}
	h, err := imapmail.ReadHeaders(raw)
	if err != nil {
		return MessageView{}, nil, err //nolint:wrapcheck // the protocol package names itself
	}
	j := c.Policy().Judge(h.From, h.AuthResults)
	if j.Verdict != mailtrust.Trusted {
		return MessageView{}, nil, withheldError{uid: uid, verdict: j.Verdict}
	}
	body, err := imapmail.ReadBody(raw)
	if err != nil {
		return MessageView{}, nil, err //nolint:wrapcheck // the protocol package names itself
	}
	m := Message{Summary: summaryOf(meta{uid: uid, folder: folder, size: int64(len(raw)), attachments: body.Attachments}, h, j), BodyFormat: body.Format}
	text := body.Text
	if !includeQuoted {
		text, m.QuotedCollapsed = imapmail.CollapseQuoted(text)
	}
	if r := []rune(text); len(r) > maxBodyRunes {
		text, m.BodyTruncated = string(r[:maxBodyRunes]), true
	}
	return MessageView{Message: m, External: MessageText{SummaryText: m.External, Body: text}}, raw, nil
}

func (t *Toolkit) handleMessage(ctx context.Context, _ *mcp.CallToolRequest, in MessageInput) (*mcp.CallToolResult, any, error) {
	cl, err := t.session(ctx, in.Connection, in.Folder)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	defer cl.close()
	view, _, err := read(cl.s, cl.cfg, cl.folder, in.UID, in.IncludeQuoted)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	out := MessageOutput{Connection: cl.cfg.ConnectionName, Message: view, Notice: untrusted.Notice}
	return toolkit.JSONResult(out), out, nil
}

func (t *Toolkit) handleThread(ctx context.Context, _ *mcp.CallToolRequest, in MessageInput) (*mcp.CallToolResult, any, error) {
	cl, err := t.session(ctx, in.Connection, in.Folder)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	defer cl.close()
	anchor, _, err := read(cl.s, cl.cfg, cl.folder, in.UID, false)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	out, err := thread(cl.s, cl.cfg, cl.folder, anchor)
	if err != nil {
		return toolkit.ErrorResult(fmt.Sprintf("imap: %v", err)), nil, nil
	}
	return toolkit.JSONResult(out), out, nil
}

// thread finds every message naming one of the anchor's thread ids and reads
// the trusted ones, oldest first.
func thread(s *imapmail.Session, c Config, folder string, anchor MessageView) (ThreadOutput, error) {
	out := ThreadOutput{Connection: c.ConnectionName, Folder: folder, Messages: []MessageView{}, Notice: untrusted.Notice}
	ids := threadIDs(anchor.Summary)
	uids, err := s.Search(imapmail.Criteria{HeaderIDs: ids})
	if err != nil {
		return out, err //nolint:wrapcheck // the protocol package names itself
	}
	if !slices.Contains(uids, anchor.UID) {
		uids = append(uids, anchor.UID)
	}
	slices.Sort(uids)
	for _, uid := range uids {
		if len(out.Messages) >= c.MaxResults {
			out.Truncated = true
			break
		}
		view, _, err := read(s, c, folder, uid, false)
		var withheld withheldError
		switch {
		case errors.As(err, &withheld):
			countHidden(withheld.verdict, &out.HiddenNotAllowed, &out.HiddenUnauthenticated)
			continue
		case err != nil:
			return out, err
		}
		out.Messages = append(out.Messages, view)
	}
	slices.SortStableFunc(out.Messages, func(a, b MessageView) int { return strings.Compare(a.Date, b.Date) })
	return out, nil
}

func threadIDs(s Summary) []string {
	var ids []string
	add := func(id string) {
		if id != "" && !slices.Contains(ids, id) && len(ids) < maxThreadIDs {
			ids = append(ids, id)
		}
	}
	for _, r := range s.References {
		add(r)
	}
	add(s.InReplyTo)
	add(s.MessageID)
	return ids
}

// tags are the resource tags an attachment is saved with: lowercase letters,
// digits and hyphens, at most 50 runes, as the resource library requires. A
// connection name that leaves nothing is not a tag.
func tags(names ...string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		var b strings.Builder
		for _, r := range strings.ToLower(n) {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				_, _ = b.WriteRune(r)
			case b.Len() > 0:
				_ = b.WriteByte('-')
			}
		}
		t := strings.Trim(b.String(), "-")
		if len(t) > maxTagLen {
			t = strings.TrimRight(t[:maxTagLen], "-")
		}
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
