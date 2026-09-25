package whlayout

import (
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
