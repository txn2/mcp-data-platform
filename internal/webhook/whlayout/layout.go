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

// rollupPrefix and manifestPrefix name the objects a rollup writes into a
// window's directory (#2132): the merged segments, which the raw table reads
// like any segment, and the list of what was merged, which it must not, so
// its name begins with "." and the query engine skips it.
const (
	rollupPrefix   = "rollup-"
	manifestPrefix = ".rollup-"
	manifestSuffix = ".keys"
)

// RollupKey is where a rollup of a window written at at is stored.
func RollupKey(source string, start, at time.Time) string {
	return fmt.Sprintf("%s%s%019d.jsonl.gz", WindowPrefix(source, start), rollupPrefix, at.UnixNano())
}

// ManifestKey is where the list of what a rollup written at at merged is
// stored, beside it.
func ManifestKey(source string, start, at time.Time) string {
	return fmt.Sprintf("%s%s%019d%s", WindowPrefix(source, start), manifestPrefix, at.UnixNano(), manifestSuffix)
}

// ObjectKind is what an object in a window's directory is.
type ObjectKind int

// The objects a window's directory holds.
const (
	// KindSegment is a segment a receiver wrote.
	KindSegment ObjectKind = iota
	// KindRollup is segments merged into one object.
	KindRollup
	// KindManifest lists what a rollup merged; it holds no events.
	KindManifest
)

// Classify reports what an object in a window's directory is and, for a
// rollup or its manifest, when the rollup was written.
func Classify(key string) (ObjectKind, time.Time) {
	name := key[strings.LastIndexByte(key, '/')+1:]
	switch {
	case strings.HasPrefix(name, manifestPrefix) && strings.HasSuffix(name, manifestSuffix):
		return KindManifest, stampOf(strings.TrimSuffix(strings.TrimPrefix(name, manifestPrefix), manifestSuffix))
	case strings.HasPrefix(name, rollupPrefix):
		return KindRollup, stampOf(strings.TrimSuffix(strings.TrimPrefix(name, rollupPrefix), ".jsonl.gz"))
	}
	return KindSegment, time.Time{}
}

// HoldsEvents reports whether an object in a window's directory holds events:
// a segment or a rollup, not a manifest.
func HoldsEvents(key string) bool {
	kind, _ := Classify(key)
	return kind != KindManifest
}

// stampOf reads the nanosecond stamp a rollup's name carries.
func stampOf(digits string) time.Time {
	var n int64
	if _, err := fmt.Sscan(digits, &n); err != nil {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}
