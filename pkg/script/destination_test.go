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
		"a bucket wearing the portal name": {
			script.Destination{
				Name: "portal", Kind: script.DestinationKindS3,
				Connection: "acme-s3", Bucket: "exports",
			},
			"reserved for the platform's own asset store",
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
