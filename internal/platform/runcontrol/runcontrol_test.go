package runcontrol

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestOutcomeOfAndCancelMessage(t *testing.T) {
	assert.Equal(t, CanceledQueued, OutcomeOf(script.RunStatusPending))
	assert.Equal(t, CancelRequested, OutcomeOf(script.RunStatusRunning))
	assert.Equal(t, CancelAlreadyFinished, OutcomeOf(script.RunStatusSucceeded))
	assert.Contains(t, CancelMessage(script.RunStatusPending), "will not run")
	assert.Contains(t, CancelMessage(script.RunStatusRunning), "within seconds")
	assert.Equal(t, "The run had already finished (failed); nothing was changed.", CancelMessage(script.RunStatusFailed))
}

// sequence answers GetRun from a list of statuses, one per read.
type sequence struct {
	mu       sync.Mutex
	statuses []string
	err      error
}

func (s *sequence) GetRun(context.Context, string) (*script.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	status := s.statuses[0]
	if len(s.statuses) > 1 {
		s.statuses = s.statuses[1:]
	}
	return &script.Run{ID: "dpx_1", Status: status}, nil
}

func TestAwaitRun(t *testing.T) {
	ctx := context.Background()
	pending := &script.Run{ID: "dpx_1", Status: script.RunStatusPending}

	got, finished, err := AwaitRun(ctx, &sequence{statuses: []string{script.RunStatusRunning, script.RunStatusSucceeded}},
		pending, time.Second, time.Millisecond)
	require.NoError(t, err)
	assert.True(t, finished)
	assert.Equal(t, script.RunStatusSucceeded, got.Status)

	got, finished, err = AwaitRun(ctx, &sequence{statuses: []string{script.RunStatusRunning}}, pending, 20*time.Millisecond, time.Millisecond)
	require.NoError(t, err)
	assert.False(t, finished, "the budget ran out first")
	assert.Equal(t, script.RunStatusRunning, got.Status)

	_, finished, err = AwaitRun(ctx, &sequence{err: errors.New("db down")}, pending, time.Second, time.Millisecond)
	require.ErrorContains(t, err, "db down")
	assert.False(t, finished)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	got, finished, err = AwaitRun(canceled, &sequence{statuses: []string{script.RunStatusRunning}}, pending, time.Second, time.Hour)
	require.NoError(t, err)
	assert.False(t, finished)
	assert.Same(t, pending, got)

	done := &script.Run{Status: script.RunStatusCanceled}
	got, finished, err = AwaitRun(ctx, nil, done, 0, time.Millisecond)
	require.NoError(t, err)
	assert.True(t, finished, "a run already terminal needs no read")
	assert.Same(t, done, got)
}
