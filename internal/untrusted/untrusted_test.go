package untrusted

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClean(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"plain text kept", "Quarterly review\n\tagenda", "Quarterly review\n\tagenda"},
		{"zero-width space removed", "ig\u200Bnore", "ignore"},
		{"joiners and word joiner removed", "a\u200Cb\u200Dc\u2060d", "abcd"},
		{"bidi override removed", "invoice\u202Efdp.exe", "invoicefdp.exe"},
		{"bidi isolates removed", "\u2066x\u2069", "x"},
		{"byte order mark removed", "\uFEFFhello", "hello"},
		{"unicode tag block removed", "hi\U000E0041\U000E0042", "hi"},
		{"control characters removed", "a\x00b\x1bc\rd", "abcd"},
		{"hangul filler removed", "a\u3164b", "ab"},
		{"invalid utf-8 replaced", "a\xffb", "a\uFFFDb"},
		{"long blank runs collapsed", "a\n\n\n\n\n\nb", "a\n\n\nb"},
		{"accented and non-latin text kept", "Café 会议 Ünïcode", "Café 会议 Ünïcode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Clean(tc.in))
		})
	}
}

func TestLine(t *testing.T) {
	assert.Equal(t, "Board meeting moved", Line("  Board\r\n meeting\u200B\tmoved \n"))
	assert.Empty(t, Line("\u200B \n"))
}

func TestHidden(t *testing.T) {
	assert.False(t, Hidden('\t'))
	assert.False(t, Hidden('\n'))
	assert.False(t, Hidden('a'))
	assert.True(t, Hidden('\u200B'))
	assert.True(t, Hidden('\u2800'))
}
