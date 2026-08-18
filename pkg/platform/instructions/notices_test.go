package instructions

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var allNoticeTools = []string{toolSearch, toolFetch, toolManageFeedback}

func TestNoticesNoteIsSilentWithNothingToRelay(t *testing.T) {
	assert.Empty(t, NoticesNote(allNoticeTools, 0, 0))
}

func TestNoticesNoteAddressesThePersonAndSaysItIsShownOnce(t *testing.T) {
	note := NoticesNote(allNoticeTools, 2, 1)

	assert.Contains(t, note, "`notices`")
	assert.Contains(t, note, "addressed to them, not to you")
	// Delivery advances the watermark, so an agent that keeps the digest to
	// itself has lost it. The note has to say so.
	assert.Contains(t, note, "not repeated to the next session")
	// Each list is capped and the watermark advances past what did not fit, so
	// the note must name the surface that holds the remainder.
	assert.Contains(t, note, "briefing, not an inbox")
	assert.Contains(t, note, "portal")
}

func TestNoticesNoteNamesOnlyTheHalvesThatHaveSomethingInThem(t *testing.T) {
	feedbackOnly := NoticesNote(allNoticeTools, 3, 0)
	assert.Contains(t, feedbackOnly, "notices.feedback")
	assert.NotContains(t, feedbackOnly, "notices.new_shares")

	sharesOnly := NoticesNote(allNoticeTools, 0, 3)
	assert.Contains(t, sharesOnly, "notices.new_shares")
	assert.NotContains(t, sharesOnly, "notices.feedback")
}

func TestNoticesNoteCountsReadAsProse(t *testing.T) {
	assert.Contains(t, NoticesNote(allNoticeTools, 1, 1), "`notices.feedback` — 1 thread")
	assert.Contains(t, NoticesNote(allNoticeTools, 1, 1), "`notices.new_shares` — 1 item")
	assert.Contains(t, NoticesNote(allNoticeTools, 4, 2), "`notices.feedback` — 4 threads")
	assert.Contains(t, NoticesNote(allNoticeTools, 4, 2), "`notices.new_shares` — 2 items")
}

// The note must never tell an agent to call a tool its persona cannot reach.
func TestNoticesNoteNamesOnlyReachableTools(t *testing.T) {
	tests := []struct {
		name    string
		tools   []string
		want    []string
		notWant []string
	}{
		{
			name:  "fetch and manage_feedback both reachable",
			tools: allNoticeTools,
			want:  []string{"`fetch`", "`manage_feedback`"},
		},
		{
			name:    "manage_feedback denied",
			tools:   []string{toolFetch},
			want:    []string{"`fetch`"},
			notWant: []string{"`manage_feedback`"},
		},
		{
			name:    "fetch denied",
			tools:   []string{toolManageFeedback},
			want:    []string{"`manage_feedback`"},
			notWant: []string{"`fetch`"},
		},
		{
			name:    "neither reachable falls back to the portal",
			tools:   []string{toolSearch},
			want:    []string{"Point them at the portal to answer it."},
			notWant: []string{"`fetch`", "`manage_feedback`"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			note := NoticesNote(tt.tools, 2, 2)
			require.NotEmpty(t, note)
			for _, want := range tt.want {
				assert.Contains(t, note, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, note, notWant)
			}
		})
	}
}

// The note is composed onto the instruction stack like the other runtime notes,
// so it must survive Compose without swallowing the baseline.
func TestNoticesNoteComposesBeneathTheBaseline(t *testing.T) {
	baseline := Build(allNoticeTools)
	out := Compose(baseline, NoticesNote(allNoticeTools, 1, 0))
	assert.True(t, strings.HasPrefix(out, "How to operate this platform:"))
	assert.Contains(t, out, "notices.feedback")
}
