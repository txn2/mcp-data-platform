package scriptexec

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakePoster records what a failed run's alert posted to channels.
type fakePoster struct {
	keys     []string
	payloads []notification.Payload
	err      error
}

func (f *fakePoster) Post(_ context.Context, key string, p notification.Payload) (int, error) {
	f.keys = append(f.keys, key)
	f.payloads = append(f.payloads, p)
	return 1, f.err
}

func TestNotifyFailure_PostsToTheChannelsUnderTheScriptsKeyWithTheRunsLink(t *testing.T) {
	f := failedScheduledRun()
	n, p := &fakeNotifier{}, &fakePoster{}
	w := newWorker(workerConfig{runs: &fakeRuns{}, notifier: n, channels: p, portalURL: "https://portal.example.com/"})

	w.notifyFailure(context.Background(), f.run, f.script, f.result)

	link := "https://portal.example.com/portal/automations/" + f.script.ID + "/runs/" + f.run.ID
	require.Len(t, p.keys, 1)
	assert.Equal(t, "script:"+f.script.ID, p.keys[0])
	assert.Equal(t, link, p.payloads[0].Link, "the post leads to the run")
	require.Len(t, n.payloads, 1, "the owner is still emailed")
	assert.Equal(t, link, n.payloads[0].Link, "and the email leads to the run too")
}

func TestNotifyFailure_ChannelsWithoutAnOwnerOrANotifier(t *testing.T) {
	f := failedScheduledRun()
	f.script.OwnerEmail = ""
	p := &fakePoster{err: errors.New("settings unreadable")}
	w := newWorker(workerConfig{runs: &fakeRuns{}, channels: p})

	w.notifyFailure(context.Background(), f.run, f.script, f.result)

	assert.Len(t, p.keys, 1, "a script with no owner still reaches the operator's channels")
	assert.Empty(t, p.payloads[0].Link, "no portal address, no link")
}

func TestFailureChannels(t *testing.T) {
	supplied := &fakePoster{}
	assert.Same(t, supplied, failureChannels(Config{FailureChannels: supplied}, nil))
	assert.Nil(t, failureChannels(Config{}, &fakeNotifier{}), "no enqueuer of its own, nothing to post through")
	enq := notification.NewEnqueuer(nil, nil, 13)
	t.Cleanup(enq.Close)
	assert.Nil(t, failureChannels(Config{}, enq), "no database, no channels")
}

func TestFailureChannels_BuiltOverTheDatabaseAndTheEnqueuer(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	enq := notification.NewEnqueuer(nil, nil, 13)
	t.Cleanup(enq.Close)
	assert.NotNil(t, failureChannels(Config{DB: db}, enq))
}
