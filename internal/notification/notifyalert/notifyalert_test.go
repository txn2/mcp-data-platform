package notifyalert

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/notification/notifychannel"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

type channels map[string]notification.Channel

func (channels) List(context.Context) ([]notification.Channel, error) { return nil, nil }
func (c channels) Get(_ context.Context, name string) (*notification.Channel, error) {
	if name == "broken" {
		return nil, errors.New("db down")
	}
	ch, ok := c[name]
	if !ok {
		return nil, notifychannel.ErrChannelNotFound
	}
	return &ch, nil
}
func (channels) Set(context.Context, notification.Channel) error { return nil }
func (channels) Delete(context.Context, string) error            { return nil }

// queue records the gates and rows of each channel send.
type queue struct {
	rows  []notification.Notification
	gates []notification.ChannelGate
}

func (*queue) Enqueue(context.Context, notification.Notification) error { return nil }
func (q *queue) EnqueueChannel(_ context.Context, rows []notification.Notification, g notification.ChannelGate) (notification.ChannelOutcome, error) {
	q.rows = append(q.rows, rows...)
	q.gates = append(q.gates, g)
	return notification.ChannelOutcome{}, nil
}

func (*queue) ClaimImmediate(context.Context, time.Duration, notification.TransportFilter) (*notification.Notification, error) {
	return nil, notification.ErrNoWork
}

func (*queue) ClaimDigest(context.Context, time.Duration, notification.TransportFilter) ([]notification.Notification, error) {
	return nil, notification.ErrNoWork
}
func (*queue) MarkSent(context.Context, []int64) error                     { return nil }
func (*queue) Retry(context.Context, []int64, string, time.Duration) error { return nil }
func (*queue) Fail(context.Context, []int64, string) error                 { return nil }
func (*queue) PurgeOld(context.Context, time.Duration, time.Duration) (int64, error) {
	return 0, nil
}
func (*queue) PurgeChannelKeys(context.Context, time.Duration) (int64, error) { return 0, nil }

type prefs struct{}

func (prefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

func (prefs) Set(_ context.Context, email string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

func fixture(t *testing.T) (*Sender, *queue) {
	t.Helper()
	q := &queue{}
	e := notification.NewEnqueuer(prefs{}, q, 13)
	t.Cleanup(e.Close)
	chs := channels{
		"ops":  {Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true, Mode: notification.ChannelModeImmediate, Connection: "c", Target: "T"},
		"off":  {Name: "off", Kind: notification.ChannelKindMattermost, Enabled: false, Mode: notification.ChannelModeImmediate, Connection: "c", Target: "T"},
		"list": {Name: "list", Kind: notification.ChannelKindEmail, Enabled: true, Mode: notification.ChannelModeImmediate, Recipients: []string{"a@example.com"}},
	}
	return New(chs, e), q
}

func TestSend_PostsTheAlertUnderItsKeyToEachChannel(t *testing.T) {
	s, q := fixture(t)
	p := notification.Payload{
		Kind: notification.KindConnectionAuth, ItemTitle: "crm", Link: "https://p/portal/admin/connections",
		Connection: &notification.ConnectionAuth{Kind: "api", Name: "crm"},
	}
	n := s.Send(context.Background(), []string{"ops", "gone", "off", "broken", "list"}, "connection:api/crm", p)
	if n != 2 {
		t.Fatalf("queued for %d channels, want ops and list", n)
	}
	for _, g := range q.gates {
		if g.Key != "connection:api/crm" {
			t.Errorf("gate key = %q", g.Key)
		}
	}
	doc := q.rows[0].Payload.Document
	if doc.Title == "" || doc.Link != "https://p/portal/admin/connections" || doc.Key != "connection:api/crm" {
		t.Errorf("document = %+v, want the alert's subject, link and key", doc)
	}
	if q.rows[0].Payload.Actor != "" {
		t.Error("an alert was charged to an actor")
	}
}

func TestSend_NothingToDo(t *testing.T) {
	var nilSender *Sender
	if nilSender.Send(context.Background(), []string{"ops"}, "k", notification.Payload{}) != 0 {
		t.Error("a nil sender sent")
	}
	s, q := fixture(t)
	if s.Send(context.Background(), nil, "k", notification.Payload{}) != 0 || len(q.rows) != 0 {
		t.Error("an empty channel list sent")
	}
	if New(nil, nil) != nil {
		t.Error("New without dependencies built a sender")
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize([]string{" ops ", "", "ops", "pager", "  "})
	if strings.Join(got, ",") != "ops,pager" {
		t.Errorf("Normalize = %v", got)
	}
	if got := Normalize(nil); got == nil || len(got) != 0 {
		t.Errorf("Normalize(nil) = %#v, want an empty list", got)
	}
}

func TestValidate(t *testing.T) {
	chs := channels{"ops": {Name: "ops"}, "off": {Name: "off", Enabled: false}}
	ctx := context.Background()
	if err := Validate(ctx, chs, []string{"ops", "off"}); err != nil {
		t.Errorf("existing channels refused: %v", err)
	}
	if err := Validate(ctx, chs, nil); err != nil {
		t.Errorf("no channels refused: %v", err)
	}
	if err := Validate(ctx, nil, nil); err != nil {
		t.Errorf("no channels and no store refused: %v", err)
	}
	if err := Validate(ctx, chs, []string{"gone"}); !errors.Is(err, ErrUnknownChannel) || !strings.Contains(err.Error(), `"gone"`) {
		t.Errorf("unknown channel: %v", err)
	}
	if err := Validate(ctx, chs, []string{"broken"}); err == nil || errors.Is(err, ErrUnknownChannel) {
		t.Errorf("a read failure read as %v", err)
	}
	if err := Validate(ctx, nil, []string{"ops"}); err == nil {
		t.Error("a channel was accepted with no store")
	}
	many := make([]string, MaxChannels+1)
	if err := Validate(ctx, chs, many); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("too many: %v", err)
	}
}
