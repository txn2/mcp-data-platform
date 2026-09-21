package notification

import (
	"strings"
	"testing"
	"time"
)

// validChannel is a well-formed channel of each kind, which each case below
// then breaks in exactly one way. Building from a good record rather than
// from the zero value keeps every failure attributable to the field the case
// changed.
func validChannel(kind string) Channel {
	ch := Channel{Name: "ops", Kind: kind, Enabled: true, Mode: ChannelModeImmediate}
	switch kind {
	case ChannelKindMattermost:
		ch.Connection, ch.Target = "chat-bot", "C0123456789"
	case ChannelKindWebhook:
		ch.Connection = "hook-conn"
	case ChannelKindEmail:
		ch.Recipients = []string{"ops@example.com"}
	}
	return ch
}

func TestValidateChannel_AcceptsEachKind(t *testing.T) {
	for _, kind := range []string{ChannelKindMattermost, ChannelKindWebhook, ChannelKindEmail} {
		t.Run(kind, func(t *testing.T) {
			if err := ValidateChannel(validChannel(kind)); err != nil {
				t.Errorf("a well-formed %s channel was refused: %v", kind, err)
			}
		})
	}
}

func TestValidateChannel_RefusesTheWrongShapes(t *testing.T) {
	tests := []struct {
		name  string
		build func() Channel
		wants string
	}{
		{"an unknown kind", func() Channel {
			ch := validChannel(ChannelKindMattermost)
			ch.Kind = "teams"
			return ch
		}, "must be one of"},
		{"an unknown mode", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.Mode = "hourly"
			return ch
		}, "mode"},
		{"an empty name", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.Name = ""
			return ch
		}, "lowercase letters"},
		{"an uppercase name", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.Name = "Ops"
			return ch
		}, "lowercase letters"},
		{"a name starting with a hyphen", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.Name = "-ops"
			return ch
		}, "lowercase letters"},
		{"a name over the length bound", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.Name = strings.Repeat("a", MaxChannelNameLen+1)
			return ch
		}, "lowercase letters"},
		// The HTTP kinds carry no credential of their own: the connection is
		// what authorizes them, so a channel without one has nothing to
		// deliver through.
		{"a chat channel with no connection", func() Channel {
			ch := validChannel(ChannelKindMattermost)
			ch.Connection = ""
			return ch
		}, "needs an api connection"},
		{"a chat channel with no target", func() Channel {
			ch := validChannel(ChannelKindMattermost)
			ch.Target = ""
			return ch
		}, "needs a target channel id"},
		{"a chat channel with recipients", func() Channel {
			ch := validChannel(ChannelKindMattermost)
			ch.Recipients = []string{"a@example.com"}
			return ch
		}, "remove the recipients"},
		// A webhook's own configuration decides where its message lands, so a
		// target here is an instruction the upstream ignores.
		{"a webhook channel with a target", func() Channel {
			ch := validChannel(ChannelKindWebhook)
			ch.Target = "C1"
			return ch
		}, "remove the target"},
		{"a webhook channel with no connection", func() Channel {
			ch := validChannel(ChannelKindWebhook)
			ch.Connection = ""
			return ch
		}, "needs an api connection"},
		{"an email channel with a connection", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.Connection = "smtp"
			return ch
		}, "remove the connection"},
		{"an email channel with a target", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.Target = "C1"
			return ch
		}, "remove the target"},
		{"an email channel with no recipients", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.Recipients = nil
			return ch
		}, "needs at least one recipient"},
		{"an email channel over the recipient bound", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.Recipients = make([]string, MaxChannelRecipients+1)
			for i := range ch.Recipients {
				ch.Recipients[i] = "a@example.com"
			}
			return ch
		}, "at most"},
		{"an email channel with a malformed address", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.Recipients = []string{"not-an-address"}
			return ch
		}, "not a valid email address"},
		{"a negative repeat window", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.RepeatAfter = -time.Hour
			return ch
		}, "repeat_after"},
		{"a negative hourly cap", func() Channel {
			ch := validChannel(ChannelKindEmail)
			ch.MaxPerHour = -1
			return ch
		}, "max_per_hour"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateChannel(tc.build())
			if err == nil {
				t.Fatal("the channel was accepted")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q does not mention %q", err, tc.wants)
			}
		})
	}
}

func TestChannelRecipient_RoundTrips(t *testing.T) {
	ch := validChannel(ChannelKindMattermost)
	recipient := ch.Recipient()
	if recipient != "channel:ops" {
		t.Errorf("Recipient() = %q, want channel:ops", recipient)
	}
	name, addressed := ChannelName(recipient)
	if !addressed || name != "ops" {
		t.Errorf("ChannelName(%q) = %q, %v; want ops, true", recipient, name, addressed)
	}
}

func TestChannelName_RejectsWhatIsNotAChannelAddress(t *testing.T) {
	// The worker dispatches on this, so a person's address must never read as
	// a channel, and a bare prefix must not read as a channel called "".
	for _, recipient := range []string{"alice@example.com", "", "channel:", "notchannel:ops"} {
		if name, addressed := ChannelName(recipient); addressed {
			t.Errorf("ChannelName(%q) reported channel %q", recipient, name)
		}
	}
}

func TestChannelDefaults(t *testing.T) {
	var ch Channel
	if got := ch.RepeatWindow(); got != DefaultChannelRepeatAfter {
		t.Errorf("RepeatWindow() = %v, want the default %v", got, DefaultChannelRepeatAfter)
	}
	if got := ch.HourlyCap(); got != DefaultChannelMaxPerHour {
		t.Errorf("HourlyCap() = %v, want the default %v", got, DefaultChannelMaxPerHour)
	}
	ch.RepeatAfter, ch.MaxPerHour = 30*time.Minute, 5
	if got := ch.RepeatWindow(); got != 30*time.Minute {
		t.Errorf("RepeatWindow() = %v, want the channel's own value", got)
	}
	if got := ch.HourlyCap(); got != 5 {
		t.Errorf("HourlyCap() = %v, want the channel's own value", got)
	}
}

func TestChannelNeedsConnection(t *testing.T) {
	// This split is read by validation, by the admin form and by the persona
	// filter that authorizes a send, so all three have to agree.
	for kind, want := range map[string]bool{
		ChannelKindMattermost: true,
		ChannelKindWebhook:    true,
		ChannelKindEmail:      false,
		"":                    false,
	} {
		if got := ChannelNeedsConnection(kind); got != want {
			t.Errorf("ChannelNeedsConnection(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestValidateDocument(t *testing.T) {
	tests := []struct {
		name  string
		doc   Document
		wants string
	}{
		{"a titleless document", Document{Body: "x"}, "needs a title"},
		{"a whitespace title", Document{Title: "   "}, "needs a title"},
		{"an oversized title", Document{Title: strings.Repeat("a", MaxDocumentTitleBytes+1)}, "title is at most"},
		{"an oversized body", Document{Title: "t", Body: strings.Repeat("a", MaxDocumentBytes+1)}, "link to the asset instead"},
		{"an oversized link", Document{Title: "t", Link: strings.Repeat("a", MaxDocumentLinkBytes+1)}, "link is at most"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDocument(tc.doc)
			if err == nil {
				t.Fatal("the document was accepted")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q does not mention %q", err, tc.wants)
			}
		})
	}
	if err := ValidateDocument(Document{Title: "Report", Body: "# heading", Link: "https://example.com"}); err != nil {
		t.Errorf("a well-formed document was refused: %v", err)
	}
}
