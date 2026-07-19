// Package notification is the platform's email-notification substrate: a
// typed admin SMTP settings store, per-user notification preferences, a
// durable delivery queue, and the enqueue path consulted by share and
// thread-comment triggers. Delivery (worker + SMTP send + branded
// rendering) also lives here; the platform wires it through the
// internal/platform/notifydelivery seam.
package notification

import (
	"errors"
	"time"
)

// Sentinel errors returned by the stores in this package.
var (
	// ErrNotFound is returned when a requested row does not exist.
	ErrNotFound = errors.New("notification: not found")
	// ErrNoWork is returned by claim methods when no due row is available.
	ErrNoWork = errors.New("notification: no work available")
	// ErrSMTPNotConfigured is returned by delivery actions when SMTP is
	// absent, disabled, or missing a host; the caller should surface it as
	// a configuration conflict, not a delivery failure.
	ErrSMTPNotConfigured = errors.New("notification: smtp is disabled or not configured")
)

// Notification categories. A category maps to a per-user preference toggle
// and an email template family.
const (
	// CategoryShare covers direct shares of assets, collections, and prompts.
	CategoryShare = "share"
	// CategoryComment covers thread comments and feedback events.
	CategoryComment = "comment"
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
)

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
