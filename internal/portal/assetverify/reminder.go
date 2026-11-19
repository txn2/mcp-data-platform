package assetverify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// ReminderLead is how long before a verification expires its reminder is
// sent.
const ReminderLead = 7 * 24 * time.Hour

// reminderEvery is how often the reminder pass looks for verifications owed
// one. The claim reads a partial index of unreminded rows with an expiry, so
// the pass is cheap; running it often is what lets a verification written
// with a near expiry be reminded promptly.
const reminderEvery = time.Minute

// reminderBatch bounds the reminders one pass claims.
const reminderBatch = 100

// Notifier queues one notification.
type Notifier interface {
	Notify(ctx context.Context, recipient, category string, p notification.Payload) (bool, error)
}

// Reminder tells the person who verified an asset and its owner, once, that
// the verification expires within ReminderLead.
type Reminder struct {
	Store    Store
	Assets   portaldomain.AssetStore
	Notifier Notifier
	// CanOpen reports whether the person with this address can still open
	// the asset; a verifier who cannot is not reminded, so the reminder
	// tells no one about an asset they lost.
	CanOpen func(ctx context.Context, asset *portaldomain.Asset, email string) bool
	// BaseURL is the portal's public address, for the link.
	BaseURL string
	// Lead is how far ahead a reminder is sent; zero is ReminderLead.
	Lead time.Duration
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Run runs the reminder pass until ctx ends.
func (r *Reminder) Run(ctx context.Context) {
	bgloop.Run(ctx, bgloop.Loop{Name: bgloop.NameVerificationReminders, Every: reminderEvery, Body: r.Pass})
}

// Pass claims the verifications owed a reminder and sends each one.
func (r *Reminder) Pass(ctx context.Context) error {
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	lead := r.Lead
	if lead <= 0 {
		lead = ReminderLead
	}
	due, err := r.Store.ClaimDue(ctx, now, now.Add(lead), reminderBatch)
	if err != nil {
		return err //nolint:wrapcheck // ClaimDue names the step
	}
	for _, d := range due {
		r.remind(ctx, d)
	}
	return nil
}

// remind sends one claimed reminder to the verifier and the owner.
func (r *Reminder) remind(ctx context.Context, d Due) {
	asset, err := r.Assets.Get(ctx, d.AssetID)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		return
	}
	payload := notification.Payload{
		Kind:      notification.KindVerificationExpiring,
		ItemID:    asset.ID,
		ItemTitle: asset.Name,
		Message: fmt.Sprintf("The Verified badge on %q expires on %s. Verify it again to keep the badge.",
			asset.Name, d.ExpiresAt.UTC().Format(time.DateOnly)),
	}
	if r.BaseURL != "" {
		payload.Link = strings.TrimRight(r.BaseURL, "/") + "/portal/assets/" + asset.ID
	}
	for _, to := range r.recipients(ctx, asset, d.VerifiedBy) {
		if _, err := r.Notifier.Notify(ctx, to, notification.CategoryVerification, payload); err != nil {
			slog.Warn("verification reminder not queued", // #nosec G706 -- structured slog call; values sanitized
				"asset", logsan.SanitizeForLog(asset.ID), "error", logsan.SanitizeForLog(err.Error()))
		}
	}
}

// recipients is the owner and, when they can still open the asset, the
// person who verified it, each once.
func (r *Reminder) recipients(ctx context.Context, asset *portaldomain.Asset, verifier string) []string {
	out := []string{}
	if asset.OwnerEmail != "" {
		out = append(out, asset.OwnerEmail)
	}
	if verifier == "" || strings.EqualFold(verifier, asset.OwnerEmail) {
		return out
	}
	if r.CanOpen == nil || r.CanOpen(ctx, asset, verifier) {
		out = append(out, verifier)
	}
	return out
}
