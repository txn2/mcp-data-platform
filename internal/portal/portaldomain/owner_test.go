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

func TestAssetOwner_IdentifiedAndArms(t *testing.T) {
	assert.False(t, portaldomain.AssetOwner{}.Identified())
	assert.False(t, portaldomain.NewAssetOwner(portaldomain.AnonymousOwner, "").Identified())
	assert.True(t, portaldomain.NewAssetOwner("u1", "").Identified())
	assert.True(t, portaldomain.NewAssetOwner("", "alice@example.com").Identified())

	assert.Empty(t, portaldomain.NewAssetOwner("u1", portaldomain.AnonymousOwner).Arms().Email,
		"the sentinel must never reach a query parameter")
	assert.Equal(t, "alice@example.com",
		portaldomain.NewAssetOwner("u1", " alice@example.com ").Arms().Email)
}

// A nil asset is owned by nobody: the callers that hold a pointer read it
// straight from a store that reports a miss as a nil record.
func TestAssetOwner_OwnsAssetNil(t *testing.T) {
	assert.False(t, portaldomain.NewAssetOwner("u1", "alice@example.com").OwnsAsset(nil))
}

// TestAssetOwner_ActingFor is the unattended caller's rule: the address of the
// person the run acts for, and only that. The principal such a caller presents
// is script:<name> and idx_scripts_name_owner makes a script name unique only
// within its OWNER, so two people who each keep a daily-sales present the same
// subject and matching it hands one person's automation the other's outputs
// (#1579).
func TestAssetOwner_ActingFor(t *testing.T) {
	run := portaldomain.NewAssetOwner("script:daily-sales", "alice@example.com").
		ActingFor("alice@example.com")

	assert.True(t, run.Owns("script:daily-sales", "alice@example.com"),
		"a run still owns what its own script wrote for the person it acts for")
	assert.True(t, run.Owns("u-alice", "alice@example.com"),
		"a run still owns what that person saved by hand")
	assert.False(t, run.Owns("script:daily-sales", "bob@example.com"),
		"another person's same-named script's output is not this run's")
	assert.False(t, run.Owns("script:daily-sales", ""),
		"the principal alone is not an identity for an unattended caller")

	// The person that run acts for reaches exactly the same rows, which is the
	// whole rule of the binding: a run reaches what its author reaches.
	person := portaldomain.NewAssetOwner("u-alice", "alice@example.com")
	for _, row := range [][2]string{
		{"script:daily-sales", "alice@example.com"},
		{"u-alice", "alice@example.com"},
		{"script:daily-sales", "bob@example.com"},
	} {
		assert.Equal(t, person.Owns(row[0], row[1]), run.Owns(row[0], row[1]),
			"a run must reach no row the person it acts for cannot: %v", row)
	}
}

// An empty address is a no-op, so a surface can pass whatever its context
// carries without first asking whether the caller is unattended.
func TestAssetOwner_ActingForEmptyAddressIsANoop(t *testing.T) {
	person := portaldomain.NewAssetOwner("u-alice", "alice@example.com")
	assert.Equal(t, person, person.ActingFor(""))
	assert.Equal(t, person, person.ActingFor("   "))
	assert.Equal(t, person, person.ActingFor(portaldomain.AnonymousOwner))
	assert.True(t, person.ActingFor("").Owns("u-alice", ""),
		"a person keeps the subject arm the no-op did not touch")
}

// A run's own INVENTORY is not an ownership question at all: it is the producer
// the platform recorded for the writes that made those rows. Neither identifier
// on the row names one script -- the owner id is the principal every same-named
// script shares, the address is the script owner's as of the insert and a
// transfer does not rewrite it -- so the producer is what an enumeration scopes
// by (#1579).
func TestContentProducer(t *testing.T) {
	p := portaldomain.NewContentProducer("script", "4f0b3e2a-1c9d-4e77-9a52-0f6c1d2b3a45")
	assert.True(t, p.Named())
	assert.Equal(t, "script", p.Kind)
	assert.Equal(t, "4f0b3e2a-1c9d-4e77-9a52-0f6c1d2b3a45", p.ID)

	assert.Equal(t, p, portaldomain.NewContentProducer("  script  ", "  4f0b3e2a-1c9d-4e77-9a52-0f6c1d2b3a45  "),
		"whitespace is not part of an identifier")
}

// A producer missing either half names nothing. An empty id with a kind would
// scope to every row written by that KIND, which is every script's outputs on
// the platform -- the very widening the producer scope exists to close.
func TestContentProducer_HalfAnIdentifierNamesNothing(t *testing.T) {
	for _, tt := range []struct{ name, kind, id string }{
		{"no id", "script", ""},
		{"no kind", "", "script-uuid"},
		{"neither", "", ""},
		{"whitespace id", "script", "   "},
		{"whitespace kind", "   ", "script-uuid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := portaldomain.NewContentProducer(tt.kind, tt.id)
			assert.False(t, p.Named())
			assert.Equal(t, portaldomain.ContentProducer{}, p)
		})
	}
}
