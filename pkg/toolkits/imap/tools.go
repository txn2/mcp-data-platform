package imap

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/imapmail"
	"github.com/txn2/mcp-data-platform/internal/untrusted"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// maxScan bounds how many messages one search reads the headers of while it
// looks for ones the sender rules admit.
const maxScan = 2000

// headerBatch is how many messages' headers one FETCH asks for.
const headerBatch = 100

// maxBodyRunes bounds a body returned inline.
const maxBodyRunes = 50000

// maxThreadIDs bounds the ids a thread is searched by.
const maxThreadIDs = 50

// RegisterTools registers the mail tools.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	toolkit.AddTool(s, &mcp.Tool{
		Name:  ToolSearch,
		Title: "Search Mail",
		Description: "Search a folder of the platform's mailbox and return message headers only: uid, date, sender, " +
			"recipients, subject, message id, thread references and attachment names and sizes. Only mail from senders " +
			"on the connection's allowlist, authenticated by the receiving server, is returned; the rest is counted in " +
			"hidden_not_allowed and hidden_unauthenticated and never shown. For incremental processing pass since_uid " +
			"and uid_validity from the last run and keep highest_uid. " + untrusted.Notice,
		Annotations: toolkit.ReadOnlyAnnotations(),
	}, t.handleSearch)
	toolkit.AddTool(s, &mcp.Tool{
		Name:  ToolMessage,
		Title: "Read Mail Message",
		Description: "Read one message by uid: its headers, and its body as plain text (HTML converted, hidden content " +
			"removed, quoted replies collapsed unless include_quoted). A message from a sender the connection does not " +
			"trust is refused. " + untrusted.Notice,
		Annotations: toolkit.ReadOnlyAnnotations(),
	}, t.handleMessage)
	toolkit.AddTool(s, &mcp.Tool{
		Name:  ToolThread,
		Title: "Read Mail Thread",
		Description: "Read the thread a message belongs to (by Message-ID, In-Reply-To and References), oldest first, " +
			"each message with its body. Messages from senders the connection does not trust are left out and counted. " +
			untrusted.Notice,
		Annotations: toolkit.ReadOnlyAnnotations(),
	}, t.handleThread)
	if t.resourceLander() != nil {
		toolkit.AddTool(s, &mcp.Tool{
			Name:  ToolAttachment,
			Title: "Save Mail Attachment",
			Description: "Save one attachment of a trusted message into your resource library (default folder mail/<connection>), " +
				"with its own content type and the connection's size cap, so the file tools can read it. The file is " +
				"stored, never opened, run or rendered here. Returns the resource reference.",
			Annotations: toolkit.WriteAnnotations(false),
		}, t.handleAttachment)
	}
}

// call is one tool call's session on the folder it names.
type call struct {
	cfg      Config
	s        *imapmail.Session
	folder   string
	validity uint32
	cancel   context.CancelFunc
}

// close logs out and releases the call's deadline.
func (c *call) close() {
	_ = c.s.Close()
	c.cancel()
}

// session opens a call's session on the folder it names, bounded by the
// connection's call timeout.
func (t *Toolkit) session(ctx context.Context, conn, folder string) (*call, error) {
	c, err := t.conns.Get(ctx, conn)
	if err != nil {
		return nil, err //nolint:wrapcheck // the registry names the kind
	}
	if folder == "" {
		folder = c.Folders[0]
	}
	if !c.allowsFolder(folder) {
		return nil, fmt.Errorf("imap: %s may read only %s, not %q", c.ConnectionName, strings.Join(c.Folders, ", "), folder)
	}
	ctx, cancel := context.WithTimeout(ctx, c.CallTimeout)
	s, err := open(ctx, c)
	if err != nil {
		cancel()
		return nil, err
	}
	validity, _, err := s.Examine(folder)
	if err != nil {
		_ = s.Close()
		cancel()
		return nil, fmt.Errorf("imap: %w", err)
	}
	return &call{cfg: c, s: s, folder: folder, validity: validity, cancel: cancel}, nil
}
