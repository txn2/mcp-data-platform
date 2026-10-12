package scriptincident

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// memStore is script_failure_incidents in memory.
type memStore struct {
	open      map[string]*Incident
	openErr   error
	closeErr  error
	openCalls int
}

func (m *memStore) Open(_ context.Context, o Opening) (bool, error) {
	m.openCalls++
	if m.openErr != nil {
		return false, m.openErr
	}
	if in, ok := m.open[o.ScriptID]; ok {
		in.FailedRuns++
		return false, nil
	}
	if m.open == nil {
		m.open = map[string]*Incident{}
	}
	m.open[o.ScriptID] = &Incident{OpenedAt: o.FailedAt, OpeningRunID: o.RunID, OpeningCause: o.Cause, OpeningError: o.Error, FailedRuns: 1}
	return true, nil
}

func (m *memStore) Close(_ context.Context, id string) (*Incident, error) {
	if m.closeErr != nil {
		return nil, m.closeErr
	}
	in := m.open[id]
	delete(m.open, id)
	return in, nil
}

type notifier struct {
	to  []string
	got []notification.Payload
	err error
}

func (n *notifier) Notify(_ context.Context, to, _ string, p notification.Payload) (bool, error) {
	n.to, n.got = append(n.to, to), append(n.got, p)
	return n.err == nil, n.err
}

type poster struct {
	keys []string
	err  error
}

func (p *poster) Post(_ context.Context, key string, _ notification.Payload) (int, error) {
	p.keys = append(p.keys, key)
	return 1, p.err
}

func fixture() (*script.Script, func(id string) *script.Run, script.RunResult) {
	sc := &script.Script{ID: "s1", Name: "nightly", OwnerEmail: "jane@example.com"}
	run := func(id string) *script.Run {
		return &script.Run{ID: id, ScriptID: sc.ID, Trigger: script.TriggerSchedule}
	}
	return sc, run, script.RunResult{Status: script.RunStatusFailed, Error: "Traceback (most recent call last):\n  main.star:2\nError: timeout", Cause: "upstream", Log: "loading\n"}
}

// TestAlerter_OneIncidentOneFailureOneRecovery is #2041 at the alerter: a
// failure is told when it opens the incident and counted otherwise, and the
// success that closes it is told as the recovery under its own channel key.
func TestAlerter_OneIncidentOneFailureOneRecovery(t *testing.T) {
	sc, run, res := fixture()
	store, n, p := &memStore{}, &notifier{}, &poster{}
	a := Alerter{Store: store, Notifier: n, Channels: p, PortalURL: "https://p.example.com/"}
	repeats := 0
	count := func() int { repeats++; return 7 }
	ctx := context.Background()

	a.Failed(ctx, run("r1"), sc, res, count)
	a.Failed(ctx, run("r2"), sc, res, count)
	require.Len(t, n.got, 1, "the second failure is counted, not sent")
	assert.Equal(t, 1, repeats, "the streak is read only for the run that is told")
	assert.Equal(t, 7, n.got[0].Repeats)
	assert.Equal(t, "r1", n.got[0].ItemID)
	assert.Equal(t, "https://p.example.com/portal/automations/s1/runs/r1", n.got[0].Link)
	assert.Equal(t, sc.Principal(), n.got[0].Actor)
	assert.Contains(t, n.got[0].Message, "Error: timeout")
	assert.Equal(t, "Error: timeout", store.open["s1"].OpeningError, "the line of the opening run's error that names the failure")

	a.Succeeded(ctx, run("r3"), sc)
	require.Len(t, n.got, 2)
	rec := n.got[1].Recovery
	require.NotNil(t, rec)
	assert.Equal(t, 2, rec.FailedRuns)
	assert.Equal(t, "r1", rec.OpeningRunID)
	assert.Equal(t, "upstream", rec.OpeningCause)
	assert.Equal(t, "https://p.example.com/portal/automations/s1/runs/r1", rec.OpeningLink)
	assert.False(t, rec.RecoveredAt.Before(rec.FailingSince))
	assert.Equal(t, []string{"script:s1", "script:s1:recovered"}, p.keys)

	a.Succeeded(ctx, run("r4"), sc)
	assert.Len(t, n.got, 2, "a success with nothing open tells nobody")
}

// TestAlerter_Degrades: no store tells every failure and no recovery; a store
// that cannot be written tells the failure anyway and no recovery; a missing
// owner, a refused enqueue and a refused post are logged, not raised.
func TestAlerter_Degrades(t *testing.T) {
	sc, run, res := fixture()
	ctx := context.Background()
	zero := func() int { return 0 }

	n := &notifier{}
	a := Alerter{Notifier: n}
	a.Failed(ctx, run("a"), sc, res, zero)
	a.Failed(ctx, run("b"), sc, res, zero)
	a.Succeeded(ctx, run("c"), sc)
	assert.Len(t, n.got, 2, "with no store every failure is told and no recovery")

	n = &notifier{}
	down := &memStore{openErr: errors.New("down"), closeErr: errors.New("down")}
	a = Alerter{Store: down, Notifier: n}
	a.Failed(ctx, run("a"), sc, res, zero)
	a.Succeeded(ctx, run("b"), sc)
	assert.Len(t, n.got, 1, "an unwritable incident still tells the failure, and no recovery")

	n = &notifier{err: errors.New("queue full")}
	p := &poster{err: errors.New("settings unreadable")}
	nobody := &script.Script{ID: "s2", Name: "ownerless"}
	Alerter{Store: &memStore{}, Notifier: n, Channels: p}.Failed(ctx, run("a"), nobody, res, zero)
	assert.Empty(t, n.got, "no owner, nobody mailed")
	assert.Len(t, p.keys, 1, "the channel is still posted to")
	Alerter{Store: &memStore{}, Notifier: n}.Failed(ctx, run("b"), sc, res, zero)
	assert.Len(t, n.to, 1, "a refused enqueue was attempted")
}

func TestRunPage(t *testing.T) {
	assert.Empty(t, RunPage("", "s", "r"))
	assert.Equal(t, "https://p/portal/automations/s/runs/r", RunPage("https://p", "s", "r"))
}
