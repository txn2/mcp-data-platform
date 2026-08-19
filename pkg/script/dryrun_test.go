package script_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The account of a draft execution is keyed by the SOURCE that ran, which is
// what lets it attach to whichever version later carries that exact code — in
// either order, and to no other version (#1364).

func TestSourceDigest_IsStableAndDistinguishing(t *testing.T) {
	const source = "platform.query(connection=\"warehouse\", sql=\"SELECT 1\")\n"

	assert.Equal(t, script.SourceDigest(source), script.SourceDigest(source),
		"the same source must resolve to the same account")
	assert.NotEqual(t, script.SourceDigest(source), script.SourceDigest(source+"\n"),
		"a whitespace change is a different version, and must not inherit its account")
	assert.Len(t, script.SourceDigest(""), 32, "a raw SHA-256, which is why the column is BYTEA")
}

func TestDryRun_Succeeded(t *testing.T) {
	assert.True(t, (&script.DryRun{Status: script.RunStatusSucceeded}).Succeeded())
	assert.False(t, (&script.DryRun{Status: script.RunStatusFailed}).Succeeded())
	var none *script.DryRun
	assert.False(t, none.Succeeded(), "no account is not a successful one")
}
