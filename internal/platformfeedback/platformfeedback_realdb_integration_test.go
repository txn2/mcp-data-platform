//go:build integration

package platformfeedback

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/secretscan"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// recordingSender records what a post queued, as the notification queue would.
type recordingSender struct{ docs []notification.Document }

func (r *recordingSender) NotifyChannel(_ context.Context, s notification.ChannelSend) (notification.ChannelResult, error) {
	r.docs = append(r.docs, s.Document)
	return notification.ChannelResult{Queued: 1}, nil
}

// oneChannel is a channel store holding one webhook channel.
type oneChannel struct{ notification.ChannelStore }

func (oneChannel) Get(_ context.Context, name string) (*notification.Channel, error) {
	if name != "ops-webhook" {
		return nil, errors.New("channel not found")
	}
	return &notification.Channel{Name: name, Kind: notification.ChannelKindWebhook}, nil
}

// TestService_RealDB_PostFoldListAndRefuse is #2820 against the real schema:
// a post is stored and sent to the channel the settings name; three
// near-duplicates are one post counted three times and sent once; a refused
// post leaves its reason and never its text; the list, a post's reports and
// its status read back as written.
func TestService_RealDB_PostFoldListAndRefuse(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sender := &recordingSender{}
	section := settingsjson.New[Settings](db, SectionName)
	svc := New(Deps{
		DB: db, Section: section, Sender: sender, Channels: oneChannel{},
		Detector: secretscan.Detector{Known: func(context.Context) ([]string, error) { return []string{"planted-secret-0001"}, nil }},
		// Every post's text is embedded the same, so a repeat folds by embedding.
		Embed: func(context.Context, string) ([]float32, error) { return []float32{1, 0, 0}, nil },
	})
	_, err := svc.SaveSettings(ctx, Settings{Enabled: true, Channels: []string{"ops-webhook"}}, "admin@example.com")
	require.NoError(t, err)

	caller := Caller{Persona: "analyst", UserID: "u1", SessionID: "dps_session1"}
	first, err := svc.Submit(ctx, caller, validPost())
	require.NoError(t, err)
	assert.False(t, first.Folded)
	assert.Equal(t, 1, first.Channels)
	require.Len(t, sender.docs, 1)
	var data map[string]any
	require.NoError(t, json.Unmarshal(sender.docs[0].Data, &data))
	assert.Equal(t, "list the assets shared with me", data["trying_to"])

	for range 2 {
		again := validPost()
		again.TryingTo = "show the assets other people shared with me"
		got, err := svc.Submit(ctx, caller, again)
		require.NoError(t, err)
		assert.True(t, got.Folded)
		assert.Equal(t, first.ID, got.ID)
	}
	assert.Len(t, sender.docs, 1, "a repeat is counted, not sent again")

	page, err := svc.List(ctx, ListFilter{})
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	assert.Equal(t, 3, page.Posts[0].ReportCount)
	assert.Equal(t, 1, page.Stats.Open)
	assert.Equal(t, 3, page.Stats.ReportsThisWeek)

	detail, err := svc.Get(ctx, first.ID)
	require.NoError(t, err)
	assert.Len(t, detail.Reports, 3)
	assert.Equal(t, []string{"mcp:call:abc123"}, detail.Reports[0].Calls)
	assert.Equal(t, []Delivery{}, detail.Deliveries, "the recording sender wrote no queue row")

	_, err = svc.Submit(ctx, caller, Post{TryingTo: "x", BlockedBy: "it printed planted-secret-0001"})
	var refusal *RefusedError
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, CodeSecret, refusal.Code)
	var leaked int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM platform_feedback WHERE trying_to || blocked_by || suggestion LIKE '%planted-secret%')
		     + (SELECT COUNT(*) FROM platform_feedback_refusals WHERE reason LIKE '%planted%')`).Scan(&leaked))
	assert.Zero(t, leaked, "a refused post left its text behind")
	page, err = svc.List(ctx, ListFilter{})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Stats.RefusedThisWeek)

	require.NoError(t, svc.SetStatus(ctx, first.ID, StatusPlanned, "admin@example.com"))
	planned, err := svc.List(ctx, ListFilter{Status: StatusPlanned, Query: "shared"})
	require.NoError(t, err)
	require.Len(t, planned.Posts, 1)
	assert.ErrorIs(t, svc.SetStatus(ctx, "pfb_none", StatusDone, "a"), ErrNotFound)
	_, err = svc.Get(ctx, "pfb_none")
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, svc.Resend(ctx, first.ID, "ops-webhook"))
	require.Len(t, sender.docs, 2)
	assert.True(t, strings.HasPrefix(sender.docs[1].Key, keyPrefix+first.ID+":resend:"))
	var inv *InvalidError
	assert.ErrorAs(t, svc.Resend(ctx, first.ID, "other"), &inv)

	// Past the session limit of 5: the three above plus two more.
	for range 2 {
		p := validPost()
		p.TryingTo = "a distinct task " + strings.Repeat("z", len(sender.docs))
		svc.d.Embed = nil
		_, err := svc.Submit(ctx, caller, p)
		require.NoError(t, err)
	}
	_, err = svc.Submit(ctx, caller, Post{TryingTo: "one more", BlockedBy: "y"})
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, CodeLimit, refusal.Code)
}
