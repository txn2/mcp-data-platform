package imap

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/imapmail"
	"github.com/txn2/mcp-data-platform/internal/mailtrust"
	"github.com/txn2/mcp-data-platform/internal/untrusted"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// SearchInput is mail_search's arguments.
type SearchInput struct {
	Connection string `json:"connection,omitempty" jsonschema:"The imap connection. Optional when the deployment has one."`
	Folder     string `json:"folder,omitempty" jsonschema:"One of the connection's folders. Defaults to the first."`
	From       string `json:"from,omitempty" jsonschema:"Text the From header contains."`
	To         string `json:"to,omitempty" jsonschema:"Text the To header contains."`
	Cc         string `json:"cc,omitempty" jsonschema:"Text the Cc header contains."`
	Subject    string `json:"subject,omitempty" jsonschema:"Text the Subject contains."`
	Text       string `json:"text,omitempty" jsonschema:"Text anywhere in the headers or body."`
	Since      string `json:"since,omitempty" jsonschema:"Received on or after this date (YYYY-MM-DD)."`
	Before     string `json:"before,omitempty" jsonschema:"Received before this date (YYYY-MM-DD)."`
	Unseen     bool   `json:"unseen,omitempty" jsonschema:"Only messages no one has opened in a mail client."`
	SinceUID   uint32 `json:"since_uid,omitempty" jsonschema:"Only messages with a uid above this: a script's watermark. Returned oldest first."`
	UIDValid   uint32 `json:"uid_validity,omitempty" jsonschema:"The uid_validity the watermark was read under. When the folder's differs, its uids were renumbered and since_uid is ignored."`
	Limit      int    `json:"limit,omitempty" jsonschema:"Most messages to return, up to the connection's max_results."`
}

// Summary is one message as a search reports it: the platform's fields, and
// what the sender wrote under External.
type Summary struct {
	UID         uint32                `json:"uid"`
	Folder      string                `json:"folder"`
	Date        string                `json:"date,omitempty"`
	Size        int64                 `json:"size,omitempty"`
	MessageID   string                `json:"message_id,omitempty"`
	InReplyTo   string                `json:"in_reply_to,omitempty"`
	References  []string              `json:"references,omitempty"`
	FromAddress string                `json:"from_address"`
	SenderAuth  string                `json:"sender_auth"`
	Attachments []imapmail.Attachment `json:"attachments"`
	External    SummaryText           `json:"external"`
}

// SummaryText is what the sender wrote into the headers.
type SummaryText struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Cc      []string `json:"cc,omitempty"`
	Subject string   `json:"subject"`
}

// SearchOutput is mail_search's result.
type SearchOutput struct {
	Connection  string    `json:"connection"`
	Folder      string    `json:"folder"`
	UIDValidity uint32    `json:"uid_validity"`
	Messages    []Summary `json:"messages"`
	// HighestUID is the newest uid this search read, a script's next
	// watermark even when every message was hidden.
	HighestUID uint32 `json:"highest_uid"`
	// UIDValidityChanged is set when the caller's uid_validity no longer
	// holds and since_uid was ignored.
	UIDValidityChanged bool `json:"uid_validity_changed,omitempty"`
	// HiddenNotAllowed and HiddenUnauthenticated count the matching
	// messages the sender rules withheld. Nothing else about them is shown.
	HiddenNotAllowed      int    `json:"hidden_not_allowed"`
	HiddenUnauthenticated int    `json:"hidden_unauthenticated"`
	Truncated             bool   `json:"truncated"`
	Notice                string `json:"notice"`
}

func (t *Toolkit) handleSearch(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
	crit, err := in.criteria()
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	cl, err := t.session(ctx, in.Connection, in.Folder)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	defer cl.close()
	out := SearchOutput{Connection: cl.cfg.ConnectionName, Folder: cl.folder, UIDValidity: cl.validity, Messages: []Summary{}, Notice: untrusted.Notice}
	if in.UIDValid != 0 && in.UIDValid != cl.validity {
		out.UIDValidityChanged, crit.SinceUID = true, 0
	}
	uids, err := cl.s.Search(crit)
	if err != nil {
		return toolkit.ErrorResult(fmt.Sprintf("imap: %v", err)), nil, nil
	}
	if len(uids) > 0 {
		out.HighestUID = uids[len(uids)-1]
	}
	if crit.SinceUID == 0 {
		slices.Reverse(uids) // newest first, unless a watermark asks for order
	}
	if err := scanSummaries(cl, uids, clampLimit(in.Limit, cl.cfg.MaxResults), &out); err != nil {
		return toolkit.ErrorResult(fmt.Sprintf("imap: %v", err)), nil, nil
	}
	return toolkit.JSONResult(out), out, nil
}

