package mention

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScan(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{name: "no mention", body: "looks good to me", want: nil},
		{name: "single", body: "@marcus.johnson(example.com) please review", want: []string{"marcus.johnson@example.com"}},
		{
			name: "terminates before sentence punctuation",
			body: "over to @marcus.johnson(example.com). thanks",
			want: []string{"marcus.johnson@example.com"},
		},
		{
			name: "two mentions keep written order",
			body: "@bob(example.com) and @alice(example.org) both",
			want: []string{"bob@example.com", "alice@example.org"},
		},
		{
			name: "de-duplicated by address, case-insensitively",
			body: "@Bob(Example.com) ... @bob(example.com)",
			want: []string{"bob@example.com"},
		},
		{
			name: "plain email is not a mention",
			body: "reach me at bob@example.com",
			want: nil,
		},
		{
			name: "token glued to a preceding address is not a mention",
			body: "bob@example.com@carol(example.com)",
			want: nil,
		},
		{
			name: "subdomain and hyphenated domain",
			body: "@ops(mail.data-team.example.com)",
			want: []string{"ops@mail.data-team.example.com"},
		},
		{
			name: "empty parens is not a mention",
			body: "@bob() hello",
			want: nil,
		},
		{
			name: "domain without a dot is not a mention",
			body: "@bob(localhost)",
			want: nil,
		},
		{
			name: "mention inside parentheses is still found",
			body: "(cc @bob(example.com))",
			want: []string{"bob@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Emails(Scan(tt.body)))
		})
	}
}

func TestScanRawPreservesWrittenText(t *testing.T) {
	got := Scan("ping @Marcus.Johnson(Example.com) now")
	require.Len(t, got, 1)
	assert.Equal(t, "@Marcus.Johnson(Example.com)", got[0].Raw, "raw keeps the author's casing for rendering")
	assert.Equal(t, "marcus.johnson@example.com", got[0].Email, "address is normalized for comparison")
}

func TestScanCapsTokensPerBody(t *testing.T) {
	var b strings.Builder
	for i := range maxTokensPerBody + 10 {
		b.WriteString("@user")
		b.WriteString(strings.Repeat("x", i+1))
		b.WriteString("(example.com) ")
	}
	assert.Len(t, Scan(b.String()), maxTokensPerBody)
}

func TestFormat(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  string
	}{
		{name: "address", email: "marcus.johnson@example.com", want: "@marcus.johnson(example.com)"},
		{name: "surrounding space trimmed", email: "  bob@example.com  ", want: "@bob(example.com)"},
		{name: "no at sign", email: "bob", want: ""},
		{name: "empty local part", email: "@example.com", want: ""},
		{name: "empty domain", email: "bob@", want: ""},
		{name: "two at signs", email: "bob@example.com@evil.example", want: ""},
		{name: "domain without a dot", email: "bob@localhost", want: ""},
		{name: "space in local part", email: "bob smith@example.com", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Format(tt.email))
		})
	}
}

// Format and Scan must agree: every token Format emits reads back as exactly
// the address it was built from, which is what lets the composer write tokens
// the write path will resolve.
func TestFormatRoundTripsThroughScan(t *testing.T) {
	for _, email := range []string{
		"bob@example.com",
		"marcus.johnson@example.com",
		"first+tag@mail.example.co.uk",
		"a_b%c@data-team.example.org",
	} {
		token := Format(email)
		require.NotEmpty(t, token, "expected a token for %s", email)
		assert.Equal(t, []string{email}, Emails(Scan("hi "+token+", thanks")))
	}
}
