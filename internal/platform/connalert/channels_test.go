package connalert

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyalert"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

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

// channelHarness is the alerter with its alerts also routed to channels.
func channelHarness(t *testing.T, settings Settings) (*Alerter, *recordingQueue) {
	t.Helper()
	queue := &recordingQueue{}
	enq := notification.NewEnqueuer(openPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	a := NewAlerter(Config{
		Settings: stubSettings{settings: &settings},
		Alerts:   newFakeAlerts(),
		Enqueuer: enq,
		Channels: notifyalert.New(opsChannels{}, enq),
		BaseURL:  "https://platform.example.com",
	})
	require.NotNil(t, a)
	return a, queue
}

func channelRows(rows []queued) []queued {
	out := []queued{}
	for _, r := range rows {
		if r.recipient == "channel:ops" {
			out = append(out, r)
		}
	}
	return out
}

func TestAlerter_ARevocationPostsToTheChannelsUnderTheConnectionsKey(t *testing.T) {
	settings := DefaultSettings()
	settings.Channels = []string{"ops"}
	a, queue := channelHarness(t, settings)

	a.Revoked(t.Context(), revocation())

	rows := queue.all()
	posts := channelRows(rows)
	require.Len(t, posts, 1, "the channel is told beside the person who authorized it")
	doc := posts[0].payload.Document
	require.NotNil(t, doc)
	assert.Equal(t, "connection:api/billing", doc.Key)
	assert.Equal(t, "https://platform.example.com/portal/admin/connections", doc.Link)
	assert.Contains(t, doc.Title, "billing")
	assert.Len(t, rows, 2, "the person who authorized it still gets their email")
}

func TestAlerter_AnUnattributedRevocationStillReachesTheChannels(t *testing.T) {
	settings := DefaultSettings()
	settings.Channels = []string{"ops"}
	a, queue := channelHarness(t, settings)
	rev := revocation()
	rev.AuthorizedBy = ""

	a.Revoked(t.Context(), rev)

	assert.Len(t, channelRows(queue.all()), 1)
	assert.Len(t, queue.all(), 1, "nobody is emailed when nobody is known to have authorized it")
}

func TestAlerter_ARefusedAssertionWithOnlyChannelsIsAnnounced(t *testing.T) {
	settings := DefaultSettings()
	settings.Channels = []string{"ops"}
	a, queue := channelHarness(t, settings)
	rev := revocation()
	rev.SignedAssertion, rev.AuthorizedBy = true, ""

	a.Revoked(t.Context(), rev)

	assert.Len(t, channelRows(queue.all()), 1)
}

func TestEscalator_EscalatesToChannelsWithNoRecipients(t *testing.T) {
	settings := DefaultSettings()
	settings.Channels = []string{"ops"}
	a, queue := channelHarness(t, settings)
	alerts, ok := a.cfg.Alerts.(*fakeAlerts)
	require.True(t, ok)
	alerts.claimed = []Alert{{Kind: "api", Name: "billing", RevokedAt: time.Now().Add(-48 * time.Hour)}}
	esc := NewEscalator(a.cfg)
	require.NotNil(t, esc)

	require.NoError(t, esc.Sweep(t.Context()))

	posts := channelRows(queue.all())
	require.Len(t, posts, 1, "a revocation nobody acted on is raised in the channel")
	assert.Equal(t, `The connection "billing" is still unauthorized`, posts[0].payload.Document.Title)
}
