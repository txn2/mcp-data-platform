package imap

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/imapmail"
	"github.com/txn2/mcp-data-platform/internal/untrusted"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// MessageInput addresses one message.
type MessageInput struct {
	Connection    string `json:"connection,omitempty" jsonschema:"The imap connection. Optional when the deployment has one."`
	Folder        string `json:"folder,omitempty" jsonschema:"The folder mail_search reported. Defaults to the first."`
	UID           uint32 `json:"uid" jsonschema:"The message's uid, as mail_search reported it."`
	IncludeQuoted bool   `json:"include_quoted,omitempty" jsonschema:"Keep the quoted history under a reply instead of collapsing it."`
}

// AttachmentInput addresses one attachment and where to save it.
type AttachmentInput struct {
	Connection string `json:"connection,omitempty" jsonschema:"The imap connection. Optional when the deployment has one."`
	Folder     string `json:"folder,omitempty" jsonschema:"The folder mail_search reported. Defaults to the first."`
	UID        uint32 `json:"uid" jsonschema:"The message's uid."`
	Index      int    `json:"index" jsonschema:"The attachment's index, as the message's attachments list reports it."`
	Path       string `json:"path,omitempty" jsonschema:"Folder in your resource library to save it under. Defaults to mail/<connection>."`
	Filename   string `json:"filename,omitempty" jsonschema:"Name to save it as. Defaults to the attachment's own name."`
}

// AttachmentOutput is mail_attachment's result.
type AttachmentOutput struct {
	Connection string                   `json:"connection"`
	UID        uint32                   `json:"uid"`
	Attachment imapmail.Attachment      `json:"attachment"`
	Resource   *toolkit.ResourceLanding `json:"resource"`
	Notice     string                   `json:"notice"`
}

func (t *Toolkit) handleAttachment(ctx context.Context, _ *mcp.CallToolRequest, in AttachmentInput) (*mcp.CallToolResult, any, error) {
	lander := t.resourceLander()
	if lander == nil {
		return toolkit.ErrorResult("imap: this deployment has no resource library to save attachments into"), nil, nil
	}
	cl, err := t.session(ctx, in.Connection, in.Folder)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	defer cl.close()
	c := cl.cfg
	view, raw, err := read(cl.s, c, cl.folder, in.UID, false)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	att, data, err := imapmail.ReadAttachment(raw, in.Index)
	if err != nil {
		return toolkit.ErrorResult(fmt.Sprintf("imap: message %d: %v", in.UID, err)), nil, nil
	}
	if int64(len(data)) > c.MaxAttachmentBytes {
		return toolkit.ErrorResult(fmt.Sprintf("imap: the attachment is %d bytes, over this connection's max_attachment_bytes (%d)", len(data), c.MaxAttachmentBytes)), nil, nil
	}
	dest := attachmentDestination(c, in, att, view)
	if err := lander.CheckResourceDestination(ctx, dest); err != nil {
		return toolkit.ErrorResult(fmt.Sprintf("imap: %v", err)), nil, nil
	}
	landing, err := lander.LandResource(ctx, dest, bytes.NewReader(data), att.ContentType)
	if err != nil {
		return toolkit.ErrorResult(fmt.Sprintf("imap: saving the attachment: %v", err)), nil, nil
	}
	out := AttachmentOutput{Connection: c.ConnectionName, UID: in.UID, Attachment: att, Resource: landing, Notice: untrusted.Notice}
	return toolkit.JSONResult(out), out, nil
}

// attachmentDestination is where an attachment is saved: the caller's folder,
// or mail/<connection>, under the attachment's own cleaned name, described
// with where it came from.
func attachmentDestination(c Config, in AttachmentInput, att imapmail.Attachment, view MessageView) toolkit.ResourceDestination {
	folder := strings.Trim(path.Clean("/"+strings.TrimSpace(in.Path)), "/")
	if folder == "" || folder == "." {
		folder = "mail/" + c.ConnectionName
	}
	name := att.Name
	if strings.TrimSpace(in.Filename) != "" {
		name = untrusted.Line(strings.ReplaceAll(in.Filename, "/", "_"))
	}
	return toolkit.ResourceDestination{
		Path:        folder,
		Filename:    name,
		DisplayName: name,
		Description: fmt.Sprintf("Attachment %d of message %d in %s on the %s mail connection, from %s, %s.",
			att.Index, view.UID, view.Folder, c.ConnectionName, view.FromAddress, view.Date),
		ChangeSummary: fmt.Sprintf("saved from mail message %d", view.UID),
		Tags:          tags("mail", c.ConnectionName),
	}
}

// maxTagLen is the resource library's longest tag.
const maxTagLen = 50
