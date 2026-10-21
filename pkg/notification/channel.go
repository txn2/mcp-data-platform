package notification

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

// Channel kinds. A kind decides what a channel needs to deliver and what a
// document may carry through it.
const (
	// ChannelKindSlack posts through the Slack Web API (chat.postMessage,
	// and the external upload sequence for files) with the bot token held by
	// the connection.
	ChannelKindSlack = "slack"
	// ChannelKindMattermost posts through the Mattermost REST API
	// (POST /api/v4/posts) with the bot token held by the connection.
	ChannelKindMattermost = "mattermost"
	// ChannelKindWebhook posts one {"text": ...} body to an incoming-webhook
	// URL, the shape Slack and Mattermost both accept. It carries no channel
	// choice and no files: the webhook's own configuration decides where the
	// message lands.
	ChannelKindWebhook = "webhook"
	// ChannelKindEmail delivers to an operator-named recipient list through
	// the existing SMTP path. It is the one kind that names no connection,
	// because its transport is the deployment's mail server rather than an
	// upstream a persona is allowed or denied.
	ChannelKindEmail = "email"
)

// Channel delivery modes. The channel's mode governs; a sender does not
// override it, so a report that must arrive at once goes to an immediate
// channel rather than asking a daily one to make an exception.
const (
	// ChannelModeImmediate delivers each document as it is enqueued.
	ChannelModeImmediate = "immediate"
	// ChannelModeDaily collects a day's documents into one bulletin
	// delivered in the deployment's digest window.
	ChannelModeDaily = "daily"
)

// Webhook channel payload formats (#1997). Only the webhook kind has a
// choice: the chat kinds post in their API's shape and email renders a
// message.
const (
	// ChannelFormatText posts {"text": ...}, the incoming-webhook shape Slack
	// and Mattermost accept. The default, so a channel configured before
	// formats existed delivers as it did.
	ChannelFormatText = "text"
	// ChannelFormatJSON posts one envelope per notification for a system
	// to receive: an id stable across retries, a type to route on, the
	// source, and the sender's structured data.
	ChannelFormatJSON = "json"
)

// Document types: what a channel document is, which a JSON envelope carries
// as its type for a receiver to route on (#1997).
const (
	// DocumentScriptFinding is a managed script's platform.notify.
	DocumentScriptFinding = "script.finding"
	// DocumentScriptPublished is a managed script's platform.publish.
	DocumentScriptPublished = "script.published"
	// DocumentNotifySent is a person's notify action=send.
	DocumentNotifySent = "notify.sent"
	// DocumentNotifyPublished is a person's notify action=publish.
	DocumentNotifyPublished = "notify.published"
	// DocumentTest is an administrator's test send.
	DocumentTest = "test"
)

// Document source kinds.
const (
	DocumentSourceScript = "script"
	DocumentSourceUser   = "user"
	DocumentSourceAdmin  = "admin"
)

// Channel bounds. They keep a typo from turning a channel into an outbound
// flood or an unreadable configuration.
const (
	// MaxChannelRecipients caps an email channel's distribution list, as
	// reviewalert.MaxRecipients caps the review alert's.
	MaxChannelRecipients = 20
	// MaxChannelNameLen bounds a channel name.
	MaxChannelNameLen = 63
	// DefaultChannelRepeatAfter is the window a repeated key is suppressed
	// for when a channel names none.
	DefaultChannelRepeatAfter = time.Hour
	// DefaultChannelMaxPerHour is the hourly send cap applied when a channel
	// names none.
	DefaultChannelMaxPerHour = 60
)

// channelNamePattern is the accepted channel name: lowercase, digit or
// hyphen, starting on a letter or digit. A channel name reaches a queue row
// as "channel:<name>", so it is kept to a shape that cannot be confused with
// an email address.
var channelNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ChannelRecipientPrefix marks a queue row addressed to a channel rather than
// to a person. The worker dispatches on it, and the enqueue path skips the
// preference lookup for it: a channel has no preferences, and nothing about it
// is a mailbox that could unsubscribe.
const ChannelRecipientPrefix = "channel:"

