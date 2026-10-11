package settingsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func TestChannelStatus_ReportsActivityAgainstTheCap(t *testing.T) {
	store := newFakeChannels(notification.Channel{
		Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: "c", Target: "C1", MaxPerHour: 20,
	})
	delivered := time.Date(2026, 10, 10, 9, 14, 3, 0, time.FixedZone("x", 7200))
	failed := time.Date(2026, 10, 10, 8, 2, 11, 0, time.UTC)
	var asked string
	mux := testMux(Config{Channels: store, ChannelActivity: func(_ context.Context, name string) (notification.ChannelActivity, error) {
		asked = name
		return notification.ChannelActivity{
			LastDeliveredAt: &delivered, LastError: "upstream answered HTTP 403", LastErrorAt: &failed,
			SentLastHour: 12, Pending: 2, Suppressed: 3,
		}, nil
	}})
	res := doJSON(t, mux, http.MethodGet, channelPath("ops")+"/status", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	var got ChannelStatusView
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := ChannelStatusView{
		Name: "ops", LastDeliveredAt: "2026-10-10T07:14:03Z", LastError: "upstream answered HTTP 403",
		LastErrorAt: "2026-10-10T08:02:11Z", SentLastHour: 12, MaxPerHour: 20, Pending: 2, Suppressed: 3,
	}
	if got != want || asked != "ops" {
		t.Errorf("status = %+v (asked %q), want %+v", got, asked, want)
	}
}

func TestChannelStatus_AQuietChannelAndTheFailures(t *testing.T) {
	store := newFakeChannels(notification.Channel{
		Name: "list", Kind: notification.ChannelKindEmail, Enabled: true,
		Mode: notification.ChannelModeImmediate, Recipients: []string{"a@example.com"},
	})
	quiet := testMux(Config{Channels: store, ChannelActivity: func(context.Context, string) (notification.ChannelActivity, error) {
		return notification.ChannelActivity{}, nil
	}})
	res := doJSON(t, quiet, http.MethodGet, channelPath("list")+"/status", nil)
	if res.Code != http.StatusOK || res.Body.String() != `{"name":"list","sent_last_hour":0,"max_per_hour":60,"pending":0,"suppressed":0}`+"\n" {
		t.Errorf("quiet channel: %d %s", res.Code, res.Body.String())
	}
	if res := doJSON(t, quiet, http.MethodGet, channelPath("gone")+"/status", nil); res.Code != http.StatusNotFound {
		t.Errorf("absent channel: %d", res.Code)
	}
	broken := testMux(Config{Channels: store, ChannelActivity: func(context.Context, string) (notification.ChannelActivity, error) {
		return notification.ChannelActivity{}, errors.New("db down")
	}})
	if res := doJSON(t, broken, http.MethodGet, channelPath("list")+"/status", nil); res.Code != http.StatusInternalServerError {
		t.Errorf("store failure: %d", res.Code)
	}
	unwired := testMux(Config{Channels: store})
	if res := doJSON(t, unwired, http.MethodGet, channelPath("list")+"/status", nil); res.Code == http.StatusOK {
		t.Error("the status route answered with no activity reader")
	}
}