// scanSummaries reads headers in batches, keeping the messages the sender
// rules admit until limit, counting the ones they withhold.
func scanSummaries(cl *call, uids []uint32, limit int, out *SearchOutput) error {
	sc := scan{cl: cl, policy: cl.cfg.Policy(), limit: limit, out: out}
	for start := 0; start < len(uids); start += headerBatch {
		if len(out.Messages) >= limit || sc.scanned >= maxScan {
			out.Truncated = true
			return nil
		}
		if err := sc.batch(uids[start:min(start+headerBatch, len(uids))]); err != nil {
			return err
		}
	}
	return nil
}

// scan is one search's walk over its matches.
type scan struct {
	cl      *call
	policy  mailtrust.Policy
	limit   int
	scanned int
	out     *SearchOutput
}

func (sc *scan) batch(uids []uint32) error {
	envs, err := sc.cl.s.Headers(uids)
	if err != nil {
		return err //nolint:wrapcheck // the protocol package names itself
	}
	byUID := make(map[uint32]imapmail.Envelope, len(envs))
	for _, e := range envs {
		byUID[e.UID] = e
	}
	for _, uid := range uids {
		if e, ok := byUID[uid]; ok {
			sc.take(e)
		}
	}
	return nil
}

func (sc *scan) take(e imapmail.Envelope) {
	sc.scanned++
	sum, verdict := summarize(e, sc.cl.folder, sc.policy)
	if !countHidden(verdict, &sc.out.HiddenNotAllowed, &sc.out.HiddenUnauthenticated) {
		return
	}
	if len(sc.out.Messages) >= sc.limit {
		sc.out.Truncated = true
		return
	}
	sc.out.Messages = append(sc.out.Messages, sum)
}

// countHidden counts a withheld verdict and reports whether the message may
// be shown.
func countHidden(v mailtrust.Verdict, notAllowed, unauthenticated *int) bool {
	switch v {
	case mailtrust.Trusted:
		return true
	case mailtrust.NotAuthenticated:
		*unauthenticated++
	default:
		*notAllowed++
	}
	return false
}

func summarize(e imapmail.Envelope, folder string, p mailtrust.Policy) (Summary, mailtrust.Verdict) {
	h, err := imapmail.ReadHeaders(e.Header)
	if err != nil {
		return Summary{}, mailtrust.BadFrom
	}
	j := p.Judge(h.From, h.AuthResults)
	if j.Verdict != mailtrust.Trusted {
		return Summary{}, j.Verdict
	}
	return summaryOf(meta{uid: e.UID, folder: folder, size: e.Size, attachments: e.Attachments}, h, j), j.Verdict
}

// meta is what the platform knows of a message apart from its headers.
type meta struct {
	uid         uint32
	folder      string
	size        int64
	attachments []imapmail.Attachment
}

func summaryOf(m meta, h imapmail.Headers, j mailtrust.Judgment) Summary {
	sum := Summary{
		UID: m.uid, Folder: m.folder, Size: m.size,
		MessageID: untrusted.Line(h.MessageID), InReplyTo: untrusted.Line(h.InReplyTo),
		FromAddress: j.From, SenderAuth: j.Auth, Attachments: m.attachments,
		External: SummaryText{From: untrusted.Line(h.From), To: lines(h.To), Cc: lines(h.Cc), Subject: untrusted.Line(h.Subject)},
	}
	if sum.Attachments == nil {
		sum.Attachments = []imapmail.Attachment{}
	}
	for _, r := range h.References {
		sum.References = append(sum.References, untrusted.Line(r))
	}
	if !h.Date.IsZero() {
		sum.Date = h.Date.UTC().Format(time.RFC3339)
	}
	return sum
}

func lines(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, untrusted.Line(s))
	}
	return out
}

func clampLimit(asked, ceiling int) int {
	if asked <= 0 || asked > ceiling {
		return ceiling
	}
	return asked
}

func (in SearchInput) criteria() (imapmail.Criteria, error) {
	c := imapmail.Criteria{From: in.From, To: in.To, Cc: in.Cc, Subject: in.Subject, Text: in.Text, Unseen: in.Unseen, SinceUID: in.SinceUID}
	var err error
	if c.Since, err = parseDate("since", in.Since); err != nil {
		return c, err
	}
	if c.Before, err = parseDate("before", in.Before); err != nil {
		return c, err
	}
	return c, nil
}

func parseDate(field, v string) (time.Time, error) {
	if strings.TrimSpace(v) == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, fmt.Errorf("imap: %s must be a YYYY-MM-DD date, not %q", field, v)
	}
	return t, nil
}
