package imapmail

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzReadMessage holds the mail reader to anything a sender can mail a
// polled mailbox (#2713): headers, bodies and attachments are read or refused
// without panicking.
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

// hiddenCanary is text the fuzzer is not expected to write, put inside each
// element FuzzHTMLToText hides.
const hiddenCanary = "zq7-hidden-2754"

// hiddenWraps are elements whose content a reader never sees, each with a
// place for the canary.
var hiddenWraps = []string{ //nolint:gochecknoglobals // fixed table
	"<script>%s</script>", "<style>%s</style>", "<head><title>%s</title></head>", "<div hidden>%s</div>",
	`<span aria-hidden="true">%s</span>`, `<p style="display:none">%s</p>`, `<b style="font-size:0">%s</b>`,
	"<template>%s</template>", "<svg><text>%s</text></svg>",
}

// FuzzHTMLToText holds the converter to any HTML a sender can mail: the text
// is valid UTF-8, and nothing a script, style or hidden element held reaches
// it, whatever follows that element. The text may contain "<script" (#2754):
// a sender who writes &lt;script&gt;, or a '<' and "script" a reader sees side
// by side, shows the reader those characters, and the converter writes text,
// never markup, so its output is escaped wherever it is shown as HTML.
func FuzzHTMLToText(f *testing.F) {
	f.Add("<p>Hello <b>there</b></p><script>alert(1)</script><style>p{}</style>")
	f.Add("<<<>>>&amp;&#x3c;")
	f.Add("</div></script><textarea>")
	f.Fuzz(func(t *testing.T, html string) {
		if len(html) > 1<<16 || strings.Contains(html, hiddenCanary) {
			return
		}
		if text := HTMLToText(html); !utf8.ValidString(text) {
			t.Fatalf("HTMLToText(%q) is not valid UTF-8", html)
		}
		for _, wrap := range hiddenWraps {
			doc := fmt.Sprintf(wrap, hiddenCanary) + html
			if text := HTMLToText(doc); strings.Contains(text, hiddenCanary) {
				t.Fatalf("HTMLToText(%q) kept what the element hid: %q", doc, text)
			}
		}
	})
}
