package script_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestTransfer_NormalizesTheNewOwner proves a transfer lands the address in the
// form every other identity in the platform is stored in, so a transfer to
// "Jane@Example.com" and a login as "jane@example.com" name one person rather
// than leaving the script unreachable for both.
func TestTransfer_NormalizesTheNewOwner(t *testing.T) {
	sc := &script.Script{Name: "daily", OwnerEmail: "bob@example.com"}

	require.NoError(t, sc.Transfer("  Jane@Example.COM "))

	assert.Equal(t, "jane@example.com", sc.OwnerEmail)
	assert.True(t, sc.OwnedBy("jane@example.com"))
	assert.False(t, sc.OwnedBy("bob@example.com"))
}

// TestTransfer_AdoptsAnOwnerlessScript proves the case the action exists for
// beyond hand-over: a script authored by a principal carrying no address
// belongs to nobody until an administrator gives it an owner.
func TestTransfer_AdoptsAnOwnerlessScript(t *testing.T) {
	sc := &script.Script{Name: "daily"}
	assert.False(t, sc.OwnedBy(""), "an ownerless script belongs to nobody")

	require.NoError(t, sc.Transfer("admin@example.com"))

	assert.True(t, sc.OwnedBy("admin@example.com"))
}

func TestTransfer_Refusals(t *testing.T) {
	cases := []struct {
		name    string
		sc      *script.Script
		to      string
		wantErr string
	}{
		{
			"no address", &script.Script{Name: "daily", OwnerEmail: "bob@example.com"}, "",
			"not a usable address",
		},
		{
			"not an address", &script.Script{Name: "daily", OwnerEmail: "bob@example.com"}, "jane at example",
			"not a usable address",
		},
		{
			"a display name is not a bare address",
			&script.Script{Name: "daily", OwnerEmail: "bob@example.com"}, "Jane <jane@example.com>",
			"not a usable address",
		},
		{
			// A no-op is refused rather than silently recorded: the store writes
			// a version for every transfer, and one that changed nothing would
			// put a hand-over in the history that never happened.
			"the owner it already has",
			&script.Script{Name: "daily", OwnerEmail: "jane@example.com"}, "JANE@example.com",
			"already belongs to",
		},
		{"no script", nil, "jane@example.com", "no script to transfer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := ""
			if tc.sc != nil {
				before = tc.sc.OwnerEmail
			}

			err := tc.sc.Transfer(tc.to)

			require.ErrorContains(t, err, tc.wantErr)
			if tc.sc != nil {
				assert.Equal(t, before, tc.sc.OwnerEmail, "a refused transfer changes nothing")
			}
		})
	}
}
