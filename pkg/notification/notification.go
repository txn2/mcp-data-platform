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
	// CategoryScriptRun covers the alert raised when a SCHEDULED managed-script
	// run fails (#1286). Like the review-queue alert it carries no per-user
	// toggle, and for a stronger reason: its recipient is the person
	// accountable for the automation — the script's owner — so it is addressed
	// to a responsibility rather than to an interest. ModeOff, including
	// through the unsubscribe link, remains the recipient's own opt-out.
	//
	// A run somebody asked for through run_script never comes here: that
	// failure is reported to the caller in the tool's own response, and mailing
	// it as well would notify a person about something they are already
	// reading.
	CategoryScriptRun = "script_run"
	// CategoryConnectionAuth covers the alert raised when an upstream rejects a
	// connection's refresh and the platform discards the credential (#1694).
	// Like the two above it carries no per-user toggle, for the script-run
	// reason: its first recipient is the person who authorized the connection,
	// which is a responsibility and not an interest, and its second is whoever
	// the operator named to hear about a connection nobody has come back to.
	// ModeOff, including through the unsubscribe link, remains each
	// recipient's own opt-out.
	//
	// This is the one auth surface where the person holding the upstream
	// credential is deliberately not the person using the connection day to
	// day, which is exactly why that person will not be watching the status
	// card that already reports it.
	CategoryConnectionAuth = "connection_auth"
	// CategoryChannel covers a document sent to an operator-configured
	// channel (#1720): a script's monitor post, a published report, a
	// message a person asked the agent to send. Like the three above it
	// carries no per-user toggle, and for a third reason: its recipient is
	// usually not a person at all but a chat channel, and where it IS a
	// person — an address on an email channel's list — the operator named
	// them, so removing the address is the way to stop sending. ModeOff,
	// including through the unsubscribe link, remains that person's own
	// opt-out.
	CategoryChannel = "channel"
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
	// KindScriptRun marks a failed scheduled script run (#1286). Its payload
	// names the script in ItemTitle, the run in ItemID, and carries the failure
	// and the tail of what the script printed in Message.
	KindScriptRun = "script_run"
	// KindConnectionAuth marks a connection whose credential was discarded
	// (#1694). Its payload carries a ConnectionAuth describing the revocation
	// instead of an item reference.
	KindConnectionAuth = "connection_auth"
	// KindChannel marks a document addressed to a channel (#1720). Its
	// payload carries a Document instead of an item reference, and the row's
	// Channel names the destination.
	KindChannel = "channel"
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

// ConnectionAuth is the revocation a KindConnectionAuth notification reports:
// which connection lost its credential, which upstream rejected it, what the
// upstream said, and when.
//
// The queued row holds the revocation as the platform saw it rather than a
// sentence about it, for the ReviewQueue reason: a digest recipient reads what
// actually happened, not a re-measurement taken when the digest went out. Here
// there is a second reason — by the time the mail is rendered the token row has
// been deleted, so nothing could be re-read even if the renderer wanted to.
type ConnectionAuth struct {
	// Kind is the connection kind (mcp, api, graphql). With Name it is how a
	// recipient finds the connection, and the two are rendered together rather
	// than as one joined string so the email can link to it.
	Kind string `json:"kind"`
	// Name is the connection name within the kind.
	Name string `json:"name"`
	// IDPHost is the host of the upstream token endpoint that rejected the
	// refresh. Empty when the platform decided locally (see Reason).
	IDPHost string `json:"idp_host,omitempty"`
	// Reason is what the upstream returned, in the stable short form the auth
	// event history records: an RFC 6749 error code such as invalid_grant or
	// invalid_client when the upstream answered, and no_refresh_token or
	// refresh_expired when the platform reached the verdict without calling
	// it. The email states which of those two it was, because they ask the
	// recipient for different things.
	Reason string `json:"reason,omitempty"`
	// Description is the upstream's error_description, bounded by the
	// platform. Carried for a refused jwt_bearer assertion, where it is
	// usually the only statement of which upstream approval is missing.
	Description string `json:"description,omitempty"`
	// SignedAssertion marks a refused jwt_bearer assertion rather than a
	// revoked authorization (#1734). The email then says there is nothing to
	// reconnect and sends the reader to the upstream: the key, the integration
	// user or the clocks, and it says the alert clears itself when an exchange
	// is next accepted.
	SignedAssertion bool `json:"signed_assertion,omitempty"`
	// AuthorizedBy is the identity that authorized the connection, and the
	// address the first alert is sent to. It is carried in the payload as well
	// so the escalation to the operator's chosen recipients can say whose
	// authorization lapsed.
	AuthorizedBy string `json:"authorized_by,omitempty"`
	// RevokedAt is when the credential was discarded.
	RevokedAt time.Time `json:"revoked_at,omitzero"`
	// Escalated marks the second alert: the connection was still unauthorized
	// after the operator's escalation window, so it went to the addresses the
	// operator named rather than to the person who authorized it. The two
	// alerts carry the same revocation and differ only in who is being asked
	// to act, which is what this field lets the email say.
	Escalated bool `json:"escalated,omitempty"`
	// EscalatedAfterHours is the window that elapsed before the escalation was
	// raised. Zero on the first alert.
	EscalatedAfterHours int `json:"escalated_after_hours,omitempty"`
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
	// Connection carries the revocation a KindConnectionAuth alert reports and
	// is nil for every other kind.
	Connection *ConnectionAuth `json:"connection,omitempty"`
	// Document carries what a KindChannel row delivers and is nil for every
	// other kind. It holds the message rather than a reference to one
	// because a channel document has no platform record behind it to re-read
	// at send time: the sender composed it, and what was composed is what
	// must arrive.
	Document *Document `json:"document,omitempty"`
}

// Notification is one queued delivery.
type Notification struct {
	ID        int64  `json:"id"`
	Recipient string `json:"recipient"`
	Category  string `json:"category"`
	// Channel names the destination a KindChannel row was sent to, and is
	// empty for every row addressed to a person alone. It is a column rather
	// than a payload field because the worker claims on it (a chat row is
	// deliverable with no mail server configured) and the admin history
	// filters on it.
	Channel      string     `json:"channel,omitempty"`
	Payload      Payload    `json:"payload"`
	Digest       bool       `json:"digest"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	LastError    string     `json:"last_error,omitempty"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	SentAt       *time.Time `json:"sent_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}
