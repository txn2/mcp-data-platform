package script_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestDestination_PortalIsTheDefaultShape pins what a script writing no
// destination gets: the platform's own asset store, carrying no address.
func TestDestination_PortalIsTheDefaultShape(t *testing.T) {
	portal := script.PortalDestination()
	assert.Equal(t, script.DestinationPortal, portal.Name)
	assert.True(t, portal.IsPortal())
	assert.Equal(t, script.DestinationPortal, portal.Label())
	require.NoError(t, portal.Validate())
}

// TestDestination_LabelNamesTheAddress is what a reviewer and an error message
// read: a destination is a place, not a label, so both say where it is.
func TestDestination_LabelNamesTheAddress(t *testing.T) {
	d := script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}
	assert.False(t, d.IsPortal())
	assert.Equal(t, "acme-drop (s3 acme-s3 acme-exports/weekly)", d.Label())
	require.NoError(t, d.Validate())
}

// TestDestination_NormalizedIsWhatKeepsConfigurationHonest: two declarations
// that meant the same place must read as the same place.
func TestDestination_NormalizedIsWhatKeepsConfigurationHonest(t *testing.T) {
	typed := script.Destination{
		Name: " acme-drop ", Kind: " s3 ",
		Connection: " acme-s3", Bucket: "acme-exports ", Prefix: "/weekly/",
	}
	assert.Equal(t, script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}, typed.Normalized())
}

// TestDestination_RoundTripsAsAnAddress pins that what is written back is the
// full record, so a grant read after a write says where the output goes.
func TestDestination_RoundTripsAsAnAddress(t *testing.T) {
	original := script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var back script.Destination
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, original, back)
}

// TestValidateObjectKey is the boundary of a destination: a key that could
// climb out of the prefix it writes under is refused, never cleaned up and
// written somewhere else.
func TestValidateObjectKey(t *testing.T) {
	valid := []string{
		"sales.csv",
		"2026/08/sales.csv",
		"weekly/daily-sales.csv",
		"a.b_c-d/e.csv",
	}
	for _, key := range valid {
		t.Run("valid "+key, func(t *testing.T) {
			assert.NoError(t, script.ValidateObjectKey(key))
		})
	}

	invalid := map[string]string{
		"":                  "empty",
		"/sales.csv":        "relative",
		"../../etc/passwd":  "'.' or '..'",
		"weekly/../../out":  "'.' or '..'",
		"./sales.csv":       "'.' or '..'",
		`weekly\sales.csv`:  `cannot contain '\'`,
		"weekly//sales.csv": "empty path segment",
		"weekly/":           "empty path segment",
		"weekly/ sales.csv": "whitespace",
		"sales\x00.csv":     "control characters",
		"bad\xffutf8.csv":   "valid UTF-8",
	}
	for key, want := range invalid {
		t.Run("invalid "+key, func(t *testing.T) {
			err := script.ValidateObjectKey(key)
			require.Error(t, err)
			assert.Contains(t, err.Error(), want)
		})
	}

	t.Run("over the length limit", func(t *testing.T) {
		long := make([]byte, 1025)
		for i := range long {
			long[i] = 'a'
		}
		err := script.ValidateObjectKey(string(long))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "over the")
	})
}

// TestDestination_ValidateRefusesWhatCannotBeWritten covers the shapes a
// configuration must not declare, each of which would otherwise become a run
// that cannot write where it was told to.
func TestDestination_ValidateRefusesWhatCannotBeWritten(t *testing.T) {
	tests := map[string]struct {
		destination script.Destination
		wantErr     string
	}{
		"unnamed": {
			script.Destination{Kind: script.DestinationKindPortal}, "must be named",
		},
		"portal under another name": {
			script.Destination{Name: "assets", Kind: script.DestinationKindPortal},
			`must be named "portal"`,
		},
		"the portal carrying an address": {
			script.Destination{
				Name: "portal", Kind: script.DestinationKindPortal, Bucket: "exports",
			},
			"the platform owns where its own assets are stored",
		},
		"a bucket wearing the portal name": {
			script.Destination{
				Name: "portal", Kind: script.DestinationKindS3,
				Connection: "acme-s3", Bucket: "exports",
			},
			"reserved for one of the platform's own stores",
		},
		"prefix over the limit": {
			script.Destination{
				Name: "drop", Kind: script.DestinationKindS3,
				Connection: "acme-s3", Bucket: "exports",
				Prefix: strings.Repeat("a", 513),
			},
			"over the",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.destination.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestResourcesDestination_IsBuiltIn holds the managed-resource destination to the
// same shape the portal has: named by the platform, carrying no address, and
// refused in configuration.
func TestResourcesDestination_IsBuiltIn(t *testing.T) {
	d := script.ResourcesDestination()
	require.NoError(t, d.Validate())
	assert.Equal(t, script.DestinationResources, d.Name)
	assert.True(t, d.IsResource())
	assert.True(t, d.IsBuiltIn())
	assert.False(t, d.IsPortal())
	assert.Equal(t, script.DestinationResources, d.Label())
}

func TestResourcesDestination_RefusesWhatCannotBeWritten(t *testing.T) {
	tests := map[string]struct {
		destination script.Destination
		wantErr     string
	}{
		"under another name": {
			script.Destination{Name: "library", Kind: script.DestinationKindResource},
			`must be named "resources"`,
		},
		"carrying an address": {
			script.Destination{
				Name: script.DestinationResources, Kind: script.DestinationKindResource,
				Bucket: "exports",
			},
			"takes no connection, bucket, or prefix",
		},
		"a bucket wearing the library name": {
			script.Destination{
				Name: script.DestinationResources, Kind: script.DestinationKindS3,
				Connection: "acme-s3", Bucket: "exports",
			},
			"reserved for one of the platform's own stores",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.destination.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateDeclaredDestinations_RefusesBothBuiltIns(t *testing.T) {
	for _, d := range []script.Destination{script.PortalDestination(), script.ResourcesDestination()} {
		err := script.ValidateDeclaredDestinations([]script.Destination{d})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "built in and cannot be declared")
		assert.Contains(t, err.Error(), d.Name)
	}
}

// TestSplitLibraryKey reads the address a script writes to the library as the
// folder chain and the filename it is.
func TestSplitLibraryKey(t *testing.T) {
	path, filename, err := script.SplitLibraryKey("datasets/acme/orders.csv")
	require.NoError(t, err)
	assert.Equal(t, "datasets/acme", path)
	assert.Equal(t, "orders.csv", filename)

	path, filename, err = script.SplitLibraryKey("datasets/orders.csv")
	require.NoError(t, err)
	assert.Equal(t, "datasets", path)
	assert.Equal(t, "orders.csv", filename)
}

func TestSplitLibraryKey_RefusesWhatIsNotAnAddress(t *testing.T) {
	tests := map[string]string{
		"empty":              "",
		"a filename alone":   "orders.csv",
		"a leading slash":    "/datasets/orders.csv",
		"a trailing slash":   "datasets/orders.csv/",
		"an empty segment":   "datasets//orders.csv",
		"a relative segment": "datasets/../orders.csv",
		"a backslash":        `datasets\orders.csv`,
		"a control byte":     "datasets/orders\x00.csv",
	}
	for name, key := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := script.SplitLibraryKey(key)
			require.Error(t, err)
		})
	}
}
