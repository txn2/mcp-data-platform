package notifyworker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/notification/notifychannel"
	"github.com/txn2/mcp-data-platform/internal/notification/notifypost"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/notification/smtp"
)

// fakeChannelStore serves the operator's channel records.
type fakeChannelStore struct {
	channels map[string]notification.Channel
	err      error
}

func (f *fakeChannelStore) List(context.Context) ([]notification.Channel, error) {
	return nil, f.err
}

func (f *fakeChannelStore) Get(_ context.Context, name string) (*notification.Channel, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch, ok := f.channels[name]
	if !ok {
		return nil, notifychannel.ErrChannelNotFound
	}
	return &ch, nil
}

func (*fakeChannelStore) Set(context.Context, notification.Channel) error { return nil }
func (*fakeChannelStore) Delete(context.Context, string) error            { return nil }

// recordingSender records what it was asked to post and fails on demand. It
// replaces the dispatcher rather than one kind's transport, which is the seam
// the worker actually holds.
type recordingSender struct {
	mu    sync.Mutex
	posts []notification.Document
	err   error
}

func (s *recordingSender) Send(_ context.Context, _ notification.Channel, doc notification.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.posts = append(s.posts, doc)
	return nil
}

func (s *recordingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.posts)
}

// chatRow is one queued document addressed to a channel.
func chatRow(channel, title string) notification.Notification {
	doc := notification.Document{Title: title, Body: "body", Link: "https://x.example.com"}
	return notification.Notification{
		// Every caller wants the same row id; what varies is where it goes.
		ID:        1,
		Recipient: notification.ChannelRecipientPrefix + channel,
		Channel:   channel,
		Category:  notification.CategoryChannel,
		Payload: notification.Payload{
			Kind: notification.KindChannel, ItemTitle: title, Document: &doc,
		},
	}
}

// channelWorker assembles a worker whose channel transport is snd and whose
// SMTP settings are whatever store is given.
func channelWorker(t *testing.T, queue *fakeQueueStore, settings smtp.SettingsStore, channels notification.ChannelStore, snd *recordingSender) *Worker {
	t.Helper()
	w := testWorker(t, queue, settings, &fakeSender{})
	w.cfg.Channels = channels
	w.cfg.ChannelSenders = snd
	return w
}

func TestWorker_DeliversAChatRowWithNoMailServerConfigured(t *testing.T) {
	// The whole point of the transport filter: before channels the worker
	// asked one question -- is SMTP usable? -- and answered no by draining
	// nothing. A deployment with channels and no mail server must still post.
	queue := &fakeQueueStore{immediate: [][]notification.Notification{
		{chatRow("ops", "Threshold crossed")}, nil,
	}}
	snd := &recordingSender{}
	w := channelWorker(t, queue,
		&fakeSettingsStore{err: smtp.ErrNotFound},
		&fakeChannelStore{channels: map[string]notification.Channel{
			"ops": {Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true, Connection: "c", Target: "C1"},
		}}, snd)

	w.drain()

	if snd.count() != 1 {
		t.Fatalf("posted %d documents with no mail server; want 1", snd.count())
	}
	if len(queue.sent) != 1 {
		t.Errorf("the delivered row was not marked sent (sent batches: %d)", len(queue.sent))
	}
	for _, f := range queue.filters {
		if f.Email {
			t.Error("the worker claimed email rows with no mail server configured")
		}
		if !f.Channel {
			t.Error("the worker did not claim channel rows")
		}
	}
}

func TestWorker_LeavesChannelRowsAloneWithNoChannelTransport(t *testing.T) {
	// The mirror case: a deployment whose gateway is not wired delivers email
	// and leaves its chat rows pending rather than failing them.
	queue := &fakeQueueStore{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, &fakeSender{})
	w.drain()
	if len(queue.filters) == 0 {
		t.Fatal("the worker made no claim at all")
	}
	for _, f := range queue.filters {
		if f.Channel {
			t.Error("the worker claimed channel rows with no channel transport wired")
		}
		if !f.Email {
			t.Error("the worker stopped delivering email")
		}
	}
}

