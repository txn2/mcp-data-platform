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

// TestDestination_NormalizedIsWhatKeepsADiffHonest: two approvals that meant
// the same place must read as the same place, or the next version's capability
// diff reports a widening that never happened.
func TestDestination_NormalizedIsWhatKeepsADiffHonest(t *testing.T) {
	typed := script.Destination{
		Name: " acme-drop ", Kind: " s3 ",
		Connection: " acme-s3", Bucket: "acme-exports ", Prefix: "/weekly/",
	}
	assert.Equal(t, script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}, typed.Normalized())
}

// TestDestination_ReadsTheNameOnlyFormAGrantRecordedBefore covers the stored
// shape a version approved before delivery existed carries. The portal was the
// only destination then, so the older form is unambiguous — and reading it is
// what lets one replica read a version another approved mid-upgrade.
func TestDestination_ReadsTheNameOnlyFormAGrantRecordedBefore(t *testing.T) {
	var grants script.Grants
	require.NoError(t, json.Unmarshal([]byte(`{"destinations":["portal"]}`), &grants))
	require.Len(t, grants.Destinations, 1)
	assert.Equal(t, script.PortalDestination(), grants.Destinations[0])
	assert.True(t, grants.AllowsDestination(script.DestinationPortal))

	// A name that never meant anything on its own is refused rather than
	// invented into an address.
	var d script.Destination
	require.Error(t, json.Unmarshal([]byte(`"acme-drop"`), &d))

	require.Error(t, json.Unmarshal([]byte(`42`), &d))
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

// TestValidateObjectKey is the boundary of a destination grant: a key that
// could climb out of the prefix it was granted under is refused, never cleaned
// up and written somewhere else.
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

// TestDestination_ValidateRefusesWhatCannotBeWritten covers the shapes an
// approval must not bind, each of which would otherwise become a run that
// cannot write where it was told to.
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

// TestGrants_DestinationNamesAreWhatAnErrorTells pins the list an author reads
// when their script names a destination the approval did not bind.
func TestGrants_DestinationNamesAreWhatAnErrorTells(t *testing.T) {
	g := script.Grants{Destinations: []script.Destination{
		script.PortalDestination(),
		{Name: "acme-drop", Kind: script.DestinationKindS3, Connection: "acme-s3", Bucket: "exports"},
	}}
	assert.Equal(t, []string{"portal", "acme-drop"}, g.DestinationNames())
	assert.Empty(t, script.Grants{}.DestinationNames())
}
