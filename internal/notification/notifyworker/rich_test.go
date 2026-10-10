package notifyworker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakeLoader serves attachment content by asset or resource id; an id it
// does not hold is unreadable.
type fakeLoader struct {
	files map[string]notification.File
	loads int
}

func (l *fakeLoader) Load(_ context.Context, att notification.Attachment) (notification.File, error) {
	l.loads++
	if f, ok := l.files[att.AssetID+att.ResourceID]; ok {
		return f, nil
	}
	return notification.File{}, errors.New("unreadable")
}

func attachedRow(channel string) notification.Notification {
	n := chatRow(channel, "Weekly")
	n.Payload.Document.Attachments = []notification.Attachment{
		{AssetID: "a1", Name: "chart.png", Link: "https://p/a1"},
		{ResourceID: "gone", Name: "raw.csv", Link: "https://p/gone"},
	}
	return n
}

func loader() *fakeLoader {
	return &fakeLoader{files: map[string]notification.File{"a1": {Name: "chart.png", Data: []byte("png")}}}
}

func channels(chs ...notification.Channel) *fakeChannelStore {
	m := map[string]notification.Channel{}
	for _, ch := range chs {
		m[ch.Name] = ch
	}
	return &fakeChannelStore{channels: m}
}

func TestWorker_AFileCarryingKindGetsTheFilesItCouldRead(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]notification.Notification{{attachedRow("ops")}, nil}}
	snd := &recordingSender{}
	w := channelWorker(t, queue, &fakeSettingsStore{}, channels(
		notification.Channel{Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true}), snd)
	w.cfg.Attachments = loader()

	_ = w.drain(context.Background())

	if len(snd.deliveries) != 1 {
		t.Fatalf("posted %d deliveries, want 1", len(snd.deliveries))
	}
	d := snd.deliveries[0]
	if len(d.Files) != 1 || d.Files[0].Name != "chart.png" {
		t.Errorf("files = %+v, want the readable one", d.Files)
	}
	if len(d.Linked) != 1 || d.Linked[0].Name != "raw.csv" {
		t.Errorf("linked = %+v, want the unreadable one sent as its link", d.Linked)
	}
	if len(queue.sent) != 1 {
		t.Error("an unreadable attachment failed the delivery")
	}
}

func TestWorker_AKindWithoutFilesReadsNone(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]notification.Notification{{attachedRow("hook")}, nil}}
	snd := &recordingSender{}
	w := channelWorker(t, queue, &fakeSettingsStore{}, channels(
		notification.Channel{Name: "hook", Kind: notification.ChannelKindWebhook, Enabled: true}), snd)
	l := loader()
	w.cfg.Attachments = l

	_ = w.drain(context.Background())

	if l.loads != 0 {
		t.Errorf("read %d attachments for a webhook, which carries none", l.loads)
	}
}

func TestWorker_NoLoaderSendsEveryAttachmentAsALink(t *testing.T) {
	files, linked := (&Worker{}).loadFiles(context.Background(), attachedRow("x").Payload.Document.Attachments)
	if len(files) != 0 || len(linked) != 2 {
		t.Errorf("files %d linked %d; want none read and both linked", len(files), len(linked))
	}
}

func digestRows(channel string, titles ...string) []notification.Notification {
	rows := make([]notification.Notification, 0, len(titles))
	for i, title := range titles {
		n := chatRow(channel, title)
		n.ID = int64(i + 10)
		n.Digest = true
		rows = append(rows, n)
	}
	return rows
}

func TestWorker_ADailyChannelsBatchPostsOneBulletin(t *testing.T) {
	queue := &fakeQueueStore{digests: [][]notification.Notification{digestRows("daily", "A", "B", "C"), nil}}
	snd := &recordingSender{}
	w := channelWorker(t, queue, &fakeSettingsStore{}, channels(
		notification.Channel{Name: "daily", Kind: notification.ChannelKindMattermost, Enabled: true, Mode: notification.ChannelModeDaily}), snd)

	_ = w.drain(context.Background())

	if snd.count() != 1 {
		t.Fatalf("posted %d messages for a three-document digest, want one bulletin", snd.count())
	}
	if got := snd.posts[0].Title; got != "daily: 3 updates today" {
		t.Errorf("bulletin title = %q", got)
	}
	for _, title := range []string{"**A**", "**B**", "**C**"} {
		if !strings.Contains(snd.posts[0].Body, title) {
			t.Errorf("the bulletin did not list %s", title)
		}
	}
	if len(queue.sent) != 1 || len(queue.sent[0]) != 3 {
		t.Errorf("sent = %v, want all three rows resolved by the one post", queue.sent)
	}
}

func TestWorker_AJSONWebhookReceivesEachDigestDocument(t *testing.T) {
	queue := &fakeQueueStore{digests: [][]notification.Notification{digestRows("events", "A", "B"), nil}}
	snd := &recordingSender{}
	w := channelWorker(t, queue, &fakeSettingsStore{}, channels(
		notification.Channel{Name: "events", Kind: notification.ChannelKindWebhook, Format: notification.ChannelFormatJSON, Enabled: true, Mode: notification.ChannelModeDaily}), snd)

	_ = w.drain(context.Background())

	if snd.count() != 2 {
		t.Fatalf("posted %d deliveries, want each document to a system receiving events", snd.count())
	}
}

func TestWorker_AnEmailChannelDocumentCarriesItsFiles(t *testing.T) {
	n := attachedRow("list")
	n.Recipient = "reader@example.com"
	queue := &fakeQueueStore{immediate: [][]notification.Notification{{n}, nil}}
	sender := &fakeSender{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)
	w.cfg.Attachments = loader()

	_ = w.drain(context.Background())

	sent := sender.sentCopy()
	if len(sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(sent))
	}
	if len(sent[0].Attachments) != 1 || sent[0].Attachments[0].Name != "chart.png" {
		t.Errorf("attachments = %+v, want chart.png", sent[0].Attachments)
	}
	if !strings.Contains(sent[0].Text, "Attachment: raw.csv https://p/gone") {
		t.Errorf("the unreadable attachment was not named by link:\n%s", sent[0].Text)
	}
}

func TestWorker_ThePurgeAlsoClearsStaleChannelKeys(t *testing.T) {
	queue := &fakeQueueStore{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, &fakeSender{})
	_ = w.drain(context.Background())
	if queue.keyPurges != 1 {
		t.Errorf("channel keys purged %d times, want once with the queue purge", queue.keyPurges)
	}
	queue.keyPurgeErr = errors.New("db down")
	w.lastPurge = w.lastPurge.Add(-2 * purgeEvery)
	_ = w.drain(context.Background())
	if queue.keyPurges != 2 {
		t.Errorf("a failing key purge was not attempted")
	}
}
