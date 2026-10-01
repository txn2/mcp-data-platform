package notifylayer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

func webhookChannel(name, conn string) notification.Channel {
	return notification.Channel{
		Name: name, Kind: notification.ChannelKindWebhook, Enabled: true, Connection: conn,
		Mode: notification.ChannelModeImmediate, Format: notification.ChannelFormatJSON,
	}
}

// TestSendCarriesDataAndWhoSentIt holds #1997's send surface: data in every
// JSON form the parameter admits is queued verbatim, and the document records
// that a person sent it.
func TestSendCarriesDataAndWhoSentIt(t *testing.T) {
	for _, tc := range []struct {
		data any
		want string
	}{
		{map[string]any{"stores": []any{3.0, 7.0}}, `{"stores":[3,7]}`},
		{[]any{"a", 1.0}, `["a",1]`},
		{"verbatim", `"verbatim"`},
		{42.5, `42.5`},
		{nil, ``},
	} {
		h, queue := testHandle(t, []notification.Channel{webhookChannel("events", "hook")}, []string{"hook"})
		res, _, err := h.handle(asAnalyst(t), notifyInput{Action: "send", Channel: "events", Title: "t", Data: tc.data})
		if err != nil || res.IsError {
			t.Fatalf("data %v: %v %s", tc.data, err, resultText(t, res))
		}
		doc := queue.rows[0].Payload.Document
		if string(doc.Data) != tc.want {
			t.Errorf("data %v queued as %s, want %s", tc.data, doc.Data, tc.want)
		}
		if doc.Type != notification.DocumentNotifySent || doc.Source == nil || doc.Source.Kind != notification.DocumentSourceUser {
			t.Errorf("a person's send was stamped %q %+v", doc.Type, doc.Source)
		}
	}
}

func TestSendRefusesDataOverTheCap(t *testing.T) {
	h, queue := testHandle(t, []notification.Channel{webhookChannel("events", "hook")}, []string{"hook"})
	res, _, _ := h.handle(asAnalyst(t), notifyInput{
		Action: "send", Channel: "events", Title: "t",
		Data: strings.Repeat("x", notification.MaxDocumentDataBytes),
	})
	if !res.IsError || len(queue.rows) != 0 {
		t.Fatalf("data over the cap was queued: %s", resultText(t, res))
	}
	res, _, _ = h.handle(asAnalyst(t), notifyInput{Action: "send", Channel: "events", Title: "t", Data: func() {}})
	if !res.IsError {
		t.Fatal("data that cannot be JSON was accepted")
	}
}

// TestAScriptRunsSendIsAFinding stamps a send made from a managed script's run
// with the script's reference and the run's id, read off the context the run's
// calls carry.
func TestAScriptRunsSendIsAFinding(t *testing.T) {
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserEmail: "owner@example.com", UserID: "script:monitor", PersonaName: "analyst",
		Source: middleware.SourceScript,
	})
	ctx = pkgsession.WithAwareSessionID(ctx, "dpx_run1")
	ctx = producedby.With(ctx, producedby.Producer{Kind: producedby.KindScript, ID: "s1", Label: "monitor"})
	h, queue := testHandle(t, []notification.Channel{webhookChannel("events", "hook")}, []string{"hook"})
	res, _, err := h.handle(ctx, notifyInput{Action: "send", Channel: "events", Title: "t", Data: map[string]any{"n": 1.0}})
	if err != nil || res.IsError {
		t.Fatalf("send: %v %s", err, resultText(t, res))
	}
	doc := queue.rows[0].Payload.Document
	got, _ := json.Marshal(doc.Source)
	if doc.Type != notification.DocumentScriptFinding ||
		string(got) != `{"kind":"script","script":"mcp:script:s1","run_id":"dpx_run1"}` {
		t.Errorf("a script's send was stamped %q %s", doc.Type, got)
	}
}
