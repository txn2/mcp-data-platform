package assetverify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// claimStore records the window a pass claims over and hands back due rows.
type claimStore struct {
	fakeVerify
	due          []Due
	err          error
	now, horizon time.Time
	limit        int
}

func (c *claimStore) ClaimDue(_ context.Context, now, horizon time.Time, limit int) ([]Due, error) {
	c.now, c.horizon, c.limit = now, horizon, limit
	return c.due, c.err
}

type sent struct {
	to       string
	category string
	payload  notification.Payload
}

type recordingNotifier struct {
	sent []sent
	err  error
}

func (r *recordingNotifier) Notify(_ context.Context, to, category string, p notification.Payload) (bool, error) {
	r.sent = append(r.sent, sent{to, category, p})
	return r.err == nil, r.err
}

func (r *recordingNotifier) recipients() []string {
	out := make([]string, 0, len(r.sent))
	for _, s := range r.sent {
		out = append(out, s.to)
	}
	return out
}

// reminderRig is a Reminder over fakes and the fakes it holds.
type reminderRig struct {
	r        *Reminder
	store    *claimStore
	notifier *recordingNotifier
	assets   *fakeAssets
}

func reminderFixture(due ...Due) reminderRig {
	assets := &fakeAssets{rows: map[string]*portaldomain.Asset{
		"a1": {ID: "a1", Name: "Q3 revenue", OwnerEmail: ownerEmail},
	}}
	store := &claimStore{due: due}
	notifier := &recordingNotifier{}
	r := &Reminder{
		Store: store, Assets: assets, Notifier: notifier, BaseURL: "https://portal.example.com/",
		CanOpen: func(context.Context, *portaldomain.Asset, string) bool { return true },
		Now:     func() time.Time { return fixedNow },
	}
	return reminderRig{r: r, store: store, notifier: notifier, assets: assets}
}

func TestPassClaimsTheLeadWindowAndRemindsOwnerAndVerifier(t *testing.T) {
	exp := fixedNow.Add(5 * 24 * time.Hour)
	rig := reminderFixture(Due{AssetID: "a1", VerifiedBy: verifierEmail, ExpiresAt: exp})
	r, store, notifier := rig.r, rig.store, rig.notifier
	require.NoError(t, r.Pass(context.Background()))

	assert.Equal(t, fixedNow, store.now)
	assert.Equal(t, fixedNow.Add(ReminderLead), store.horizon, "a zero Lead is the week before")
	assert.Equal(t, reminderBatch, store.limit)
	assert.Equal(t, []string{ownerEmail, verifierEmail}, notifier.recipients())
	for _, s := range notifier.sent {
		assert.Equal(t, notification.CategoryVerification, s.category)
		assert.Equal(t, notification.KindVerificationExpiring, s.payload.Kind)
		assert.Equal(t, "a1", s.payload.ItemID)
		assert.Equal(t, "Q3 revenue", s.payload.ItemTitle)
		assert.Equal(t, "https://portal.example.com/portal/assets/a1", s.payload.Link)
		assert.Contains(t, s.payload.Message, exp.Format(time.DateOnly))
	}
}

func TestPassHonorsAnInjectedLead(t *testing.T) {
	rig := reminderFixture()
	r, store := rig.r, rig.store
	r.Lead = time.Hour
	require.NoError(t, r.Pass(context.Background()))
	assert.Equal(t, fixedNow.Add(time.Hour), store.horizon)
}

func TestPassSkipsAVerifierWhoCannotOpenTheAsset(t *testing.T) {
	rig := reminderFixture(Due{AssetID: "a1", VerifiedBy: verifierEmail, ExpiresAt: fixedNow.Add(time.Hour)})
	r, notifier := rig.r, rig.notifier
	r.CanOpen = func(_ context.Context, _ *portaldomain.Asset, email string) bool { return email != verifierEmail }
	require.NoError(t, r.Pass(context.Background()))
	assert.Equal(t, []string{ownerEmail}, notifier.recipients(), "a verifier who lost access is told nothing")
}

func TestPassRemindsAVerifierWhoIsTheOwnerOnce(t *testing.T) {
	rig := reminderFixture(Due{AssetID: "a1", VerifiedBy: strings.ToUpper(ownerEmail), ExpiresAt: fixedNow.Add(time.Hour)})
	r, notifier := rig.r, rig.notifier
	require.NoError(t, r.Pass(context.Background()))
	assert.Equal(t, []string{ownerEmail}, notifier.recipients())
}

func TestPassSkipsADeletedOrMissingAsset(t *testing.T) {
	rig := reminderFixture(
		Due{AssetID: "a1", VerifiedBy: verifierEmail, ExpiresAt: fixedNow.Add(time.Hour)},
		Due{AssetID: "missing", VerifiedBy: verifierEmail, ExpiresAt: fixedNow.Add(time.Hour)},
	)
	r, notifier, assets := rig.r, rig.notifier, rig.assets
	deleted := fixedNow
	assets.rows["a1"].DeletedAt = &deleted
	require.NoError(t, r.Pass(context.Background()))
	assert.Empty(t, notifier.sent)
}

func TestPassWithNoOwnerAddressRemindsTheVerifier(t *testing.T) {
	rig := reminderFixture(Due{AssetID: "a1", VerifiedBy: verifierEmail, ExpiresAt: fixedNow.Add(time.Hour)})
	r, notifier, assets := rig.r, rig.notifier, rig.assets
	assets.rows["a1"].OwnerEmail = ""
	r.CanOpen = nil
	r.BaseURL = ""
	require.NoError(t, r.Pass(context.Background()))
	require.Equal(t, []string{verifierEmail}, notifier.recipients())
	assert.Empty(t, notifier.sent[0].payload.Link, "no base URL, no link")
}

func TestPassKeepsGoingWhenANotificationFails(t *testing.T) {
	rig := reminderFixture(Due{AssetID: "a1", VerifiedBy: verifierEmail, ExpiresAt: fixedNow.Add(time.Hour)})
	r, notifier := rig.r, rig.notifier
	notifier.err = errors.New("queue full")
	require.NoError(t, r.Pass(context.Background()))
	assert.Len(t, notifier.sent, 2, "a failed enqueue for one recipient does not stop the next")
}

func TestPassReportsAFailedClaim(t *testing.T) {
	rig := reminderFixture()
	r, store, notifier := rig.r, rig.store, rig.notifier
	store.err = errors.New("db down")
	require.Error(t, r.Pass(context.Background()))
	assert.Empty(t, notifier.sent)
}

func TestPassDefaultsTheClock(t *testing.T) {
	rig := reminderFixture()
	r, store := rig.r, rig.store
	r.Now = nil
	before := time.Now()
	require.NoError(t, r.Pass(context.Background()))
	assert.False(t, store.now.Before(before))
}

func TestRunStopsWithItsContext(t *testing.T) {
	rig := reminderFixture()
	r := rig.r
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}
