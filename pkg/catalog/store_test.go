package catalog

import (
	"testing"
	"time"
)

func TestConflictErrorNamesTheNewerWrite(t *testing.T) {
	err := &ConflictError{
		Change: "c1", Reference: "mcp:dataset:trino:w:c.s.t", Aspect: AspectTags,
		Newer: Change{ID: "c2", WrittenBy: "ben", WrittenAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)},
	}
	want := "the tags of mcp:dataset:trino:w:c.s.t was written again by ben at 2026-10-13T09:00:00Z (change c2) after change c1"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
