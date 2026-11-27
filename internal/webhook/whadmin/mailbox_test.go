package whadmin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whimap"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// fakeMailbox records the sources it was asked to check and refuses while
// refuse is set.
type fakeMailbox struct {
	checked []string
	refuse  error
	marks   []whimap.Mark
	repair  *whimap.RepairRecord
}

func (f *fakeMailbox) Check(_ context.Context, src whsource.Source) error {
	f.checked = append(f.checked, src.Config.IMAP.Host)
	return f.refuse
}

func (f *fakeMailbox) Repair(context.Context, string) (*whimap.RepairRecord, error) {
	return f.repair, nil
}

func (f *fakeMailbox) Marks(context.Context, string) ([]whimap.Mark, error) {
	return f.marks, nil
}

func imapInput() whsource.Source {
	return whsource.Source{
		Name: "inbox", Enabled: true, Connection: "scratch", CreatedBy: "admin@example.com",
		Auth: whsource.Auth{Mode: whsource.AuthIMAP, Username: "platform", Secret: "pw"},
		Config: whsource.Config{
			Input: whsource.InputIMAP,
			IMAP:  &whsource.IMAP{Host: "mail.example.com", Folders: []string{"INBOX"}, TrustedAuthServID: "mx.example.com", AllowSenders: []string{"*@example.com"}},
		},
	}
}

func TestCreate_ChecksTheMailbox(t *testing.T) {
	r := newRig()
	mb := &fakeMailbox{refuse: errBoom}
	r.svc.deps.Mailbox = mb

	_, err := r.svc.Create(ctx, imapInput())
	require.ErrorIs(t, err, whsource.ErrInvalid, "a mailbox the platform cannot open is refused")
	assert.ErrorContains(t, err, "boom")
	assert.Zero(t, r.tables.created, "nothing is built for a refused source")

	_, err = r.svc.Create(ctx, input())
	require.NoError(t, err, "an http source is never checked")
	assert.Equal(t, []string{"mail.example.com"}, mb.checked)

	mb.refuse = nil
	src, err := r.svc.Create(ctx, imapInput())
	require.NoError(t, err)
	assert.Equal(t, whsource.InputIMAP, src.Config.Input)
}

func TestUpdate_ChecksTheMailbox(t *testing.T) {
	r := newRig()
	mb := &fakeMailbox{}
	r.svc.deps.Mailbox = mb
	_, err := r.svc.Create(ctx, imapInput())
	require.NoError(t, err)

	cfg := imapInput().Config
	cfg.IMAP.Host = "other.example.com"
	mb.refuse = errBoom
	_, err = r.svc.Update(ctx, "inbox", Update{Auth: whsource.Auth{Mode: whsource.AuthIMAP, Username: "platform"}, Config: cfg})
	require.ErrorIs(t, err, whsource.ErrInvalid)
	stored, _, err := r.svc.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Equal(t, "mail.example.com", stored.Config.IMAP.Host, "a refused update changes nothing")

	mb.refuse = nil
	src, err := r.svc.Update(ctx, "inbox", Update{Auth: whsource.Auth{Mode: whsource.AuthIMAP, Username: "platform"}, Config: cfg})
	require.NoError(t, err)
	assert.Equal(t, "other.example.com", src.Config.IMAP.Host)
	assert.Equal(t, "pw", src.Auth.Secret, "an empty password keeps the stored one")
	assert.Equal(t, []string{"mail.example.com", "other.example.com", "other.example.com"}, mb.checked)
}

func TestMarks(t *testing.T) {
	r := newRig()
	got, err := r.svc.Marks(ctx, "inbox")
	require.NoError(t, err)
	assert.Equal(t, []whimap.Mark{}, got, "no mailbox is an empty list, never null")

	r.svc.deps.Mailbox = &fakeMailbox{marks: []whimap.Mark{{Folder: "INBOX", Stored: 3}}}
	got, err = r.svc.Marks(ctx, "inbox")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(3), got[0].Stored)
}

func TestDelete_RemovesAMailboxSourcesAttachments(t *testing.T) {
	r := newRig()
	r.svc.deps.Mailbox = &fakeMailbox{}
	_, err := r.svc.Create(ctx, imapInput())
	require.NoError(t, err)
	_, err = r.svc.Create(ctx, input())
	require.NoError(t, err)

	require.NoError(t, r.svc.Delete(ctx, "esp"))
	assert.Empty(t, r.resources.attachments, "an http source saved no attachments")

	r.resources.attachErr = errBoom
	require.ErrorIs(t, r.svc.Delete(ctx, "inbox"), errBoom)
	_, _, err = r.svc.Get(ctx, "inbox")
	require.NoError(t, err, "a source whose attachments are not gone is kept, so the delete can be retried")

	r.resources.attachErr = nil
	r.resources.folderErr = errBoom
	require.ErrorIs(t, r.svc.Delete(ctx, "inbox"), errBoom)
	_, _, err = r.svc.Get(ctx, "inbox")
	require.NoError(t, err, "#2836: a source whose folders are not gone is kept, so the delete can be retried")

	r.resources.folderErr = nil
	require.NoError(t, r.svc.Delete(ctx, "inbox"))
	assert.Equal(t, []string{"inbox", "inbox", "inbox"}, r.resources.attachments)
	assert.Equal(t, []string{"esp", "inbox", "inbox"}, r.resources.folders,
		"#2836: every deleted source's stored folders go, an http source's day folders too")
}

func TestUpdate_KeepsTheInput(t *testing.T) {
	r := newRig()
	r.svc.deps.Mailbox = &fakeMailbox{}
	_, err := r.svc.Create(ctx, input())
	require.NoError(t, err)

	cfg := imapInput().Config
	_, err = r.svc.Update(ctx, "esp", Update{Auth: whsource.Auth{Mode: whsource.AuthIMAP, Username: "platform"}, Config: cfg})
	require.ErrorIs(t, err, whsource.ErrInvalid, "a posted source does not become a mailbox")
	stored, _, err := r.svc.Get(ctx, "esp")
	require.NoError(t, err)
	assert.Equal(t, whsource.InputHTTP, stored.Config.Input)

	_, err = r.svc.Create(ctx, imapInput())
	require.NoError(t, err)
	cfg.Input = ""
	src, err := r.svc.Update(ctx, "inbox", Update{Auth: whsource.Auth{Mode: whsource.AuthIMAP, Username: "platform"}, Config: cfg})
	require.NoError(t, err)
	assert.Equal(t, whsource.InputIMAP, src.Config.Input, "an update that leaves the input out keeps the mailbox")
}

// TestAttachmentRepair: the service reads a source's repair through its
// mailbox, and a deployment with no mailbox input has none (#2836).
func TestAttachmentRepair(t *testing.T) {
	r := newRig()
	got, err := r.svc.AttachmentRepair(ctx, "inbox")
	require.NoError(t, err)
	assert.Nil(t, got)
	want := &whimap.RepairRecord{Moved: 3}
	r.svc.deps.Mailbox = &fakeMailbox{repair: want}
	got, err = r.svc.AttachmentRepair(ctx, "inbox")
	require.NoError(t, err)
	assert.Same(t, want, got)
}