// Channel is an operator-configured destination: what it is called, how it
// delivers, and what its kind needs to deliver. It holds no credential of its
// own — the three HTTP kinds name an api connection, whose credential is
// encrypted at rest and whose persona authorization is the channel's.
type Channel struct {
	// Name identifies the channel and is its primary key.
	Name string `json:"name"`
	// Kind is one of the ChannelKind* constants.
	Kind string `json:"kind"`
	// Description is the operator's sentence about what this channel is for.
	Description string `json:"description,omitempty"`
	// Enabled gates delivery. A disabled channel refuses an enqueue rather
	// than silently collecting rows nobody will send.
	Enabled bool `json:"enabled"`
	// Connection names the api connection the three HTTP kinds deliver
	// through. Empty for the email kind.
	Connection string `json:"connection,omitempty"`
	// Target is the chat channel id a post is addressed to: Slack's C… id or
	// Mattermost's channel id. Empty for the webhook and email kinds.
	Target string `json:"target,omitempty"`
	// Recipients is the email kind's distribution list. Empty for the three
	// HTTP kinds.
	Recipients []string `json:"recipients,omitempty"`
	// Mode is ChannelModeImmediate or ChannelModeDaily.
	Mode string `json:"mode"`
	// RepeatAfter is how long a repeated key is suppressed for. Zero means
	// DefaultChannelRepeatAfter.
	RepeatAfter time.Duration `json:"repeat_after,omitempty"`
	// MaxPerHour bounds the channel's outbound rate across replicas. Zero
	// means DefaultChannelMaxPerHour.
	MaxPerHour int `json:"max_per_hour,omitempty"`
	// Format is the webhook kind's payload: ChannelFormatText (the default
	// when empty) or ChannelFormatJSON. Empty for every other kind.
	Format string `json:"format,omitempty"`
	// CreatedBy is the administrator who created the channel.
	CreatedBy string `json:"created_by,omitempty"`
	// UpdatedAt is when the record was last written.
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Recipient returns the queue-row recipient for a channel: the channel form
// for the three HTTP kinds, which are addressed as a destination rather than
// as a person. An email channel has no single recipient — it fans out to one
// row per address — so this is not the form its rows carry.
func (c Channel) Recipient() string {
	return ChannelRecipientPrefix + c.Name
}

// ChannelName returns the channel a queue row was addressed to, and whether
// the row was addressed to one at all. The worker dispatches on it.
func ChannelName(recipient string) (string, bool) {
	name, ok := strings.CutPrefix(recipient, ChannelRecipientPrefix)
	return name, ok && name != ""
}

// RepeatWindow returns the channel's suppression window, applying the default
// for an unset one.
func (c Channel) RepeatWindow() time.Duration {
	if c.RepeatAfter <= 0 {
		return DefaultChannelRepeatAfter
	}
	return c.RepeatAfter
}

// PayloadFormat returns the webhook payload format, applying the default for
// an unset one.
func (c Channel) PayloadFormat() string {
	if c.Format == "" {
		return ChannelFormatText
	}
	return c.Format
}

// HourlyCap returns the channel's hourly send cap, applying the default for
// an unset one.
func (c Channel) HourlyCap() int {
	if c.MaxPerHour <= 0 {
		return DefaultChannelMaxPerHour
	}
	return c.MaxPerHour
}

// ChannelStore persists the operator's channel records.
// internal/notification/notifychannel holds the PostgreSQL implementation.
type ChannelStore interface {
	// List returns every channel in name order.
	List(ctx context.Context) ([]Channel, error)
	// Get returns one channel by name, or ErrChannelNotFound.
	Get(ctx context.Context, name string) (*Channel, error)
	// Set creates or replaces a channel.
	Set(ctx context.Context, ch Channel) error
	// Delete removes a channel. Deleting an absent channel is not an error.
	Delete(ctx context.Context, name string) error
}

// ValidateChannel reports what is wrong with a channel record, in the words
// an administrator reads in the settings form.
//
// The kind-specific rules are refusals rather than silent normalizations
// because each one names a field the operator filled in that their chosen
// kind cannot use: a webhook carries no channel target, an email channel
// reaches no api connection, and a slack channel with no target would post
// nowhere.
func ValidateChannel(c Channel) error {
	if !channelNamePattern.MatchString(c.Name) {
		return fmt.Errorf("channel name %q must be lowercase letters, digits and hyphens, start with a letter or digit, and be at most %d characters", c.Name, MaxChannelNameLen)
	}
	switch c.Mode {
	case ChannelModeImmediate, ChannelModeDaily:
	default:
		return fmt.Errorf("channel mode %q must be %q or %q", c.Mode, ChannelModeImmediate, ChannelModeDaily)
	}
	if c.RepeatAfter < 0 {
		return errors.New("channel repeat_after must not be negative")
	}
	if c.MaxPerHour < 0 {
		return errors.New("channel max_per_hour must not be negative")
	}
	if err := validateChannelFormat(c); err != nil {
		return err
	}
	switch c.Kind {
	case ChannelKindSlack, ChannelKindMattermost:
		return validateChatChannel(c)
	case ChannelKindWebhook:
		return validateWebhookChannel(c)
	case ChannelKindEmail:
		return validateEmailChannel(c)
	default:
		return fmt.Errorf("channel kind %q must be one of %s, %s, %s, %s",
			c.Kind, ChannelKindSlack, ChannelKindMattermost, ChannelKindWebhook, ChannelKindEmail)
	}
}

// validateChannelFormat refuses a format the channel's kind does not have.
func validateChannelFormat(c Channel) error {
	switch c.Format {
	case "", ChannelFormatText:
		return nil
	case ChannelFormatJSON:
		if c.Kind != ChannelKindWebhook {
			return fmt.Errorf("format %q is for a %s channel; a %s channel posts in its own shape", c.Format, ChannelKindWebhook, c.Kind)
		}
		return nil
	default:
		return fmt.Errorf("channel format %q must be %q or %q", c.Format, ChannelFormatText, ChannelFormatJSON)
	}
}

// validateChatChannel applies the rules a bot-token kind follows: a
// connection to deliver through and no recipient list, which belongs to the
// email kind alone.
//
// The target is optional on an immediate channel (#2241). With one, the
// channel is a fixed destination agents, scripts and alerts post to. Without
// one it stands for the connection's bot as a whole, and a person sharing an
// asset chooses among the chat channels the bot is in; a send that names no
// target is refused at enqueue (checkSend). A daily channel collects one
// bulletin for one chat channel, so it needs its own.
func validateChatChannel(c Channel) error {
	if c.Connection == "" {
		return fmt.Errorf("a %s channel needs an api connection holding its bot token", c.Kind)
	}
	if c.Target == "" && c.Mode == ChannelModeDaily {
		return fmt.Errorf("a daily %s channel posts one bulletin to its own chat channel; give it a target, or make it immediate", c.Kind)
	}
	if len(c.Recipients) > 0 {
		return fmt.Errorf("a %s channel posts to its target, not to a recipient list; remove the recipients", c.Kind)
	}
	return nil
}

// validateWebhookChannel refuses the target an incoming webhook cannot honor:
// the webhook's own configuration decides where its message lands, so a
// target here would be an instruction the upstream ignores.
func validateWebhookChannel(c Channel) error {
	if c.Connection == "" {
		return fmt.Errorf("a %s channel needs an api connection holding its webhook URL", c.Kind)
	}
	if c.Target != "" {
		return fmt.Errorf("a %s channel posts where its webhook URL points; remove the target", c.Kind)
	}
	if len(c.Recipients) > 0 {
		return fmt.Errorf("a %s channel posts to its webhook, not to a recipient list; remove the recipients", c.Kind)
	}
	return nil
}

// validateEmailChannel applies the email kind's rules: a bounded list of
// addresses, and no connection, since its transport is the deployment's mail
// server rather than an upstream.
func validateEmailChannel(c Channel) error {
	if c.Connection != "" {
		return fmt.Errorf("an %s channel delivers through the deployment's mail server; remove the connection", c.Kind)
	}
	if c.Target != "" {
		return fmt.Errorf("an %s channel delivers to its recipients; remove the target", c.Kind)
	}
	if len(c.Recipients) == 0 {
		return fmt.Errorf("an %s channel needs at least one recipient", c.Kind)
	}
	if len(c.Recipients) > MaxChannelRecipients {
		return fmt.Errorf("an %s channel accepts at most %d recipients, not %d", c.Kind, MaxChannelRecipients, len(c.Recipients))
	}
	for _, addr := range c.Recipients {
		// Parsed rather than normalized. NormalizeAddress deliberately falls
		// back to the trimmed string for a value it cannot parse, so that a
		// malformed address at least compares equal to itself; relying on it
		// here would accept "not-an-address" and hand the queue a recipient
		// no mail server will ever accept.
		if _, err := mail.ParseAddress(strings.TrimSpace(addr)); err != nil {
			return fmt.Errorf("recipient %q is not a valid email address", addr)
		}
	}
	return nil
}

// ChannelNeedsConnection reports whether a kind delivers through an api
// connection. It is the one statement of that split, read by validation, by
// the admin form and by the persona filter that authorizes a send.
func ChannelNeedsConnection(kind string) bool {
	switch kind {
	case ChannelKindSlack, ChannelKindMattermost, ChannelKindWebhook:
		return true
	default:
		return false
	}
}

// ChannelCarriesFiles reports whether a kind delivers attachments as files.
// The others name each attachment by its link.
func ChannelCarriesFiles(kind string) bool {
	return kind == ChannelKindSlack || kind == ChannelKindMattermost || kind == ChannelKindEmail
}

// ChannelHasTarget reports whether a kind posts to a chat channel its
// connection's bot chooses among: the kinds a document's Target can redirect.
func ChannelHasTarget(kind string) bool {
	return kind == ChannelKindSlack || kind == ChannelKindMattermost
}

// ChatTarget is one chat channel a slack or mattermost connection's bot is
// in: what a person sharing an asset chooses among (#2241).
type ChatTarget struct {
	// ID is the channel id a post is addressed to.
	ID string `json:"id"`
	// Name is the channel's name as the upstream shows it, with the team in
	// front on a Mattermost bot that is in more than one.
	Name string `json:"name"`
	// Private marks a channel only its members see.
	Private bool `json:"private,omitempty"`
}
