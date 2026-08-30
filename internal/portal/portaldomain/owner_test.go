package portaldomain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// TestAssetOwner_Owns is the whole ownership rule in one table: either
// identifier matches, both sides of an arm must name somebody, and the
// anonymous sentinel names nobody on either side.
func TestAssetOwner_Owns(t *testing.T) {
	tests := []struct {
		name                  string
		callerID, callerEmail string
		ownerID, ownerEmail   string
		want                  bool
	}{
		{
			name:     "the id matches",
			callerID: "u1", callerEmail: "alice@example.com",
			ownerID: "u1", ownerEmail: "alice@example.com", want: true,
		},
		{
			name:     "a script output matches on the address alone",
			callerID: "u1", callerEmail: "alice@example.com",
			ownerID: "script:weekly-revenue", ownerEmail: "alice@example.com", want: true,
		},
		{
			name:     "the address comparison is case-folded",
			callerID: "u1", callerEmail: "Alice@Example.COM",
			ownerID: "script:weekly-revenue", ownerEmail: "alice@example.com", want: true,
		},
		{
			name:     "somebody else's script output does not match",
			callerID: "u2", callerEmail: "bob@example.com",
			ownerID: "script:weekly-revenue", ownerEmail: "alice@example.com", want: false,
		},
		{
			name:    "an unattributed row is not owned by an unidentified caller",
			ownerID: "", ownerEmail: "", want: false,
		},
		{
			name:     "an empty address does not match an empty owner address",
			callerID: "u1", callerEmail: "",
			ownerID: "u2", ownerEmail: "", want: false,
		},
		{
			name:     "the anonymous sentinel is not an identity on the caller's side",
			callerID: portaldomain.AnonymousOwner, callerEmail: portaldomain.AnonymousOwner,
			ownerID: "u1", ownerEmail: "alice@example.com", want: false,
		},
		{
			name:     "the anonymous sentinel is not an identity on the row's side",
			callerID: "u1", callerEmail: "alice@example.com",
			ownerID: portaldomain.AnonymousOwner, ownerEmail: portaldomain.AnonymousOwner, want: false,
		},
		{
			name:     "two anonymous callers are not the same person",
			callerID: portaldomain.AnonymousOwner, callerEmail: portaldomain.AnonymousOwner,
			ownerID: portaldomain.AnonymousOwner, ownerEmail: portaldomain.AnonymousOwner, want: false,
		},
		{
			name:     "whitespace is not an identity",
			callerID: "  ", callerEmail: "  ",
			ownerID: "  ", ownerEmail: "  ", want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner := portaldomain.NewAssetOwner(tt.callerID, tt.callerEmail)
			assert.Equal(t, tt.want, owner.Owns(tt.ownerID, tt.ownerEmail))
			assert.Equal(t, tt.want, owner.OwnsAsset(&portaldomain.Asset{
				OwnerID: tt.ownerID, OwnerEmail: tt.ownerEmail,
			}))
		})
	}
}

func TestAssetOwner_IdentifiedAndEmailKey(t *testing.T) {
	assert.False(t, portaldomain.AssetOwner{}.Identified())
	assert.False(t, portaldomain.NewAssetOwner(portaldomain.AnonymousOwner, "").Identified())
	assert.True(t, portaldomain.NewAssetOwner("u1", "").Identified())
	assert.True(t, portaldomain.NewAssetOwner("", "alice@example.com").Identified())

	assert.Empty(t, portaldomain.NewAssetOwner("u1", portaldomain.AnonymousOwner).EmailKey(),
		"the sentinel must never reach a query parameter")
	assert.Equal(t, "alice@example.com", portaldomain.NewAssetOwner("u1", " alice@example.com ").EmailKey())
}

// A nil asset is owned by nobody: the callers that hold a pointer read it
// straight from a store that reports a miss as a nil record.
func TestAssetOwner_OwnsAssetNil(t *testing.T) {
	assert.False(t, portaldomain.NewAssetOwner("u1", "alice@example.com").OwnsAsset(nil))
}
