package whwrite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

type objects struct {
	keys []string
	err  error
}

func (o *objects) PutObject(_ context.Context, bucket, key string, _ []byte, contentType string) error {
	if o.err != nil {
		return o.err
	}
	o.keys = append(o.keys, bucket+"/"+key+" "+contentType)
	return nil
}

type recorder struct {
	marks []int64
	err   error
}

func (r *recorder) MarkSegment(_ context.Context, _ string, _ time.Time, _ time.Duration, bytes int64) error {
	r.marks = append(r.marks, bytes)
	return r.err
}

type raw struct {
	starts []time.Time
	err    error
}

func (r *raw) EnsureRawWindow(_ context.Context, _ whsource.Source, start time.Time) error {
	r.starts = append(r.starts, start)
	return r.err
}

func source() whsource.Source {
	return whsource.Source{Name: "orders-outbox", Connection: "scratch"}.WithDefaults()
}

func events(at ...time.Time) []whevent.Event {
	out := make([]whevent.Event, len(at))
	for i, t := range at {
		out[i] = whevent.Event{ReceivedAt: t, EventID: "e", Payload: []byte(`{}`)}
	}
	return out
}

func TestWrite_OneSegmentPerWindowOldestFirst(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 30, 0, 0, time.UTC)
	o, r, rw := &objects{}, &recorder{}, &raw{err: errors.New("no scratch catalog")}
	w := New(Config{Objects: o, Bucket: "res", Recorder: r, RawWindows: rw, Writer: "pg-r1", Now: func() time.Time { return now }})
	var told []int
	w.SetStored(func(_ context.Context, source string, evs []whevent.Event) {
		assert.Equal(t, "orders-outbox", source)
		told = append(told, len(evs))
	})

	require.NoError(t, w.Write(context.Background(), source(), events(now, now.Add(-2*time.Hour), now)))
	require.Len(t, o.keys, 2)
	assert.Contains(t, o.keys[0], "pg-r1")
	assert.True(t, strings.HasSuffix(o.keys[0], " application/gzip"))
	assert.Equal(t, []int{1, 2}, told, "the older window is written first")
	assert.Len(t, r.marks, 4, "each window is recorded before and after its write")
	assert.Equal(t, int64(0), r.marks[0])
	assert.Positive(t, r.marks[1])
	assert.Equal(t, []time.Time{now.Add(-2 * time.Hour).Truncate(time.Hour), now.Truncate(time.Hour)}, rw.starts,
		"a raw partition that cannot be registered is logged, not a failed write")

	require.NoError(t, w.Write(context.Background(), source(), nil), "nothing to write")
}

func TestWrite_Failures(t *testing.T) {
	ev := events(time.Now())
	err := New(Config{Objects: &objects{}, Recorder: &recorder{err: errors.New("db down")}}).Write(context.Background(), source(), ev)
	require.ErrorContains(t, err, "recording segment")
	err = New(Config{Objects: &objects{err: errors.New("bucket gone")}, Recorder: &recorder{}}).Write(context.Background(), source(), ev)
	require.ErrorContains(t, err, "writing segment")
}
