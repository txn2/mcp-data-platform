package reviewalert

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyalert"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// EnqueueChannel records a channel send's rows, as the real store writes them.
func (q *captureQueue) EnqueueChannel(ctx context.Context, rows []notification.Notification, _ notification.ChannelGate) (notification.ChannelOutcome, error) {
	for _, n := range rows {
		if err := q.Enqueue(ctx, n); err != nil {
			return notification.ChannelOutcome{}, err
		}
	}
	return notification.ChannelOutcome{}, nil
}

// opsChannels serves one chat channel named ops.
type opsChannels struct{}

func (opsChannels) List(context.Context) ([]notification.Channel, error) { return nil, nil }
func (opsChannels) Get(_ context.Context, name string) (*notification.Channel, error) {
	if name != "ops" {
		return nil, errors.New("not found")
	}
	return &notification.Channel{
		Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: "c", Target: "T",
	}, nil
}
func (opsChannels) Set(context.Context, notification.Channel) error { return nil }
func (opsChannels) Delete(context.Context, string) error            { return nil }

func TestCheck_ACrossedQueuePostsToTheChannelsUnderTheQueuesKey(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	oldest := now.AddDate(0, 0, -94)
	queue := &captureQueue{}
	enq := notification.NewEnqueuer(defaultPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	settings := Settings{Enabled: true, OldestPendingDays: 30, CooldownHours: 24, Channels: []string{"ops"}}
	checker := New(Config{
		Target:   KnowledgeTarget(),
		Settings: &stubSettings{settings: &settings},
		State:    &memState{},
		Source:   InsightSource{Insights: &fakeInsights{review: &knowledgekit.PendingReview{TotalPending: 12, OldestPendingAt: &oldest}}},
		Enqueuer: enq,
		Channels: notifyalert.New(opsChannels{}, enq),
		BaseURL:  "https://data.example.com",
		Now:      func() time.Time { return now },
	})
	require.NotNil(t, checker)

	require.NoError(t, checker.Check(context.Background()))

	rows := queue.snapshot()
	require.Len(t, rows, 1, "a channel alone makes the alert deliverable")
	assert.Equal(t, "channel:ops", rows[0].Recipient)
	doc := rows[0].Payload.Document
	require.NotNil(t, doc)
	assert.Equal(t, "knowledge_review", doc.Key)
	assert.Equal(t, "https://data.example.com/portal/knowledge#review", doc.Link)
	assert.NotEmpty(t, doc.Title)
	assert.NotEmpty(t, doc.Body)
}

func TestSettings_ChannelsAloneAreDeliverable(t *testing.T) {
	s := Settings{Enabled: true, OldestPendingDays: 30, Channels: []string{"ops"}}
	assert.True(t, s.Deliverable())
	assert.Empty(t, s.View().Warnings)
	assert.Equal(t, []string{"ops"}, s.View().Channels)
	assert.Equal(t, []string{}, Settings{}.View().Channels)
	assert.Contains(t, Settings{Enabled: true, OldestPendingDays: 30}.View().Warnings, NoRecipientsWarning)

	names := []string{"ops", " ops", ""}
	in := SettingsInput{Channels: &names}
	require.Empty(t, in.Validate())
	assert.Equal(t, []string{"ops"}, in.Settings().Channels)
	many := make([]string, notifyalert.MaxChannels+1)
	for i := range many {
		many[i] = string(rune('a' + i))
	}
	assert.Contains(t, (&SettingsInput{Channels: &many}).Validate(), "at most")
	assert.Equal(t, []string{}, (&SettingsInput{}).Settings().Channels)
}
