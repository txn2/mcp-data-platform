package scriptdest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestResolve(t *testing.T) {
	declared := []script.Destination{{Name: "acme-drop", Connection: "s3", Bucket: "acme"}}

	portal, err := Resolve(script.DestinationPortal, declared)
	require.NoError(t, err)
	assert.True(t, portal.IsPortal())

	library, err := Resolve(script.DestinationResources, nil)
	require.NoError(t, err)
	assert.True(t, library.IsResource())

	drop, err := Resolve("acme-drop", declared)
	require.NoError(t, err)
	assert.Equal(t, "acme", drop.Bucket)

	_, err = Resolve("elsewhere", declared)
	require.ErrorContains(t, err, "this deployment declares acme-drop")

	_, err = Resolve("elsewhere", nil)
	require.ErrorContains(t, err, "declares no bucket destinations")
}
