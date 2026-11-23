//go:build integration

package acceptance

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/tableparquet"
)

// Issue #2754: the daily fuzzing found a Parquet footer that panicked the
// footer reader. A schema element declaring a negative number of children
// panicked the Parquet library while it laid the columns out, so a file
// registered as a table could take the call down. The file the fuzzer wrote
// is registered through manage_table and refused as not Parquet, naming the
// element, and nothing is registered over it.
//
// Wire forms: manage_resource create takes the file as content_base64, the one
// form that carries these bytes, which are not UTF-8 text (content takes a
// string of text). manage_table register takes action, reference, connection
// and table_name, each as a literal string param.

// issue2754Input is the failing input the fuzzer committed, decoded from the
// corpus file go test replays.
func issue2754Input(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "tableparquet", "testdata", "fuzz", "FuzzColumns", "aaad4f2256a2749d"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(raw)), "\n", 2)
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "[]byte(") {
		t.Fatalf("the corpus file is not one []byte value: %q", raw)
	}
	body, err := strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(lines[1], "[]byte("), ")"))
	if err != nil {
		t.Fatalf("decoding the corpus file: %v", err)
	}
	return []byte(body)
}

// TestIssue2754_AParquetFooterDeclaringNegativeChildrenIsRefused is the
// criterion.
func TestIssue2754_AParquetFooterDeclaringNegativeChildrenIsRefused(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	ref := issue1833Upload(t, c, "acc-2754-"+stamp+".parquet", tableparquet.ContentType, issue2754Input(t), "content_base64")
	issue1833Refused(t, c, ref, "acc_2754_"+stamp, "not a Parquet file", "children")

	// The call that read the file answered, and the platform answers the next.
	list := c.call("manage_table", map[string]any{"action": "list", "reference": ref})
	if _, ok := list["table_registrations"]; !ok {
		t.Fatalf("manage_table list did not answer after the refusal: %v", list)
	}
}
