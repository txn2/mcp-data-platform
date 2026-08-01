package contenttype_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

func TestIsStorableText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ct   string
		want bool
	}{
		{name: "markdown", ct: "text/markdown", want: true},
		{name: "html", ct: "text/html", want: true},
		{name: "svg", ct: "image/svg+xml", want: true},
		{name: "octet-stream is generic, not binary content", ct: "application/octet-stream", want: true},
		{name: "alias resolves to a member", ct: "text/json", want: true},
		{name: "parameters are stripped", ct: "text/csv; charset=utf-8", want: true},
		{name: "case is normalized", ct: "TEXT/Markdown", want: true},
		{name: "xhtml is refused", ct: "application/xhtml+xml", want: false},
		{name: "pdf cannot travel as a string", ct: "application/pdf", want: false},
		{name: "png cannot travel as a string", ct: "image/png", want: false},
		{name: "an invented text subtype is not admitted by a prefix", ct: "text/x-shellscript", want: false},
		{name: "a vendor json type is not text-storable", ct: "application/vnd.acme.report+json", want: false},
		{name: "an xml dialect is refused", ct: "application/vnd.acme.feed+xml", want: false},
		{name: "empty", ct: "", want: false},
		{name: "not a media type", ct: "definitely not a type", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, contenttype.IsStorableText(tt.ct))
		})
	}
}

// TestStorableTextTypesListsWhatIsStorableText checks the accessor a rejected
// write quotes back to its caller: an entry that is not itself accepted would
// send the caller to retry with a type that fails again.
func TestStorableTextTypesListsWhatIsStorableText(t *testing.T) {
	t.Parallel()

	list := contenttype.StorableTextTypes()
	require.NotEmpty(t, list)
	assert.True(t, slices.IsSorted(list), "the list is quoted to callers and must be stable")
	for _, ct := range list {
		assert.True(t, contenttype.IsStorableText(ct), "%q is listed but not accepted", ct)
	}
	assert.Contains(t, list, contenttype.Markdown)
	assert.NotContains(t, list, contenttype.XHTML)
}
