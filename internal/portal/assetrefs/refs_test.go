package assetrefs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

// TestTargetKindValid pins which kinds this build can resolve. A row read back
// carrying anything else is data from a future version, and the serving route
// turns that answer into a missing target rather than a guess.
func TestTargetKindValid(t *testing.T) {
	for kind, want := range map[assetrefs.TargetKind]bool{
		assetrefs.TargetResource: true,
		assetrefs.TargetAsset:    true,
		"":                       false,
		"collection":             false,
		"Resource":               false,
	} {
		assert.Equal(t, want, kind.Valid(), "kind %q", kind)
	}
}

// TestAssetURI pins the form an asset is referenced by. It is built from the
// platform's one reference vocabulary rather than formatted here, so the string
// a picker records and the string a search hit hands an agent are the same
// string, and an author can paste one straight into their markup.
func TestAssetURI(t *testing.T) {
	assert.Equal(t, "mcp:asset:ast_7c1e", assetrefs.AssetURI("ast_7c1e"))
}
