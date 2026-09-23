package scriptlex

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCodeOnly pins what the mask keeps: quotes, newlines and every byte
// offset, so a finding's line is counted from the masked text unchanged.
func TestCodeOnly(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"string and comment":      {"x = \"ab\" # c\n", "x = \"  \" #  \n"},
		"triple spans lines":      {"s = '''a\nb''' + f\n", "s = ''' \n ''' + f\n"},
		"unterminated at newline": {"s = \"ab\nf'x'\n", "s = \"  \nf' '\n"},
		"unterminated at end":     {"s = \"\"\"ab\ncd", "s = \"\"\"  \n  "},
		"backslash newline":       {"s = \"a\\\nb\"\n", "s = \"  \n \"\n"},
		"comment at end":          {"x = 1 # no newline", "x = 1 #           "},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := CodeOnly(tc.in)
			assert.Equal(t, tc.want, got)
			assert.Len(t, got, len(tc.in))
		})
	}
}
