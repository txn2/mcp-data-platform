package whevent

import (
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// fuzzBound is the most one sender's body of up to 64 KiB may take to become
// events: the receiver answers only after the events are written, and a body
// that takes far longer is a way to hold its replica (#2713).
const fuzzBound = 500 * time.Millisecond

// FuzzBuild holds what an unauthenticated sender's JSON becomes (#2713):
// building events stays within the bound, and the events written to a segment
// read back as the same number of events.
func FuzzBuild(f *testing.F) {
	for _, s := range []string{
		`{"id":"evt_1","type":"order.created","data":{"order":7}}`,
		`{"events":[{"id":"a","type":"x"},{"id":"b","type":"y"}]}`,
		`[1,2,3]`, `{"events":"not a list"}`, `{`, `null`,
	} {
		f.Add(s)
	}
	paths, err := ParsePaths(whsource.Config{Split: "$.events[*]", EventIDPath: "$.id", EventTypePath: "$.type", KeyPath: "$.data.order"})
	if err != nil {
		f.Fatal(err)
	}
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 1<<16 {
			return
		}
		start := time.Now()
		events, err := Build([]byte(body), paths, at, "replica-a")
		if d := time.Since(start); d > fuzzBound {
			t.Fatalf("a %d-byte body took %s to become events", len(body), d)
		}
		if err != nil || len(events) == 0 {
			return
		}
		seg, err := EncodeSegment(events, at)
		if err != nil {
			t.Fatalf("events built from %q do not encode: %v", body, err)
		}
		stored, err := DecodeSegment(seg)
		if err != nil || len(stored) != len(events) {
			t.Fatalf("a segment of %d events read back %d: %v", len(events), len(stored), err)
		}
	})
}