func TestWorker_DrainsNothingWhenNeitherTransportCanDeliver(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]notification.Notification{{chatRow("ops", "t")}}}
	w := testWorker(t, queue, &fakeSettingsStore{err: smtp.ErrNotFound}, &fakeSender{})
	w.drain()
	if len(queue.filters) != 0 {
		t.Errorf("the worker issued %d claims with no usable transport", len(queue.filters))
	}
}

func TestWorker_ChannelFailuresResolveByWhetherRetryingCouldHelp(t *testing.T) {
	tests := []struct {
		name     string
		channels map[string]notification.Channel
		sendErr  error
		terminal bool
		wants    string
	}{
		{
			name:     "a deleted channel fails at once",
			channels: map[string]notification.Channel{},
			terminal: true,
			wants:    "no longer exists",
		},
		{
			name: "a disabled channel fails at once",
			channels: map[string]notification.Channel{
				"ops": {Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: false},
			},
			terminal: true,
			wants:    "is disabled",
		},
		{
			name: "a refused message fails at once",
			channels: map[string]notification.Channel{
				"ops": {Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true, Connection: "c", Target: "C1"},
			},
			sendErr:  notifypost.ErrTerminal,
			terminal: true,
		},
		{
			name: "an unreachable upstream retries",
			channels: map[string]notification.Channel{
				"ops": {Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true, Connection: "c", Target: "C1"},
			},
			sendErr:  errors.New("connection refused"),
			terminal: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			queue := &fakeQueueStore{immediate: [][]notification.Notification{
				{chatRow("ops", "t")}, nil,
			}}
			w := channelWorker(t, queue, &fakeSettingsStore{err: smtp.ErrNotFound},
				&fakeChannelStore{channels: tc.channels}, &recordingSender{err: tc.sendErr})
			w.drain()

			assertResolved(t, queue, tc.terminal)
			if tc.wants != "" && !strings.Contains(queue.lastError, tc.wants) {
				t.Errorf("recorded error %q does not mention %q", queue.lastError, tc.wants)
			}
		})
	}
}

func TestWorker_ARowWithNoDocumentIsTerminal(t *testing.T) {
	// Nothing writes such a row today. It fails rather than retrying because
	// a row the worker cannot interpret will not become interpretable.
	row := chatRow("ops", "t")
	row.Payload.Document = nil
	queue := &fakeQueueStore{immediate: [][]notification.Notification{{row}, nil}}
	w := channelWorker(t, queue, &fakeSettingsStore{err: smtp.ErrNotFound},
		&fakeChannelStore{channels: map[string]notification.Channel{
			"ops": {Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true, Connection: "c", Target: "C1"},
		}}, &recordingSender{})
	w.drain()
	if len(queue.failed) != 1 {
		t.Errorf("failed batches = %d, want 1", len(queue.failed))
	}
	if !strings.Contains(queue.lastError, "carries no document") {
		t.Errorf("recorded error %q does not say what was wrong", queue.lastError)
	}
}

func TestWorker_EmailChannelRowsGoThroughTheMailPath(t *testing.T) {
	// An email channel's rows are addressed to people, so they are rendered
	// and sent over SMTP like any other notification -- never handed to a
	// channel transport.
	row := chatRow("ops-email", "Weekly")
	row.Recipient = "reader@example.com"
	queue := &fakeQueueStore{immediate: [][]notification.Notification{{row}, nil}}
	snd := &recordingSender{}
	mail := &fakeSender{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, mail)
	w.cfg.Channels = &fakeChannelStore{}
	w.cfg.ChannelSenders = snd

	w.drain()

	if snd.count() != 0 {
		t.Error("an email channel's row was handed to a chat transport")
	}
	if got := len(mail.sentCopy()); got != 1 {
		t.Errorf("mail sends = %d, want 1", got)
	}
}

// assertResolved checks that a batch went to the one resolution its failure
// deserves: a terminal failure is failed at once and never retried, and a
// retryable one is retried and never failed.
func assertResolved(t *testing.T, queue *fakeQueueStore, terminal bool) {
	t.Helper()
	wantFailed, wantRetried := 0, 1
	if terminal {
		wantFailed, wantRetried = 1, 0
	}
	if len(queue.failed) != wantFailed {
		t.Errorf("failed batches = %d, want %d", len(queue.failed), wantFailed)
	}
	if len(queue.retried) != wantRetried {
		t.Errorf("retried batches = %d, want %d", len(queue.retried), wantRetried)
	}
}
