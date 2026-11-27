package sessionview

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/ftsquery"
)

// TestSearchQueryMatchesAnyWord holds the session search's spelled-out tsquery
// to the one rule the other searches build theirs with (#2838): every match in
// it is ftsquery.AnyWord, and none is the every-word form alone.
func TestSearchQueryMatchesAnyWord(t *testing.T) {
	anyWord := ftsquery.AnyWord("$2")
	if n := strings.Count(searchQuery, anyWord); n != 3 {
		t.Fatalf("searchQuery holds %q %d times, want 3 (two candidate arms and the score)", anyWord, n)
	}
	if strings.Count(searchQuery, ftsquery.AllWords("$2")) != 3 {
		t.Fatal("searchQuery holds an every-word tsquery outside ftsquery.AnyWord")
	}
}
