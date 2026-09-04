package portaldomain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A capture is appended on every content write and nothing bounded them, so an
// asset a scheduled script refreshes hourly carried hundreds and the reads that
// carried it stopped working (#1623). What is asserted here is the bound a read
// applies: the newest captures, in the order they were written, and the count
// of what the asset actually holds.

func captures(versions ...int) []ProvenanceCapture {
	out := make([]ProvenanceCapture, 0, len(versions))
	for _, v := range versions {
		out = append(out, ProvenanceCapture{
			Tool: "manage_asset", Version: v, CapturedAt: time.Unix(int64(v), 0).UTC(),
		})
	}
	return out
}

func versionsOf(cs []ProvenanceCapture) []int {
	out := make([]int, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Version)
	}
	return out
}

func TestBoundedProvenance_KeepsTheNewestInWriteOrder(t *testing.T) {
	p := BoundedProvenance(Provenance{Captures: captures(1, 2, 3, 4, 5)}, 2)

	assert.Equal(t, []int{4, 5}, versionsOf(p.Captures),
		"the newest two, still oldest-first, so a reader taking the last as the newest is unaffected")
	assert.Equal(t, 5, p.CapturesTotal)
}

// An asset holding no more than the bound reads exactly as it did before the
// bound existed: every capture, and no total to explain a truncation that did
// not happen.
func TestBoundedProvenance_LeavesAnAssetWithinTheBoundAlone(t *testing.T) {
	for _, n := range []int{1, 2, 20} {
		p := BoundedProvenance(Provenance{Captures: captures(1)}, n)
		require.Len(t, p.Captures, 1)
		assert.Zero(t, p.CapturesTotal, "nothing was cut, so nothing is reported cut")
	}
}

func TestBoundedProvenance_ZeroKeepsNoneAndStillReportsTheTotal(t *testing.T) {
	p := BoundedProvenance(Provenance{Captures: captures(1, 2, 3)}, 0)

	assert.Empty(t, p.Captures)
	assert.Equal(t, 3, p.CapturesTotal)
}

// A negative bound is a caller's arithmetic gone wrong, not an instruction to
// return the whole history the bound exists to withhold.
func TestBoundedProvenance_NegativeKeepsNone(t *testing.T) {
	p := BoundedProvenance(Provenance{Captures: captures(1, 2, 3)}, -5)

	assert.Empty(t, p.Captures)
	assert.Equal(t, 3, p.CapturesTotal)
}

// The legacy shape and the session it came from ride through untouched: what is
// bounded is the list that grows.
func TestBoundedProvenance_CarriesEverythingElseThrough(t *testing.T) {
	p := BoundedProvenance(Provenance{
		Captures:  captures(1, 2, 3),
		ToolCalls: []ProvenanceToolCall{{ToolName: "trino_query"}},
		SessionID: "dps_abc",
		UserID:    "u1",
	}, 1)

	assert.Equal(t, []int{3}, versionsOf(p.Captures))
	assert.Len(t, p.ToolCalls, 1)
	assert.Equal(t, "dps_abc", p.SessionID)
	assert.Equal(t, "u1", p.UserID)
}

func TestClampProvenancePage(t *testing.T) {
	tests := []struct {
		name                  string
		offset, limit         int
		wantOffset, wantLimit int
	}{
		{"defaults", 0, 0, 0, DefaultProvenancePageSize},
		{"asked for", 40, 5, 40, 5},
		{"negative offset is the newest page", -3, 10, 0, 10},
		{"negative limit is the default", 0, -1, 0, DefaultProvenancePageSize},
		{"past the maximum", 0, 5000, 0, MaxProvenancePageSize},
		{"at the maximum", 0, MaxProvenancePageSize, 0, MaxProvenancePageSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset, limit := ClampProvenancePage(tt.offset, tt.limit)
			assert.Equal(t, tt.wantOffset, offset)
			assert.Equal(t, tt.wantLimit, limit)
		})
	}
}
