// Package notification is the domain of the platform's email notifications:
// the event model, the preference model, the two store contracts that persist
// them, and the enqueue path share and thread-comment triggers call.
//
// It is the vocabulary every other layer of the substrate is written in, and
// depends on none of them:
//
//	pkg/notification/smtp                     admin-configured mail server settings
//	internal/notification/notifyprefs         preference persistence
//	internal/notification/notifyqueue         queue persistence + LISTEN wakeup
//	internal/notification/notifyrender        branded email rendering
//	internal/notification/notifysend          SMTP transport
//	internal/notification/notifyworker        the send worker that drains the queue
//	internal/httpserver/notifyhttp            self-scoped preference REST
//	internal/httpserver/unsubhttp             no-login unsubscribe endpoint
//
// internal/platform/notifydelivery assembles them into one startable handle.
package notification

import (
	"errors"
	"time"
)

// ErrNoWork is returned by QueueStore claim methods when no due row is
// available.
var ErrNoWork = errors.New("notification: no work available")

// Notification categories. A category maps to a per-user preference toggle
// and an email template family.
const (
	// CategoryShare covers direct shares of assets, collections, and prompts.
	CategoryShare = "share"
	// CategoryComment covers thread comments and feedback events.
	CategoryComment = "comment"
	// CategoryMention covers being named in a comment with an @-mention
	// (#627). It is separate from CategoryComment so muting general thread
	// chatter still leaves a person reachable when someone addresses them
	// directly.
	CategoryMention = "mention"
	// CategoryReviewQueue covers the operator alert raised when the knowledge
	// review queue crosses its staleness threshold (#803). Unlike the three
	// above it carries no per-user toggle: the operator names its recipients
	// in the admin settings, so removing an address there is the way to stop
	// sending it. A recipient still opts out for themselves with ModeOff,
	// including through the no-login unsubscribe link every email carries.
	CategoryReviewQueue = "review_queue"
)

// Delivery modes for user preferences.
const (
	// ModeOff drops notifications at enqueue time.
	ModeOff = "off"
	// ModeImmediate queues one email per event.
	ModeImmediate = "immediate"
	// ModeDaily batches a user's events into one digest email per day.
	ModeDaily = "daily"
)

// Queue row statuses.
const (
	// StatusPending marks a row waiting to be claimed.
	StatusPending = "pending"
	// StatusSending marks a row claimed by a worker (lease via locked_until).
	StatusSending = "sending"
	// StatusSent marks a delivered row.
	StatusSent = "sent"
	// StatusFailed marks a row that exhausted its attempts.
	StatusFailed = "failed"
)

// Payload item kinds.
const (
	// KindAsset marks a shared asset.
	KindAsset = "asset"
	// KindCollection marks a shared collection.
	KindCollection = "collection"
	// KindPrompt marks a shared prompt.
	KindPrompt = "prompt"
	// KindComment marks a thread comment.
	KindComment = "comment"
	// KindFeedback marks a thread feedback event.
	KindFeedback = "feedback"
	// KindMention marks a comment that named the recipient.
	KindMention = "mention"
	// KindReviewQueue marks a knowledge review-queue staleness alert (#803).
	// Its payload carries a Review rollup instead of an item reference.
	KindReviewQueue = "review_queue"
)

// ReviewQueue is the pending-review rollup a KindReviewQueue notification
// carries. The renderer turns it into the alert's subject and body, so the
// queued row holds the numbers rather than a sentence about them.
//
// The values are the queue as the check saw it. A daily-digest recipient
// therefore reads what actually tripped the threshold, not a re-measurement
// taken when the digest happened to go out.
type ReviewQueue struct {
	// Pending is the total number of insights awaiting review.
	Pending int `json:"pending"`
	// OldestAgeDays is the age in days of the oldest pending insight.
	OldestAgeDays int `json:"oldest_age_days"`
	// StaleCount is how many pending insights are at least StaleAfterDays
	// old -- the accumulating review debt.
	StaleCount int `json:"stale_count"`
	// StaleAfterDays is the age at which a pending insight counts toward
	// StaleCount. The email states it rather than assuming the reader knows
	// the platform's staleness window.
	StaleAfterDays int `json:"stale_after_days"`
}

// Payload carries the event details a template needs to render an email.
// It is stored as the queue row's JSONB payload.
type Payload struct {
	// Kind is one of the Kind* constants.
	Kind string `json:"kind"`
	// ItemID identifies the shared or commented item.
	ItemID string `json:"item_id"`
	// ItemTitle is the human-readable name of the item.
	ItemTitle string `json:"item_title"`
	// Actor is the email of the person who shared or commented.
	Actor string `json:"actor"`
	// Message is an optional comment/feedback snippet.
	Message string `json:"message,omitempty"`
	// Link is the absolute portal deep link for the item.
	Link string `json:"link,omitempty"`
	// Review carries the review-queue rollup of a KindReviewQueue alert and
	// is nil for every other kind.
	Review *ReviewQueue `json:"review,omitempty"`
}

// Notification is one queued delivery.
type Notification struct {
	ID           int64      `json:"id"`
	Recipient    string     `json:"recipient"`
	Category     string     `json:"category"`
	Payload      Payload    `json:"payload"`
	Digest       bool       `json:"digest"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	LastError    string     `json:"last_error,omitempty"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	SentAt       *time.Time `json:"sent_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}
