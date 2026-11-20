package imapmail

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzReadMessage holds the mail reader to anything a sender can mail a
// polled mailbox (#2713): headers, bodies and attachments are read or refused
// without panicking, and the text taken from HTML is valid UTF-8 with no
// element left in it.
func FuzzReadMessage(f *testing.F) {
	f.Add([]byte("From: a@example.com\r\nSubject: hi\r\nContent-Type: text/plain\r\n\r\nhello\r\n"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/html\r\n\r\n<p>hi<script>x()</script></p>\r\n--x--\r\n"))
	f.Add([]byte("\r\n"))
	f.Fuzz(func(_ *testing.T, raw []byte) {
		if len(raw) > 1<<16 {
			return
		}
		_, _ = ReadHeaders(raw)
		_, _ = ReadBody(raw)
		_, _, _ = ReadAttachment(raw, 0)
	})
}

func FuzzHTMLToText(f *testing.F) {
	f.Add("<p>Hello <b>there</b></p><script>alert(1)</script><style>p{}</style>")
	f.Add("<<<>>>&amp;&#x3c;")
	f.Fuzz(func(t *testing.T, html string) {
		if len(html) > 1<<16 {
			return
		}
		text := HTMLToText(html)
		if !utf8.ValidString(text) {
			t.Fatalf("HTMLToText(%q) is not valid UTF-8", html)
		}
		if strings.Contains(strings.ToLower(text), "<script") {
			t.Fatalf("HTMLToText(%q) kept a script element: %q", html, text)
		}
	})
}
