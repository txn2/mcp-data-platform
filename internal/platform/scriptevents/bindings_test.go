package scriptevents

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatedReader answers each read once release is closed, counting reads.
type gatedReader struct {
	mu      sync.Mutex
	answer  map[string][]string
	err     error
	reads   atomic.Int32
	release chan struct{}
	started chan struct{}
}

func (g *gatedReader) EventBindings(context.Context) (map[string][]string, error) {
	g.reads.Add(1)
	if g.started != nil {
		g.started <- struct{}{}
	}
	if g.release != nil {
		<-g.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.answer, g.err
}

func (g *gatedReader) set(answer map[string][]string) {
	g.mu.Lock()
	g.answer, g.err = answer, nil
	g.mu.Unlock()
}

// fakeClock is a settable now.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestBindings_AnswersFromMemoryUntilAChangeOrMaxAge(t *testing.T) {
	r := &gatedReader{answer: map[string][]string{"shop": {"s1"}}}
	clock := &fakeClock{t: received}
	b := &Bindings{Reader: r, MaxAge: time.Minute, now: clock.now}

	ids, err := b.For(t.Context(), "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{"s1"}, ids)
	ids, err = b.For(t.Context(), "esp")
	require.NoError(t, err)
	assert.Equal(t, []string{}, ids, "a source no script names is an empty list, never nil")
	assert.Equal(t, int32(1), r.reads.Load())

	r.set(map[string][]string{"shop": {"s1", "s2"}})
	b.Notify()
	ids, err = b.For(t.Context(), "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{"s1", "s2"}, ids, "a change heard is read at the next lookup")
	assert.Equal(t, int32(2), r.reads.Load())

	r.set(map[string][]string{})
	clock.add(59 * time.Second)
	ids, _ = b.For(t.Context(), "shop")
	assert.Equal(t, []string{"s1", "s2"}, ids, "inside MaxAge with nothing heard, memory answers")
	clock.add(time.Second)
	ids, _ = b.For(t.Context(), "shop")
	assert.Equal(t, []string{}, ids, "past MaxAge the map is read again, a lost NOTIFY's backstop")
	assert.Equal(t, int32(3), r.reads.Load())
}

func TestBindings_ConcurrentLookupsShareOneRead(t *testing.T) {
	r := &gatedReader{answer: map[string][]string{"shop": {"s1"}}, release: make(chan struct{}), started: make(chan struct{}, 1)}
	b := &Bindings{Reader: r}
	const callers = 20
	var wg sync.WaitGroup
	results := make(chan []string, callers)
	for range callers {
		wg.Go(func() {
			ids, err := b.For(context.Background(), "shop")
			assert.NoError(t, err)
			results <- ids
		})
	}
	<-r.started
	close(r.release)
	wg.Wait()
	close(results)
	for ids := range results {
		assert.Equal(t, []string{"s1"}, ids)
	}
	assert.Equal(t, int32(1), r.reads.Load(), "a burst arriving while the map is read is one query")
}

func TestBindings_AChangeHeardDuringAReadIsNotLost(t *testing.T) {
	r := &gatedReader{answer: map[string][]string{}, release: make(chan struct{}), started: make(chan struct{}, 1)}
	b := &Bindings{Reader: r, MaxAge: time.Hour}
	done := make(chan struct{})
	go func() {
		_, _ = b.For(context.Background(), "shop")
		close(done)
	}()
	<-r.started
	b.Notify() // the binding changed after the read began
	r.set(map[string][]string{"shop": {"s1"}})
	close(r.release)
	<-done

	r.release = nil
	r.started = nil
	ids, err := b.For(t.Context(), "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{"s1"}, ids, "the read that began before the change does not stand for it")
}

func TestBindings_AFailedReadIsReturnedAndRetried(t *testing.T) {
	r := &gatedReader{err: errDown}
	b := &Bindings{Reader: r}
	_, err := b.For(t.Context(), "shop")
	require.ErrorIs(t, err, errDown)
	r.set(map[string][]string{"shop": {"s1"}})
	ids, err := b.For(t.Context(), "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{"s1"}, ids, "a failed read leaves nothing cached")
}

func TestBindings_ACallerWhoseContextEndsStopsWaiting(t *testing.T) {
	r := &gatedReader{release: make(chan struct{}), started: make(chan struct{}, 1)}
	b := &Bindings{Reader: r}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := b.For(ctx, "shop")
		errc <- err
	}()
	<-r.started
	cancel()
	require.True(t, errors.Is(<-errc, context.Canceled))
	close(r.release)
	assert.Equal(t, DefaultBindingsMaxAge, (&Bindings{}).maxAge())
}
