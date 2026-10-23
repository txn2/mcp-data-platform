package imapmail

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func crlf(lines ...string) []byte { return []byte(strings.Join(lines, "\r\n") + "\r\n") }

// invoiceMail is a multipart/mixed message: an alternative of plain text and
// HTML, then a PDF attachment, with the headers the receiving server added.
var invoiceMail = crlf(
	"Authentication-Results: mx.company.example; dkim=pass header.d=vendor.example; spf=pass smtp.mailfrom=ap@vendor.example",
	"Authentication-Results: attacker.example; dmarc=pass",
	"From: Vendor AP <ap@vendor.example>",
	"To: platform@company.example",
	"Cc: Dana Ortiz <dana@company.example>, sam@company.example",
	"Subject: =?UTF-8?Q?Invoice_4411_=E2=80=93_October?=",
	"Date: Tue, 20 Oct 2026 09:14:00 +0200",
	"Message-ID: <inv-4411@vendor.example>",
	"In-Reply-To: <req-77@company.example>",
	"References: <req-70@company.example> <req-77@company.example>",
	"MIME-Version: 1.0",
	`Content-Type: multipart/mixed; boundary="outer"`,
	"",
	"--outer",
	`Content-Type: multipart/alternative; boundary="alt"`,
	"",
	"--alt",
	"Content-Type: text/plain; charset=utf-8",
	"",
	"Please find invoice 4411 attached.",
	"",
	"> earlier",
	"--alt",
	"Content-Type: text/html; charset=utf-8",
	"",
	"<p>Please find invoice 4411 attached.</p>",
	"--alt--",
	"--outer",
	`Content-Type: application/pdf; name="inv-4411.pdf"`,
	`Content-Disposition: attachment; filename="../inv-4411.pdf"`,
	"Content-Transfer-Encoding: base64",
	"",
	"JVBERi0xLjQK",
	"--outer--",
)

func TestReadHeaders(t *testing.T) {
	h, err := ReadHeaders(invoiceMail)
	require.NoError(t, err)
	assert.Equal(t, "Vendor AP <ap@vendor.example>", h.From)
	assert.Equal(t, "Invoice 4411 – October", h.Subject, "encoded words are decoded")
	assert.Equal(t, []string{"<platform@company.example>"}, h.To)
	assert.Len(t, h.Cc, 2)
	assert.Equal(t, "inv-4411@vendor.example", h.MessageID)
	assert.Equal(t, "req-77@company.example", h.InReplyTo)
	assert.Equal(t, []string{"req-70@company.example", "req-77@company.example"}, h.References)
	assert.Equal(t, "2026-10-20T07:14:00Z", h.Date.UTC().Format("2006-01-02T15:04:05Z"))
	require.Len(t, h.AuthResults, 2)
	assert.True(t, strings.HasPrefix(h.AuthResults[0], "mx.company.example"), "topmost first")
}

func TestReadBody_PrefersPlainAndListsAttachments(t *testing.T) {
	b, err := ReadBody(invoiceMail)
	require.NoError(t, err)
	assert.Equal(t, "text", b.Format)
	assert.Equal(t, "Please find invoice 4411 attached.\n\n> earlier", b.Text)
	require.Len(t, b.Attachments, 1)
	assert.Equal(t, Attachment{Index: 0, Name: "_inv-4411.pdf", ContentType: "application/pdf", Size: 9}, b.Attachments[0])
}

func TestReadBody_HTMLOnly(t *testing.T) {
	raw := crlf("From: a@vendor.example", "Content-Type: text/html; charset=utf-8", "",
		`<p>Visible</p><p style="display:none">secret instruction</p>`)
	b, err := ReadBody(raw)
	require.NoError(t, err)
	assert.Equal(t, "html", b.Format)
	assert.Equal(t, "Visible", b.Text)
	assert.Equal(t, []Attachment{}, b.Attachments, "no attachments is [] not null")
}

func TestReadBody_NoBody(t *testing.T) {
	raw := crlf("From: a@vendor.example", "Content-Type: application/octet-stream", "Content-Disposition: attachment", "", "xx")
	b, err := ReadBody(raw)
	require.NoError(t, err)
	assert.Equal(t, "none", b.Format)
	require.Len(t, b.Attachments, 1)
	assert.True(t, strings.HasPrefix(b.Attachments[0].Name, "attachment"), "a nameless attachment is named from its type")
}

func TestReadAttachment(t *testing.T) {
	a, data, err := ReadAttachment(invoiceMail, 0)
	require.NoError(t, err)
	assert.Equal(t, "_inv-4411.pdf", a.Name)
	assert.Equal(t, "%PDF-1.4\n", string(data))

	_, _, err = ReadAttachment(invoiceMail, 1)
	assert.ErrorIs(t, err, ErrAttachmentNotFound)
}

func TestSafeFilename(t *testing.T) {
	assert.Equal(t, "a_b.txt", safeFilename("a/b.txt", "text/plain"))
	assert.Equal(t, "evil.exe", safeFilename("..evil.exe", ""))
	assert.Equal(t, "rep", safeFilename("re\u202ep", ""))
	assert.Len(t, []rune(safeFilename(strings.Repeat("x", 300), "")), 200)
	assert.True(t, strings.HasPrefix(safeFilename("", "application/pdf"), "attachment."))
}
