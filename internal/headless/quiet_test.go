package headless

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func quietRender(quiet, most time.Duration) *render {
	return &render{page: Page{Quiet: quiet, QuietMax: most}, changed: make(chan struct{}, 1)}
}

// TestAwaitQuiet_WaitsForTheLastAnswer: the wait does not end while a request
// is outstanding, and ends once none has been for Quiet.
func TestAwaitQuiet_WaitsForTheLastAnswer(t *testing.T) {
	rs := quietRender(5*time.Millisecond, time.Minute)
	done := rs.track()
	returned := make(chan struct{})
	go func() {
		rs.awaitQuiet(context.Background())
		close(returned)
	}()
	select {
	case <-returned:
		t.Fatal("the wait ended with a request outstanding")
	case <-time.After(20 * time.Millisecond):
	}
	done()
	<-returned
	_, quiet := rs.quietIn()
	assert.True(t, quiet)
}

// TestAwaitQuiet_StopsAtTheLimitAndTheContext: a request never answered
// holds the wait only until QuietMax, or until the caller gives up.
func TestAwaitQuiet_StopsAtTheLimitAndTheContext(t *testing.T) {
	rs := quietRender(time.Millisecond, 10*time.Millisecond)
	_ = rs.track()
	rs.awaitQuiet(context.Background())
	wait, quiet := rs.quietIn()
	assert.False(t, quiet)
	assert.Equal(t, time.Millisecond, wait, "an outstanding request waits the whole quiet period")

	rs = quietRender(time.Millisecond, time.Minute)
	_ = rs.track()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rs.awaitQuiet(ctx)
}

// TestQuietIn_CountsTheRestOfTheQuiet: with nothing outstanding the wait is
// what is left of Quiet since the last answer.
func TestQuietIn_CountsTheRestOfTheQuiet(t *testing.T) {
	rs := quietRender(time.Hour, time.Hour)
	rs.lastSeen = time.Now()
	wait, quiet := rs.quietIn()
	assert.False(t, quiet)
	assert.Greater(t, wait, 59*time.Minute)
	rs.lastSeen = time.Now().Add(-2 * time.Hour)
	_, quiet = rs.quietIn()
	assert.True(t, quiet)
}
