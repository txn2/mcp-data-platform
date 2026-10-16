package whlayout

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLayout(t *testing.T) {
	h := time.Date(2026, 9, 24, 7, 15, 0, 0, time.UTC)
	assert.Equal(t, "webhooks/esp/raw/", RawPrefix("esp"))
	assert.Equal(t, "webhooks/esp/compacted/", CompactedPrefix("esp"))
	assert.Equal(t, "webhooks/esp/raw/dt=2026-09-24/hour=07/minute=15/", WindowPrefix("esp", h))
	assert.Equal(t, "webhooks/esp/raw/dt=2026-09-24/hour=07/minute=15/w-0000000012.jsonl.gz", SegmentKey("esp", h, "w", 12))
	dt, hh, mm := PartitionValues(h.In(time.FixedZone("x", 3600)))
	assert.Equal(t, "2026-09-24", dt)
	assert.Equal(t, "07", hh, "partition values are UTC whatever zone the window carries")
	assert.Equal(t, "15", mm)
	assert.Equal(t, "platform-a-8080", ReplicaSlug("Platform-A:8080"))
	assert.Equal(t, "replica", ReplicaSlug("::"))
}

// TestRollupNames covers #2132: a rollup is named so the query engine reads
// it as a segment, its manifest so the engine skips it, and both carry the
// time the rollup was written.
func TestRollupNames(t *testing.T) {
	start := time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)
	at := start.Add(5*time.Minute + 123*time.Nanosecond)

	rollup := RollupKey("esp", start, at)
	assert.True(t, strings.HasPrefix(rollup, WindowPrefix("esp", start)))
	kind, when := Classify(rollup)
	assert.Equal(t, KindRollup, kind)
	assert.Equal(t, at, when)
	assert.True(t, HoldsEvents(rollup))

	manifest := ManifestKey("esp", start, at)
	name := manifest[strings.LastIndexByte(manifest, '/')+1:]
	assert.True(t, strings.HasPrefix(name, "."), "the engine skips a name beginning with a dot")
	kind, when = Classify(manifest)
	assert.Equal(t, KindManifest, kind)
	assert.Equal(t, at, when)
	assert.False(t, HoldsEvents(manifest))

	segment := SegmentKey("esp", start, "a", 7)
	kind, when = Classify(segment)
	assert.Equal(t, KindSegment, kind)
	assert.True(t, when.IsZero())

	kind, when = Classify(WindowPrefix("esp", start) + "rollup-notanumber.jsonl.gz")
	assert.Equal(t, KindRollup, kind)
	assert.True(t, when.IsZero(), "an unreadable stamp reads as the zero time")
}
