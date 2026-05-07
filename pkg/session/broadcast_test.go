package session

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryBroadcaster_DeliversToAllSubscribers(t *testing.T) {
	b := NewMemoryBroadcaster(nil)
	defer func() { _ = b.Close() }()

	const subscribers = 5
	subs := make([]Subscription, subscribers)
	for i := range subscribers {
		subs[i] = b.Subscribe(context.Background(), "s")
	}
	if got := b.SubscriberCount(); got != subscribers {
		t.Fatalf("SubscriberCount = %d, want %d", got, subscribers)
	}

	if err := b.Publish(context.Background(), Event{Method: "notifications/tools/list_changed"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for i, sub := range subs {
		select {
		case ev := <-sub.Events():
			if ev.Method != "notifications/tools/list_changed" {
				t.Errorf("sub %d: method = %q", i, ev.Method)
			}
		case <-time.After(time.Second):
			t.Errorf("sub %d: no event received", i)
		}
	}
}

func TestMemoryBroadcaster_CloseUnsubscribes(t *testing.T) {
	b := NewMemoryBroadcaster(nil)
	defer func() { _ = b.Close() }()

	sub := b.Subscribe(context.Background(), "s")
	sub.Close()
	if got := b.SubscriberCount(); got != 0 {
		t.Errorf("after Close: SubscriberCount = %d, want 0", got)
	}
	// Channel must be closed so a ranged-over receive exits cleanly.
	if _, ok := <-sub.Events(); ok {
		t.Error("expected closed channel after Close")
	}
	// Idempotent — second Close is a no-op (no panic).
	sub.Close()
}

func TestMemoryBroadcaster_ContextCancelClosesSubscription(t *testing.T) {
	b := NewMemoryBroadcaster(nil)
	defer func() { _ = b.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	sub := b.Subscribe(ctx, "s")
	cancel()

	// Wait for the closer goroutine.
	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("subscription not closed after ctx cancel")
		case ev, ok := <-sub.Events():
			if !ok {
				if got := b.SubscriberCount(); got != 0 {
					t.Errorf("SubscriberCount = %d, want 0", got)
				}
				return
			}
			t.Errorf("unexpected event after cancel: %+v", ev)
		}
	}
}

func TestMemoryBroadcaster_DropsOnSlowSubscriber(t *testing.T) {
	b := NewMemoryBroadcaster(nil)
	defer func() { _ = b.Close() }()

	sub := b.Subscribe(context.Background(), "slow")

	// Fill the buffer plus several extras. None of the publishes must block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range defaultEventBufferSize * 2 {
			if err := b.Publish(context.Background(), Event{Method: "notifications/tools/list_changed"}); err != nil {
				t.Errorf("Publish: %v", err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on slow subscriber — broadcaster must drop, not block")
	}

	// The subscriber should still be alive (drops are silent to the channel).
	if got := b.SubscriberCount(); got != 1 {
		t.Errorf("SubscriberCount = %d, want 1", got)
	}
	sub.Close()
}

func TestMemoryBroadcaster_PublishAfterCloseReturnsError(t *testing.T) {
	b := NewMemoryBroadcaster(nil)
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	err := b.Publish(context.Background(), Event{Method: "notifications/tools/list_changed"})
	if !errors.Is(err, ErrBroadcasterClosed) {
		t.Errorf("Publish after Close: err = %v, want ErrBroadcasterClosed", err)
	}
	// Subscribe after Close returns a closed-immediate subscription.
	sub := b.Subscribe(context.Background(), "s")
	if _, ok := <-sub.Events(); ok {
		t.Error("Subscribe after Close: expected closed channel")
	}
}

func TestMemoryBroadcaster_CloseIsIdempotent(t *testing.T) {
	b := NewMemoryBroadcaster(nil)
	if err := b.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestMemoryBroadcaster_ConcurrentPublishSubscribe(t *testing.T) {
	b := NewMemoryBroadcaster(nil)
	defer func() { _ = b.Close() }()

	// Pre-register subscribers BEFORE publishing so the test verifies
	// actual delivery rather than racing publishes against subscriber
	// registration. A discarded testing.T (the prior signature) hid
	// any delivery failures because no assertion ran.
	const subscribers = 8
	const eventsPerSub = 5
	var totalReceived atomic.Int32

	subs := make([]Subscription, subscribers)
	for i := range subscribers {
		subs[i] = b.Subscribe(context.Background(), "concurrent")
	}
	t.Cleanup(func() {
		for _, sub := range subs {
			sub.Close()
		}
	})

	var wg sync.WaitGroup
	for _, sub := range subs {
		wg.Go(func() {
			drained := 0
			deadline := time.After(2 * time.Second)
			for drained < eventsPerSub {
				select {
				case _, ok := <-sub.Events():
					if !ok {
						return
					}
					drained++
					totalReceived.Add(1)
				case <-deadline:
					return
				}
			}
		})
	}
	// Publish enough events so every subscriber can drain eventsPerSub
	// without racing the buffer-full drop branch.
	for range 50 {
		_ = b.Publish(context.Background(), Event{Method: "x"})
	}
	wg.Wait()
	if got := totalReceived.Load(); got < subscribers {
		t.Errorf("totalReceived = %d, want at least %d (one event per subscriber)", got, subscribers)
	}
}
