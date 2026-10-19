package levelalert

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

type sent struct {
	key   string
	title string
	body  string
}

type fakeSender struct{ sent []sent }

func (f *fakeSender) Send(_ context.Context, names []string, key string, p notification.Payload) int {
	f.sent = append(f.sent, sent{key: key, title: p.Document.Title, body: p.Document.Body})
	return len(names)
}

type fakeStore struct {
	rows map[string]Record
	fam  map[string]string
	err  map[string]error
}

func newStore() *fakeStore {
	return &fakeStore{rows: map[string]Record{}, fam: map[string]string{}, err: map[string]error{}}
}

func (f *fakeStore) List(_ context.Context, family string) ([]Record, error) {
	if err := f.err["list"]; err != nil {
		return nil, err
	}
	var out []Record
	for k, r := range f.rows {
		if f.fam[k] == family {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (f *fakeStore) Upsert(_ context.Context, family string, fd Finding, now time.Time) error {
	if err := f.err["upsert"]; err != nil {
		return err
	}
	r, ok := f.rows[fd.Key]
	if !ok || r.Level != fd.Level {
		r.Since = now
	}
	r.Key, r.Level, r.Title = fd.Key, fd.Level, fd.Title
	f.rows[fd.Key], f.fam[fd.Key] = r, family
	return nil
}

func (f *fakeStore) Delete(_ context.Context, key string) error {
	if err := f.err["delete"]; err != nil {
		return err
	}
	delete(f.rows, key)
	return nil
}

func keys(s []sent) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		out = append(out, x.key)
	}
	return out
}

// TestApplyWalksWarningCriticalResolved holds the engine to its contract: a
// condition is sent at its level on every pass under a key carrying the level,
// a move to critical is a new key, and a condition that clears is resolved
// once and forgotten.
func TestApplyWalksWarningCriticalResolved(t *testing.T) {
	ctx := context.Background()
	store, sender := newStore(), &fakeSender{}
	e := New(store, sender)
	clock := time.Unix(1000, 0)
	e.now = func() time.Time { return clock }
	ch := []string{"ops"}
	age := func(l Level) Finding {
		return Finding{Key: "webhook:spool_age:a", Level: l, Title: "spool age on a", Body: "35m of 1h", Link: "https://p/admin/webhooks"}
	}

	res, err := e.Apply(ctx, "webhook", ch, []Finding{age(Warning)})
	require.NoError(t, err)
	assert.Equal(t, Result{Alerting: 1}, res)
	clock = time.Unix(1060, 0)
	_, err = e.Apply(ctx, "webhook", ch, []Finding{age(Warning)})
	require.NoError(t, err)
	clock = time.Unix(1120, 0)
	_, err = e.Apply(ctx, "webhook", ch, []Finding{age(Critical)})
	require.NoError(t, err)
	res, err = e.Apply(ctx, "webhook", ch, nil)
	require.NoError(t, err)
	assert.Equal(t, Result{Resolved: 1}, res)
	res, err = e.Apply(ctx, "webhook", ch, nil)
	require.NoError(t, err)
	assert.Equal(t, Result{}, res, "a resolved condition is resolved once")

	assert.Equal(t, []string{
		"webhook:spool_age:a:warning:1000", "webhook:spool_age:a:warning:1000",
		"webhook:spool_age:a:critical:1120", "webhook:spool_age:a:resolved:1120",
	}, keys(sender.sent), "an episode repeats under one key; a new level is a new key")
	assert.Equal(t, "WARNING: spool age on a", sender.sent[0].title)
	assert.Equal(t, "CRITICAL: spool age on a", sender.sent[2].title)
	assert.Equal(t, "Resolved: spool age on a", sender.sent[3].title)
	assert.Contains(t, sender.sent[3].body, "Was critical since")
	assert.Empty(t, store.rows)
}

// TestApplyKeepsFamiliesApart: one family's pass never resolves another's.
func TestApplyKeepsFamiliesApart(t *testing.T) {
	ctx := context.Background()
	store, sender := newStore(), &fakeSender{}
	e := New(store, sender)
	_, err := e.Apply(ctx, "storage", []string{"ops"}, []Finding{{Key: "storage:database:used", Level: Warning, Title: "db"}})
	require.NoError(t, err)
	_, err = e.Apply(ctx, "webhook", []string{"ops"}, nil)
	require.NoError(t, err)
	assert.Len(t, store.rows, 1)
	assert.Len(t, sender.sent, 1)
	assert.Contains(t, sender.sent[0].key, "storage:database:used:warning:")
}

// TestApplyWithNoChannelsRecordsAndSendsNothing: conditions are still
// recorded, so naming a channel later does not raise a resolved for a
// condition nobody was told about.
func TestApplyWithNoChannelsRecordsAndSendsNothing(t *testing.T) {
	ctx := context.Background()
	store, sender := newStore(), &fakeSender{}
	e := New(store, sender)
	_, err := e.Apply(ctx, "webhook", nil, []Finding{{Key: "k", Level: Critical}})
	require.NoError(t, err)
	assert.Empty(t, sender.sent)
	assert.Len(t, store.rows, 1)
	assert.Nil(t, New(nil, sender))
	res, err := (*Engine)(nil).Apply(ctx, "x", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, Result{}, res)
	_, err = New(store, nil).Apply(ctx, "webhook", []string{"ops"}, nil)
	require.NoError(t, err)
}

func TestApplyFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	for name, want := range map[string]string{"list": "reading", "upsert": "recording", "delete": "recording"} {
		t.Run(name, func(t *testing.T) {
			store := newStore()
			store.rows["old"], store.fam["old"] = Record{Key: "old", Level: Warning}, "f"
			store.err[name] = boom
			_, err := New(store, &fakeSender{}).Apply(ctx, "f", []string{"ops"}, []Finding{{Key: "new", Level: Warning}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), want)
		})
	}
}

