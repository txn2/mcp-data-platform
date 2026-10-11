package notification

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func opsChannel() Channel {
	return Channel{Name: "ops", Kind: ChannelKindMattermost, Enabled: true, Mode: ChannelModeImmediate, Connection: "c", Target: "T"}
}

func TestNotifyChannel_PassesTheGateItsCapAndKey(t *testing.T) {
	e, queue := channelEnqueuer(t, nil)
	ch := opsChannel()
	ch.MaxPerHour, ch.RepeatAfter = 10, 30*time.Minute
	_, err := e.NotifyChannel(context.Background(), ChannelSend{Channel: ch, Document: Document{Title: "t", Key: "feed"}})
	if err != nil {
		t.Fatal(err)
	}
	want := ChannelGate{Channel: "ops", MaxPerHour: 10, Key: "feed", Window: 30 * time.Minute}
	if len(queue.gates) != 1 || queue.gates[0] != want {
		t.Errorf("gates = %+v, want %+v", queue.gates, want)
	}
}

func TestGateFor(t *testing.T) {
	ch := opsChannel()
	cases := []struct {
		name string
		send ChannelSend
		want ChannelGate
	}{
		{
			"no key",
			ChannelSend{Channel: ch, Document: Document{Title: "t"}},
			ChannelGate{Channel: "ops", MaxPerHour: DefaultChannelMaxPerHour},
		},
		{
			"key, the channel's default window",
			ChannelSend{Channel: ch, Document: Document{Key: "k"}},
			ChannelGate{Channel: "ops", MaxPerHour: DefaultChannelMaxPerHour, Key: "k", Window: DefaultChannelRepeatAfter},
		},
		{
			"a longer repeat_after lengthens it",
			ChannelSend{Channel: ch, Document: Document{Key: "k"}, RepeatAfter: 6 * time.Hour},
			ChannelGate{Channel: "ops", MaxPerHour: DefaultChannelMaxPerHour, Key: "k", Window: 6 * time.Hour},
		},
		{
			"a shorter repeat_after is ignored",
			ChannelSend{Channel: ch, Document: Document{Key: "k"}, RepeatAfter: time.Minute},
			ChannelGate{Channel: "ops", MaxPerHour: DefaultChannelMaxPerHour, Key: "k", Window: DefaultChannelRepeatAfter},
		},
	}
	for _, tc := range cases {
		if got := gateFor(tc.send); got != tc.want {
			t.Errorf("%s: gateFor = %+v, want %+v", tc.name, got, tc.want)
		}
	}
	daily := ch
	daily.Mode = ChannelModeDaily
	if got := gateFor(ChannelSend{Channel: daily, Document: Document{Key: "k"}}); got.Key != "" {
		t.Errorf("a daily channel's send was keyed: %+v", got)
	}
}

func TestNotifyChannel_ASuppressedSendWritesNothing(t *testing.T) {
	e, queue := channelEnqueuer(t, nil)
	queue.gateOutcome = ChannelOutcome{Suppressed: true}
	res, err := e.NotifyChannel(context.Background(), ChannelSend{Channel: opsChannel(), Document: Document{Title: "t", Key: "k"}})
	if err != nil || !res.Suppressed || res.Queued != 0 || len(queue.enqueued) != 0 {
		t.Fatalf("res = %+v, err %v, rows %d; want suppressed and nothing written", res, err, len(queue.enqueued))
	}
}

func TestNotifyChannel_OverTheCapNamesTheCap(t *testing.T) {
	e, queue := channelEnqueuer(t, nil)
	queue.gateErr = ErrChannelOverCap
	ch := opsChannel()
	ch.MaxPerHour = 60
	_, err := e.NotifyChannel(context.Background(), ChannelSend{Channel: ch, Document: Document{Title: "t"}})
	if !errors.Is(err, ErrChannelOverCap) || !strings.Contains(err.Error(), "cap of 60 messages in the last hour") {
		t.Fatalf("err = %v, want the cap named", err)
	}
	queue.gateErr = errors.New("db down")
	if _, err := e.NotifyChannel(context.Background(), ChannelSend{Channel: ch, Document: Document{Title: "t"}}); err == nil || errors.Is(err, ErrChannelOverCap) {
		t.Errorf("a store failure read as %v", err)
	}
}

