// Package generalfeedback decides who is told when someone posts to the
// portal's General feedback channel (#2603): a thread with no item behind it
// has no owner and no grantees, so without a list of its own a post there
// reached nobody. The list is the platform_settings section general_feedback,
// edited in Admin > Settings > General feedback, seeded from
// portal.feedback.standalone_recipients and, with neither, the administrator
// persona's people.
package generalfeedback

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyalert"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// SettingsSection is the platform_settings section holding Settings.
const SettingsSection = "general_feedback"

// Bounds on the list, so a settings write cannot make every post a mass mailing.
const (
	maxPersonas   = 20
	maxRecipients = 50
)

// Seed entry prefixes in portal.feedback.standalone_recipients.
const (
	prefixPersona = "persona:"
	prefixChannel = "channel:"
)

// WarningReachesNobody is the warning a list that resolves to no person and no
// channel carries.
const WarningReachesNobody = "reaches_nobody"

// Settings is who hears about a new General post.
type Settings struct {
	// Personas whose people are told: everyone in the user directory whose
	// recorded roles resolve to one of them.
	Personas []string `json:"personas"`
	// Recipients are addresses told as well, such as a team list.
	Recipients []string `json:"recipients"`
	// Channels a new post is also sent to. Replies are not.
	Channels []string `json:"channels"`
	// AcknowledgeAuthor sends the person who posted an email saying their
	// post went to the people who handle feedback, without naming them.
	AcknowledgeAuthor bool `json:"acknowledge_author"`
}

// Seed is the list before an administrator saves one: the configuration's
// entries ("persona:<name>", "channel:<name>", or an address), and with none
// the administrator persona.
func Seed(entries []string, adminPersona string) Settings {
	var s Settings
	for _, e := range entries {
		e = strings.TrimSpace(e)
		switch {
		case strings.HasPrefix(e, prefixPersona):
			s.Personas = append(s.Personas, strings.TrimSpace(strings.TrimPrefix(e, prefixPersona)))
		case strings.HasPrefix(e, prefixChannel):
			s.Channels = append(s.Channels, strings.TrimSpace(strings.TrimPrefix(e, prefixChannel)))
		case e != "":
			s.Recipients = append(s.Recipients, e)
		}
	}
	if len(s.Personas)+len(s.Recipients)+len(s.Channels) == 0 && adminPersona != "" {
		s.Personas = []string{adminPersona}
	}
	return s.normalized()
}

// normalized drops blanks and repeats and puts addresses in their compared
// form, so a stored list is the one the notifier reads.
func (s Settings) normalized() Settings {
	s.Personas = notifyalert.Normalize(s.Personas)
	s.Channels = notifyalert.Normalize(s.Channels)
	recipients := make([]string, 0, len(s.Recipients))
	for _, r := range s.Recipients {
		if a := notification.NormalizeAddress(r); a != "" && !slices.Contains(recipients, a) {
			recipients = append(recipients, a)
		}
	}
	s.Recipients = recipients
	return s
}

// Deps is what the service reads.
type Deps struct {
	// Section is the stored list; nil serves the seed and refuses a save.
	Section *settingsjson.Section[Settings]
	// Seed is the list before one is saved.
	Seed Settings
	// Members lists the people a persona reaches: the confirmed people in the
	// user directory whose recorded roles resolve to it.
	Members func(ctx context.Context, persona string) []string
	// Personas lists the personas the list may name, as name and display name.
	Personas func() []PersonaOption
	// Channels is the notification channel store a saved list is checked
	// against; nil refuses any channel.
	Channels notification.ChannelStore
}

// Service reads, resolves and saves the list.
type Service struct{ d Deps }

// New builds the service.
func New(d Deps) *Service { return &Service{d: d} }

// stored is the list in force and who saved it, the seed when nobody has.
func (s *Service) stored(ctx context.Context) settingsjson.Stored[Settings] {
	if s.d.Section == nil {
		return settingsjson.Stored[Settings]{Value: s.d.Seed}
	}
	got, err := s.d.Section.Get(ctx)
	if err != nil {
		return settingsjson.Stored[Settings]{Value: s.d.Seed}
	}
	return got
}

// Recipients is who a new General post reaches now: the people the personas
// resolve to with the typed addresses, deduplicated; the channels; and
// whether the author is told.
func (s *Service) Recipients(ctx context.Context) (people, channels []string, acknowledge bool) {
	set := s.stored(ctx).Value
	return s.people(ctx, set), set.Channels, set.AcknowledgeAuthor
}

func (s *Service) people(ctx context.Context, set Settings) []string {
	var all []string
	if s.d.Members != nil {
		for _, p := range set.Personas {
			all = append(all, s.d.Members(ctx, p)...)
		}
	}
	all = append(all, set.Recipients...)
	return notification.RecipientsExcluding("", all...)
}

// ReachesSomeone reports whether a new General post is told to anyone.
func (s *Service) ReachesSomeone(ctx context.Context) bool {
	people, channels, _ := s.Recipients(ctx)
	return len(people) > 0 || len(channels) > 0
}

// ErrNoStore is a save on a deployment with no settings store.
var ErrNoStore = errors.New("this deployment has no settings store; set portal.feedback.standalone_recipients instead")

// InvalidError is a list refused, its message naming the entry.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// Save checks and stores a list.
func (s *Service) Save(ctx context.Context, in Settings, author string) (View, error) {
	if s.d.Section == nil {
		return View{}, ErrNoStore
	}
	in = in.normalized()
	if err := s.validate(ctx, in); err != nil {
		return View{}, err
	}
	if err := s.d.Section.Set(ctx, in, author); err != nil {
		return View{}, fmt.Errorf("saving the general feedback list: %w", err)
	}
	return s.View(ctx), nil
}

func (s *Service) validate(ctx context.Context, in Settings) error {
	if len(in.Personas) > maxPersonas || len(in.Recipients) > maxRecipients {
		return &InvalidError{Msg: fmt.Sprintf("the list names at most %d personas and %d addresses", maxPersonas, maxRecipients)}
	}
	if err := s.validatePersonas(in.Personas); err != nil {
		return err
	}
	if err := validateRecipients(in.Recipients); err != nil {
		return err
	}
	if err := notifyalert.Validate(ctx, s.d.Channels, in.Channels); err != nil {
		return &InvalidError{Msg: "channels: " + err.Error()}
	}
	return nil
}

// validatePersonas refuses a persona the registry does not hold.
func (s *Service) validatePersonas(names []string) error {
	known := map[string]bool{}
	if s.d.Personas != nil {
		for _, p := range s.d.Personas() {
			known[p.Name] = true
		}
	}
	for _, p := range names {
		if !known[p] {
			return &InvalidError{Msg: fmt.Sprintf("personas: no persona is named %q", p)}
		}
	}
	return nil
}

// validateRecipients refuses an entry that is not a bare email address.
func validateRecipients(addresses []string) error {
	for _, r := range addresses {
		if a, err := mail.ParseAddress(r); err != nil || a.Address != r || !strings.Contains(r, "@") {
			return &InvalidError{Msg: fmt.Sprintf("recipients: %q is not an email address", r)}
		}
	}
	return nil
}
