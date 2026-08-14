package script_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fullGrant is a grant that permits everything the platform implements.
func fullGrant() script.Grants {
	return script.Grants{
		Roles:        []string{"analyst"},
		Connections:  []string{"warehouse"},
		Capabilities: script.Capabilities,
		Destinations: []script.Destination{script.PortalDestination()},
	}
}

// TestGrants_AllowsNothingByDefault pins the deny-by-default shape: a grant
// nobody filled in permits nothing, which is what makes "approved with nothing
// granted" a safe state rather than an open one.
func TestGrants_AllowsNothingByDefault(t *testing.T) {
	var g script.Grants
	assert.False(t, g.AllowsCapability(script.CapabilityQuery))
	assert.False(t, g.AllowsCapability(script.CapabilityExport))
	assert.False(t, g.AllowsConnection("warehouse"))
	assert.False(t, g.AllowsDestination(script.DestinationPortal))
	assert.True(t, g.IsZero())
}

// TestGrants_UnnamedConnectionIsNeverAllowed pins the rule that keeps a grant
// checkable: the platform would resolve an empty connection to its default,
// which is a connection no approval named.
func TestGrants_UnnamedConnectionIsNeverAllowed(t *testing.T) {
	g := fullGrant()
	assert.True(t, g.AllowsConnection("warehouse"))
	assert.False(t, g.AllowsConnection(""), "an unnamed connection cannot be checked against a grant")
	assert.False(t, g.AllowsConnection("*"), "grants carry no wildcards")
}

func TestGrants_IsZero(t *testing.T) {
	assert.True(t, script.Grants{}.IsZero())
	assert.False(t, script.Grants{Roles: []string{"analyst"}}.IsZero(),
		"a grant carrying authority and nothing else is a deliberate state, not an absent one")
	assert.False(t, fullGrant().IsZero())
}

func TestGrants_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*script.Grants)
		wantErr string
	}{
		{"full grant", func(*script.Grants) {}, ""},
		{"no roles", func(g *script.Grants) { g.Roles = nil }, "held no roles"},
		{"unknown capability", func(g *script.Grants) {
			g.Capabilities = []string{"platform.delete_everything"}
		}, "unknown capability"},
		{"unknown destination kind", func(g *script.Grants) {
			g.Destinations = []script.Destination{{Name: "elsewhere", Kind: "ftp"}}
		}, "unknown kind"},
		{"bucket destination with no connection", func(g *script.Grants) {
			g.Destinations = []script.Destination{{Name: "drop", Kind: script.DestinationKindS3, Bucket: "exports"}}
		}, "must name the platform connection"},
		{"bucket destination with no bucket", func(g *script.Grants) {
			g.Destinations = []script.Destination{{Name: "drop", Kind: script.DestinationKindS3, Connection: "acme-s3"}}
		}, "must name a bucket"},
		{"portal destination carrying an address", func(g *script.Grants) {
			g.Destinations = []script.Destination{{
				Name: script.DestinationPortal, Kind: script.DestinationKindPortal, Bucket: "exports",
			}}
		}, "takes no connection, bucket, or prefix"},
		{"destination granted twice", func(g *script.Grants) {
			g.Destinations = []script.Destination{script.PortalDestination(), script.PortalDestination()}
		}, "granted twice"},
		{"destination prefix climbing out of itself", func(g *script.Grants) {
			g.Destinations = []script.Destination{{
				Name: "drop", Kind: script.DestinationKindS3,
				Connection: "acme-s3", Bucket: "exports", Prefix: "weekly/../../etc",
			}}
		}, "unusable prefix"},
		{"blank connection", func(g *script.Grants) { g.Connections = []string{""} }, "cannot be blank"},
		{"nothing granted but authority", func(g *script.Grants) {
			g.Capabilities, g.Connections, g.Destinations = nil, nil, nil
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := fullGrant()
			tt.mutate(&g)
			err := g.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestGrants_MissingFor is the referenced-versus-granted diff a reviewer reads
// and the approval action refuses on.
func TestGrants_MissingFor(t *testing.T) {
	g := script.Grants{
		Roles:        []string{"analyst"},
		Connections:  []string{"warehouse"},
		Capabilities: []string{script.CapabilityQuery},
	}

	missing := g.MissingFor(
		[]string{script.CapabilityQuery, script.CapabilityExport},
		[]string{"warehouse", "finance"},
		[]string{script.DestinationPortal, "acme-drop"})
	assert.Equal(t, []string{script.CapabilityExport}, missing.Capabilities)
	assert.Equal(t, []string{"finance"}, missing.Connections)
	assert.Equal(t, []string{script.DestinationPortal, "acme-drop"}, missing.Destinations)
	assert.True(t, missing.Any())

	missing = g.MissingFor([]string{script.CapabilityQuery}, []string{"warehouse"}, nil)
	assert.Empty(t, missing.Capabilities)
	assert.Empty(t, missing.Connections)
	assert.Empty(t, missing.Destinations)
	assert.False(t, missing.Any())

	// A granted destination is matched by the name a script writes, whatever
	// address that name resolves to.
	granted := script.Grants{Destinations: []script.Destination{{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "exports", Prefix: "weekly",
	}}}
	assert.Empty(t, granted.MissingFor(nil, nil, []string{"acme-drop"}).Destinations)

	// An unapproved version has no grant at all, so everything its code reaches
	// for is missing — which is exactly the grant a reviewer would have to bind.
	missing = script.Grants{}.MissingFor([]string{script.CapabilityQuery}, []string{"warehouse"}, []string{script.DestinationPortal})
	assert.Equal(t, []string{script.CapabilityQuery}, missing.Capabilities)
	assert.Equal(t, []string{"warehouse"}, missing.Connections)
	assert.Equal(t, []string{script.DestinationPortal}, missing.Destinations)
}
