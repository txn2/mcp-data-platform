package knowledgebuiltin

import (
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// Placeholders the content-types page receives its tables through, so the
// tables come from the code that decides them rather than from a transcription
// that goes stale (#1508).
const (
	textTypesPlaceholder   = "{{TEXT_CONTENT_TYPES}}"
	binaryTypesPlaceholder = "{{BINARY_CONTENT_TYPES}}"
)

// contentTypeTable renders half of contenttype.Catalog as a markdown table:
// storable true gives the families a caller may declare when the content
// travels as a string, storable false the rest, which reach the platform as
// bytes (a base64 tool argument or an upload).
func contentTypeTable(storable bool) string {
	var b strings.Builder
	b.WriteString("| Media type | Extension |\n|---|---|\n")
	for _, f := range contenttype.Catalog() {
		if f.Storable != storable {
			continue
		}
		b.WriteString("| `" + f.Type + "` | `" + f.Extension + "` |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