func TestNotifyChannel_AnEmailListAllOptedOutQueuesNothing(t *testing.T) {
	e, queue := channelEnqueuer(t, map[string]Prefs{"a@example.com": {Mode: ModeOff}})
	ch := Channel{Name: "list", Kind: ChannelKindEmail, Enabled: true, Mode: ChannelModeImmediate, Recipients: []string{"a@example.com"}}
	res, err := e.NotifyChannel(context.Background(), ChannelSend{Channel: ch, Document: Document{Title: "t", Key: "k"}})
	if err != nil || res.Queued != 0 || len(queue.gates) != 0 {
		t.Errorf("res %+v err %v gates %d; want nothing queued and the key untouched", res, err, len(queue.gates))
	}
}

func TestSuppressionNote(t *testing.T) {
	since := time.Date(2026, 10, 10, 9, 5, 0, 0, time.FixedZone("x", 3600))
	cases := []struct {
		doc  Document
		want string
	}{
		{Document{}, ""},
		{Document{Suppressed: 1}, "1 repeat of this alert was not posted."},
		{Document{Suppressed: 4, SuppressedSince: &since}, "4 repeats of this alert since 2026-10-10 08:05 UTC were not posted."},
	}
	for _, tc := range cases {
		if got := tc.doc.SuppressionNote(); got != tc.want {
			t.Errorf("SuppressionNote(%+v) = %q, want %q", tc.doc, got, tc.want)
		}
	}
}

func TestValidateDocument_Attachments(t *testing.T) {
	five := make([]Attachment, MaxAttachments+1)
	for i := range five {
		five[i] = Attachment{AssetID: "a"}
	}
	cases := map[string][]Attachment{
		"too many": five,
		"both":     {{AssetID: "a", ResourceID: "r"}},
		"neither":  {{Name: "x"}},
		"preview":  {{ResourceID: "r", Preview: true}},
		"negative": {{AssetID: "a", Version: -1}},
	}
	for name, atts := range cases {
		if err := ValidateDocument(Document{Title: "t", Attachments: atts}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := []Attachment{{AssetID: "a", Version: 2}, {AssetID: "a", Preview: true}, {ResourceID: "r"}}
	if err := ValidateDocument(Document{Title: "t", Attachments: ok}); err != nil {
		t.Errorf("valid attachments refused: %v", err)
	}
	if err := ValidateDocument(Document{Title: "t", Key: strings.Repeat("k", MaxDocumentKeyBytes+1)}); err == nil {
		t.Error("an oversized key was accepted")
	}
}

func TestChannelCarriesFiles(t *testing.T) {
	for kind, want := range map[string]bool{
		ChannelKindMattermost: true, ChannelKindEmail: true, ChannelKindWebhook: false,
	} {
		if got := ChannelCarriesFiles(kind); got != want {
			t.Errorf("ChannelCarriesFiles(%s) = %v", kind, got)
		}
	}
}

func TestValidateChannel_Slack(t *testing.T) {
	ok := Channel{Name: "ops", Kind: ChannelKindSlack, Mode: ChannelModeImmediate, Connection: "slack", Target: "C123"}
	if err := ValidateChannel(ok); err != nil {
		t.Errorf("a slack channel with a connection and a target was refused: %v", err)
	}
	noTarget := ok
	noTarget.Target = ""
	if err := ValidateChannel(noTarget); err == nil {
		t.Error("a slack channel with no target was accepted")
	}
	if !ChannelNeedsConnection(ChannelKindSlack) || !ChannelCarriesFiles(ChannelKindSlack) {
		t.Error("a slack channel names a connection and carries files")
	}
}
