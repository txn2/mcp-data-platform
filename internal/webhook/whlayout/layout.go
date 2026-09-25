// Package whlayout is where a webhook source's objects are kept in the
// managed-resources bucket (#1870): the raw segments in the Hive partition
// layout the raw table reads, and the names a replica gives the segments it
// writes. The receiver writes by it, the compactor and retention read by it,
// and the tables name its prefixes as their locations.
package whlayout

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SourcePrefix is where everything a source writes outside the resource store
// lives, in the managed-resources bucket.
func SourcePrefix(source string) string { return "webhooks/" + source + "/" }

// RawPrefix is the directory the raw table's external location names.
func RawPrefix(source string) string { return SourcePrefix(source) + "raw/" }

// CompactedPrefix is the compacted table's external location. Nothing is
// written under it: every compacted window is registered at the directory of
// the resource that holds it, so this is only the table's nominal home.
func CompactedPrefix(source string) string { return SourcePrefix(source) + "compacted/" }

// PartitionValues are the dt, hour and minute values a window is filed under:
// the UTC date, hour and minute it starts at. A source compacting by the hour
// files every window under minute 00.
func PartitionValues(start time.Time) (dt, hh, mm string) {
	s := start.UTC()
	return s.Format("2006-01-02"), fmt.Sprintf("%02d", s.Hour()), fmt.Sprintf("%02d", s.Minute())
}

// WindowPrefix is the raw directory of one window: the Hive partition layout
// the raw table's partitions are found by.
func WindowPrefix(source string, start time.Time) string {
	dt, hh, mm := PartitionValues(start)
	return RawPrefix(source) + "dt=" + dt + "/hour=" + hh + "/minute=" + mm + "/"
}

// replicaUnsafe matches what a replica name cannot carry into an object key.
var replicaUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// ReplicaSlug renders a replica identity (host:port) as an object-key segment.
func ReplicaSlug(replica string) string {
	s := strings.Trim(replicaUnsafe.ReplaceAllString(strings.ToLower(replica), "-"), "-")
	if s == "" {
		return "replica"
	}
	return s
}

// SegmentKey is where one segment is written. writer identifies the process
// (its replica and start time) and seq is its counter, so two replicas, or one
// replica before and after a restart, never write the same key.
func SegmentKey(source string, start time.Time, writer string, seq uint64) string {
	return fmt.Sprintf("%s%s-%010d.jsonl.gz", WindowPrefix(source, start), writer, seq)
}
