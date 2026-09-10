package contenttype_test

import (
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// A stored Office document keeps its extension in the bucket. Before #1657
// every one of these landed as .bin, so an object downloaded straight from
// storage did not open in the application that wrote it.
func TestExtensionNamesTheOfficeFamilies(t *testing.T) {
	cases := map[string]string{
		contenttype.DOCX: ".docx",
		contenttype.XLSX: ".xlsx",
		contenttype.PPTX: ".pptx",
		contenttype.ODT:  ".odt",
		contenttype.ODS:  ".ods",
		contenttype.ODP:  ".odp",
	}
	for ct, want := range cases {
		if got := contenttype.Extension(ct); got != want {
			t.Errorf("Extension(%s) = %q, want %q", ct, got, want)
		}
		if got := contenttype.TypeForFilename("deck" + want); got != ct {
			t.Errorf("TypeForFilename(%q) = %q, want %q", want, got, ct)
		}
	}
}
