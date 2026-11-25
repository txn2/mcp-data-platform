// Package platformfeedback is what agents report the platform could not do
// for them (#2820), delivered to whoever runs the platform.
//
// The audit records that a call failed, not what the agent wanted. An agent
// knows at the moment it is blocked what it needed, and that knowledge was
// lost with the session; the only record of intent in three production
// deployments was a handful of tool-finding queries. A post here keeps it:
// what the agent was trying to do, what blocked it, the tool, a suggestion,
// and the calls involved by reference.
//
// Every text field passes the content secret check (internal/secretscan)
// before anything is written, posts are bounded per session and per persona,
// a repeat of an open post is counted on it, and nothing is sent until an
// administrator turns the feature on and names the channels.
package platformfeedback

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/refscheme"
)

// SectionName is the platform_settings section the settings are kept in.
const SectionName = "platform_feedback"

// Defaults for the limits.
const (
	DefaultSessionLimit      = 5
	DefaultPersonaDailyLimit = 50
	maxSessionLimit          = 100
	maxPersonaDailyLimit     = 10000
)

// Settings is how platform feedback is configured in Admin > Settings.
type Settings struct {
	// Enabled lets agents post. Off by default.
	Enabled bool `json:"enabled"`
	// Channels are the notification channels a new post is sent to.
	Channels []string `json:"channels"`
	// Personas may post; empty is every persona except service accounts.
	Personas []string `json:"personas"`
	// SessionLimit is the most posts one session may make.
	SessionLimit int `json:"session_limit"`
	// PersonaDailyLimit is the most posts one persona may make in a day.
	PersonaDailyLimit int `json:"persona_daily_limit"`
}

// withDefaults fills unset limits and empty lists.
func (s Settings) withDefaults() Settings {
	if s.SessionLimit <= 0 {
		s.SessionLimit = DefaultSessionLimit
	}
	if s.PersonaDailyLimit <= 0 {
		s.PersonaDailyLimit = DefaultPersonaDailyLimit
	}
	if s.Channels == nil {
		s.Channels = []string{}
	}
	if s.Personas == nil {
		s.Personas = []string{}
	}
	return s
}

// validate refuses settings a save may not store.
func (s Settings) validate(knownPersona, knownChannel func(string) bool) error {
	if s.SessionLimit < 0 || s.SessionLimit > maxSessionLimit {
		return &InvalidError{Msg: fmt.Sprintf("session_limit: between 1 and %d", maxSessionLimit)}
	}
	if s.PersonaDailyLimit < 0 || s.PersonaDailyLimit > maxPersonaDailyLimit {
		return &InvalidError{Msg: fmt.Sprintf("persona_daily_limit: between 1 and %d", maxPersonaDailyLimit)}
	}
	if err := allKnown(s.Personas, knownPersona, "personas: no persona %q"); err != nil {
		return err
	}
	return allKnown(s.Channels, knownChannel, "channels: no notification channel %q")
}

// allKnown refuses the first name known does not know; a nil known checks
// nothing.
func allKnown(names []string, known func(string) bool, refusal string) error {
	if known == nil {
		return nil
	}
	for _, n := range names {
		if !known(n) {
			return &InvalidError{Msg: fmt.Sprintf(refusal, n)}
		}
	}
	return nil
}

// InvalidError is a settings save refused for what it holds.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// Codes a post is refused with.
const (
	CodeOff     = "platform_feedback_off"
	CodePersona = "platform_feedback_persona"
	CodeLimit   = "platform_feedback_limit"
	CodeSecret  = "platform_feedback_secret"
	CodeInvalid = "platform_feedback_invalid"
)

// RefusedError is a post refused, with the code a caller branches on. Its
// message never repeats a field's content.
type RefusedError struct {
	Code string
	Msg  string
}

func (e *RefusedError) Error() string { return e.Msg }

func refused(code, format string, args ...any) *RefusedError {
	return &RefusedError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Field limits.
const (
	maxTryingTo   = 500
	maxBlockedBy  = 2000
	maxTool       = 100
	maxSuggestion = 2000
	maxCalls      = 20
	maxCallBytes  = 100
	callPrefix    = refscheme.Call
)

// The post's field names, as a refusal, the secret check and a channel's data
// name them.
const (
	fieldTryingTo   = "trying_to"
	fieldBlockedBy  = "blocked_by"
	fieldTool       = "tool"
	fieldSuggestion = "suggestion"
	fieldCalls      = "calls"
)

// Post is one report as an agent writes it.
type Post struct {
	TryingTo   string   `json:"trying_to"`
	BlockedBy  string   `json:"blocked_by"`
	Tool       string   `json:"tool,omitempty"`
	Suggestion string   `json:"suggestion,omitempty"`
	Calls      []string `json:"calls,omitempty"`
}

// normalized trims every field.
func (p Post) normalized() Post {
	p.TryingTo, p.BlockedBy = strings.TrimSpace(p.TryingTo), strings.TrimSpace(p.BlockedBy)
	p.Tool, p.Suggestion = strings.TrimSpace(p.Tool), strings.TrimSpace(p.Suggestion)
	calls := make([]string, 0, len(p.Calls))
	for _, c := range p.Calls {
		if c = strings.TrimSpace(c); c != "" {
			calls = append(calls, c)
		}
	}
	p.Calls = calls
	return p
}

// check refuses a post whose fields are missing, too long, or name calls by
// anything but their reference.
func (p Post) check() error {
	if p.TryingTo == "" || p.BlockedBy == "" {
		return refused(CodeInvalid, "trying_to and blocked_by are required: what you were trying to do, and what stopped it")
	}
	for _, f := range []struct {
		name  string
		value string
		max   int
	}{{fieldTryingTo, p.TryingTo, maxTryingTo}, {fieldBlockedBy, p.BlockedBy, maxBlockedBy}, {fieldTool, p.Tool, maxTool}, {fieldSuggestion, p.Suggestion, maxSuggestion}} {
		if len(f.value) > f.max {
			return refused(CodeInvalid, "%s: at most %d bytes", f.name, f.max)
		}
	}
	if len(p.Calls) > maxCalls {
		return refused(CodeInvalid, "calls: at most %d references", maxCalls)
	}
	for _, c := range p.Calls {
		if !strings.HasPrefix(c, callPrefix) || len(c) > maxCallBytes || strings.ContainsAny(c, " \n\t") {
			return refused(CodeInvalid, "calls: each is a call reference (%s<id>), never data a tool returned", callPrefix)
		}
	}
	return nil
}

// text is the post's words, for duplicate folding.
func (p Post) text() string {
	return strings.Join([]string{p.TryingTo, p.BlockedBy, p.Tool}, "\n")
}
