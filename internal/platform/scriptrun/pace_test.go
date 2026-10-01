package scriptrun

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// refusingCaller answers every call with {"n": <attempt>} after refusing the
// attempts listed in refuse (1-based, counted across every call the run makes)
// with the refusal it is given. It records how many attempts it saw, which is
// how a test tells a paced retry from a call the script never made.
type refusingCaller struct {
	refuse   map[int]error
	attempts int
}

func (c *refusingCaller) CallTool(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	c.attempts++
	if err, ok := c.refuse[c.attempts]; ok {
		return nil, err
	}
	return map[string]any{"n": float64(c.attempts)}, nil
}

func rateLimited(after time.Duration) error {
	return &scriptsession.RefusalError{Code: toolratelimit.CodeRateLimited, RetryAfter: after, Text: "RATE_LIMITED: wait and retry"}
}

const loopSource = "for i in range(3):\n    print(platform.call(\"echo\", {\"i\": i})[\"n\"])\n"

// TestRun_PacesARateLimitedCall is the engine half of the #1533 acceptance: a
// call refused for timing is waited out and issued again, the script sees the
// admitted call's result, the run succeeds, the log names each wait, and the
// step count is the one an unlimited caller would have produced.
func TestRun_PacesARateLimitedCall(t *testing.T) {
	unlimited, err := execute(t, loopSource, &refusingCaller{}, nil)
	require.NoError(t, err)

	caller := &refusingCaller{refuse: map[int]error{1: rateLimited(5 * time.Millisecond), 4: rateLimited(5 * time.Millisecond)}}
	paced, err := execute(t, loopSource, caller, nil)
	require.NoError(t, err)

	assert.Equal(t, 5, caller.attempts, "three calls, two of them issued twice")
	assert.Equal(t, "rate limit: echo was refused; waited 5ms and retried\n2\n3\n"+
		"rate limit: echo was refused; waited 5ms and retried\n5\n", paced.Log,
		"the script prints the admitted result, and the host records each wait beside it")
	assert.Equal(t, unlimited.Steps, paced.Steps, "the interpreter does not advance while the host waits")
}

// TestRun_PacingIsBoundedByTheRunDeadline: a wait the deadline cuts short fails
// the run as a timeout, which the worker never re-queues, and never as a
// rate-limit backtrace.
func TestRun_PacingIsBoundedByTheRunDeadline(t *testing.T) {
	caller := &refusingCaller{refuse: map[int]error{1: rateLimited(5 * time.Second)}}
	result, err := Run(context.Background(), Options{
		Source: loopSource, Name: "test", FireTime: fireTime, Caller: caller,
		Timeout: 50 * time.Millisecond,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTimeout)
	assert.Contains(t, err.Error(), "waiting 5s to retry echo after a rate-limit refusal")
	assert.NotContains(t, err.Error(), "RATE_LIMITED")
	assert.Empty(t, result.Log, "a wait that did not complete is not recorded as one that did")
	assert.Equal(t, 1, caller.attempts)
}

// TestRun_OnlyARateLimitRefusalIsRetried: every other refusal, structured or
// not, fails the run exactly as it always has, on the first attempt.
func TestRun_OnlyARateLimitRefusalIsRetried(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"another structured refusal", &scriptsession.RefusalError{Code: middleware.CodeUnauthorized, RetryAfter: time.Millisecond, Text: "not permitted"}},
		{"a plain error", errors.New("not permitted")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caller := &refusingCaller{refuse: map[int]error{1: tc.err}}
			_, err := execute(t, loopSource, caller, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not permitted")
			assert.NotErrorIs(t, err, ErrTimeout)
			assert.Equal(t, 1, caller.attempts)
		})
	}
}

// TestRun_ARefusalNamingNoIntervalIsPacedAtTheFloor: the platform's limiter
// always names at least one second, so a refusal without one is another
// producer's, and it is waited a second rather than re-issued at once.
func TestRun_ARefusalNamingNoIntervalIsPacedAtTheFloor(t *testing.T) {
	caller := &refusingCaller{refuse: map[int]error{1: rateLimited(0)}}
	started := time.Now()
	result, err := execute(t, "print(platform.call(\"echo\")[\"n\"])\n", caller, nil)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(started), minPace)
	assert.Equal(t, "rate limit: echo was refused; waited 1s and retried\n2\n", result.Log)
}
