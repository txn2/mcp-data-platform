package scriptevents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

type fakeStore struct {
	scripts  map[string][]string
	listErr  error
	enqErr   error
	enqueued [][]*script.Run
	max      int
}

func (f *fakeStore) ScriptsForSource(_ context.Context, source string) ([]string, error) {
	return f.scripts[source], f.listErr
}

func (f *fakeStore) EnqueueEvents(_ context.Context, runs []*script.Run, maxQueued int) (EventEnqueue, error) {
	if f.enqErr != nil {
		return EventEnqueue{}, f.enqErr
	}
	f.enqueued = append(f.enqueued, runs)
	f.max = maxQueued
	return EventEnqueue{Enqueued: len(runs)}, nil
}

type fakeScripts struct {
	scripts  map[string]*script.Script
	versions map[string]*script.Version
	err      error
}

func (f fakeScripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	return f.scripts[id], f.err
}

func (f fakeScripts) GetVersion(_ context.Context, id string, _ int) (*script.Version, error) {
	return f.versions[id], nil
}

func liveScript(id string) *script.Script {
	return &script.Script{ID: id, Name: id, Enabled: true, Status: script.StatusActive, Version: 2, OwnerEmail: "jane@example.com"}
}

var received = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

func events(ids ...string) []whevent.Event {
	out := make([]whevent.Event, 0, len(ids))
	for _, id := range ids {
		out = append(out, whevent.Event{EventID: id, EventType: "sale", Key: "k-" + id, ReceivedAt: received, Payload: []byte(`{"amount":12}`)})
	}
	return out
}

// #2332: each event of a source a script names enqueues one run of it, with
// the event as run.event; a source no script names enqueues nothing.
func TestStored_EnqueuesOneRunPerEventPerScript(t *testing.T) {
	store := &fakeStore{scripts: map[string][]string{"shop": {"s1", "s2"}}}
	scripts := fakeScripts{
		scripts: map[string]*script.Script{"s1": liveScript("s1"), "s2": liveScript("s2")},
		versions: map[string]*script.Version{
			"s1": {ID: "v1", Version: 2, Params: []script.Param{{Name: "region", Type: script.ParamTypeString, Default: "us"}}},
			"s2": {ID: "v2", Version: 2},
		},
	}
	e := &Enqueuer{Store: store, Scripts: scripts, MaxQueued: 7}
	e.Stored(t.Context(), "shop", events("ev1", "ev2"))

	require.Len(t, store.enqueued, 2)
	assert.Equal(t, 7, store.max)
	first := store.enqueued[0][0]
	assert.Equal(t, runstate.TriggerEvent, first.Trigger)
	assert.Equal(t, "s1", first.ScriptID)
	assert.Equal(t, "v1", first.VersionID)
	assert.Equal(t, "shop", first.EventSource)
	assert.Equal(t, "ev1", first.EventID)
	assert.Equal(t, "jane@example.com", first.RequestedBy)
	assert.Equal(t, map[string]any{"region": "us"}, first.Params, "a parameter's default is what an event run is bound with")
	assert.NotEmpty(t, first.ID)
	assert.NotEqual(t, first.ID, store.enqueued[0][1].ID)
	assert.Equal(t, map[string]any{
		KeyID: "ev1", KeyType: "sale", KeyKey: "k-ev1", KeyReceivedAt: "2026-10-26T09:00:00Z", KeySource: "shop",
		KeyPayload: map[string]any{"amount": float64(12)},
	}, first.Event)

	store.enqueued = nil
	e.Stored(t.Context(), "nobody", events("ev3"))
	assert.Empty(t, store.enqueued)
	e.Stored(t.Context(), "shop", nil)
	assert.Empty(t, store.enqueued)
	var none *Enqueuer
	none.Stored(t.Context(), "shop", events("ev4"))
}

// A script that cannot run, or cannot be read, enqueues nothing, and a
// failure never reaches the delivery.
func TestStored_Refusals(t *testing.T) {
	disabled := liveScript("s1")
	disabled.Enabled = false
	needsParam := liveScript("s2")
	store := &fakeStore{scripts: map[string][]string{"shop": {"s1", "s2", "s3"}}}
	scripts := fakeScripts{
		scripts:  map[string]*script.Script{"s1": disabled, "s2": needsParam, "s3": liveScript("s3")},
		versions: map[string]*script.Version{"s2": {ID: "v2", Params: []script.Param{{Name: "day", Type: script.ParamTypeString, Required: true}}}},
	}
	e := &Enqueuer{Store: store, Scripts: scripts}
	e.Stored(t.Context(), "shop", events("ev1"))
	assert.Empty(t, store.enqueued, "a disabled script, a required parameter with no value, and a missing version")

	for name, e := range map[string]*Enqueuer{
		"listing fails": {Store: &fakeStore{listErr: errors.New("db down")}, Scripts: scripts},
		"reading fails": {Store: &fakeStore{scripts: map[string][]string{"shop": {"s1"}}}, Scripts: fakeScripts{err: errors.New("db down")}},
		"enqueue fails": {
			Store:   &fakeStore{scripts: map[string][]string{"shop": {"s3"}}, enqErr: errors.New("full")},
			Scripts: fakeScripts{scripts: map[string]*script.Script{"s3": liveScript("s3")}, versions: map[string]*script.Version{"s3": {ID: "v3"}}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.NotPanics(t, func() { e.Stored(t.Context(), "shop", events("ev1")) })
			if fs, ok := e.Store.(*fakeStore); ok {
				assert.Empty(t, fs.enqueued)
			}
		})
	}
}

func TestValue_TextPayloadAndDefaults(t *testing.T) {
	v := Value("shop", whevent.Event{EventID: "e", Payload: []byte("not json"), ReceivedAt: received})
	assert.Equal(t, "not json", v[KeyPayload])
	e := &Enqueuer{}
	assert.Equal(t, DefaultMaxQueued, e.maxQueued())
	assert.Equal(t, defaultTimeout, e.timeout())
	assert.Equal(t, time.Second, (&Enqueuer{Timeout: time.Second}).timeout())
}