func TestThresholds(t *testing.T) {
	up := Thresholds{Warning: 75, Critical: 90}
	assert.Equal(t, Level(""), up.Of(74.9))
	assert.Equal(t, Warning, up.Of(75))
	assert.Equal(t, Critical, up.Of(95))
	down := Thresholds{Warning: 30, Critical: 7, Below: true}
	assert.Equal(t, Level(""), down.Of(31))
	assert.Equal(t, Warning, down.Of(30))
	assert.Equal(t, Critical, down.Of(2))
	assert.Equal(t, Level(""), Thresholds{}.Of(1e9), "zero thresholds are off")
	assert.Equal(t, Critical, Thresholds{Critical: 10}.Of(10))
	assert.Equal(t, Critical, Worse(Warning, Critical))
	assert.Equal(t, Critical, Worse(Critical, Warning))
	assert.Equal(t, Warning, Worse("", Warning))
}

// TestAConditionThatReturnsIsANewEpisode: a condition that resolves and comes
// back is sent under a new key, so the channel's repeat window for the first
// episode does not hold it back.
func TestAConditionThatReturnsIsANewEpisode(t *testing.T) {
	ctx := context.Background()
	store, sender := newStore(), &fakeSender{}
	e := New(store, sender)
	clock := time.Unix(1000, 0)
	e.now = func() time.Time { return clock }
	f := Finding{Key: "webhook:refusals:esp", Level: Warning, Title: "refusals"}
	_, err := e.Apply(ctx, "webhook", []string{"ops"}, []Finding{f})
	require.NoError(t, err)
	clock = time.Unix(1360, 0)
	_, err = e.Apply(ctx, "webhook", []string{"ops"}, nil)
	require.NoError(t, err)
	clock = time.Unix(1480, 0)
	_, err = e.Apply(ctx, "webhook", []string{"ops"}, []Finding{f})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"webhook:refusals:esp:warning:1000", "webhook:refusals:esp:resolved:1000", "webhook:refusals:esp:warning:1480",
	}, keys(sender.sent))
}

// TestAConditionThatCouldNotBeJudgedIsCarried: a pass that could not read a
// store leaves its condition as it stands, neither resolved nor sent.
func TestAConditionThatCouldNotBeJudgedIsCarried(t *testing.T) {
	ctx := context.Background()
	store, sender := newStore(), &fakeSender{}
	e := New(store, sender)
	_, err := e.Apply(ctx, "storage", []string{"ops"}, []Finding{{Key: "storage:seaweedfs:used", Level: Critical}})
	require.NoError(t, err)
	res, err := e.Apply(ctx, "storage", []string{"ops"}, nil, "storage:seaweedfs:")
	require.NoError(t, err)
	assert.Equal(t, Result{}, res)
	assert.Len(t, sender.sent, 1, "nothing resolved while unknown")
	assert.Contains(t, store.rows, "storage:seaweedfs:used")
	res, err = e.Apply(ctx, "storage", []string{"ops"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Resolved, "a pass that can judge it resolves it")
}
