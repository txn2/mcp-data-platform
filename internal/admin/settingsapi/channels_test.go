package settingsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/notification/notifychannel"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakeChannels models the real store's contract: an absent name reads as
// ErrChannelNotFound, not as a zero-valued channel.
type fakeChannels struct {
	channels map[string]notification.Channel
	getErr   error
	setErr   error
	listErr  error
	delErr   error
	lastSet  *notification.Channel
	deleted  []string
}

func newFakeChannels(seed ...notification.Channel) *fakeChannels {
	f := &fakeChannels{channels: map[string]notification.Channel{}}
	for _, ch := range seed {
		f.channels[ch.Name] = ch
	}
	return f
}

func (f *fakeChannels) List(context.Context) ([]notification.Channel, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	// Name order, as the real store's ORDER BY gives.
	names := make([]string, 0, len(f.channels))
	for n := range f.channels {
		names = append(names, n)
	}
	out := make([]notification.Channel, 0, len(names))
	for _, n := range sorted(names) {
		out = append(out, f.channels[n])
	}
	return out, nil
}

func (f *fakeChannels) Get(_ context.Context, name string) (*notification.Channel, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	ch, ok := f.channels[name]
	if !ok {
		return nil, notifychannel.ErrChannelNotFound
	}
	return &ch, nil
}

func (f *fakeChannels) Set(_ context.Context, ch notification.Channel) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.lastSet = &ch
	f.channels[ch.Name] = ch
	return nil
}

func (f *fakeChannels) Delete(_ context.Context, name string) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = append(f.deleted, name)
	delete(f.channels, name)
	return nil
}

// sorted returns names in ascending order without pulling in a helper.
func sorted(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func channelPath(name string) string { return channelsPath + "/" + name }

func decodeChannel(t *testing.T, body []byte) ChannelView {
	t.Helper()
	var v ChannelView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode channel: %v", err)
	}
	return v
}

func TestPutChannel_CreatesEachKind(t *testing.T) {
	// Every JSON form the PUT admits, one per kind.
	tests := []struct {
		name string
		body map[string]any
	}{
		{"mattermost", map[string]any{"kind": "mattermost", "connection": "mm", "target": "ch1"}},
		{"webhook", map[string]any{"kind": "webhook", "connection": "hook"}},
		{"email", map[string]any{"kind": "email", "recipients": []string{"ops@example.com"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeChannels()
			mux := testMux(Config{Channels: store, Mutable: true})
			res := doJSON(t, mux, http.MethodPut, channelPath("ops"), tc.body)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
			}
			view := decodeChannel(t, res.Body.Bytes())
			if view.Name != "ops" || view.Kind != tc.name {
				t.Errorf("view = %+v, want the channel that was written", view)
			}
			// Omitted fields take the platform defaults rather than zero.
			if view.Mode != notification.ChannelModeImmediate {
				t.Errorf("Mode = %q, want %q by default", view.Mode, notification.ChannelModeImmediate)
			}
			if !view.Enabled {
				t.Error("a channel created without an enabled flag was disabled")
			}
			if view.MaxPerHour != notification.DefaultChannelMaxPerHour {
				t.Errorf("MaxPerHour = %d, want the default", view.MaxPerHour)
			}
			if view.RepeatAfter != notification.DefaultChannelRepeatAfter.String() {
				t.Errorf("RepeatAfter = %q, want the default", view.RepeatAfter)
			}
			if store.lastSet.CreatedBy != "admin@example.com" {
				t.Errorf("CreatedBy = %q, want the acting admin", store.lastSet.CreatedBy)
			}
		})
	}
}

func TestPutChannel_RefusesTheWrongShape(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		body  map[string]any
		wants string
	}{
		{"an unknown kind", "ops", map[string]any{"kind": "teams"}, "must be one of"},
		{"a name that is not a channel name", "OpsTeam", map[string]any{"kind": "email", "recipients": []string{"a@example.com"}}, "lowercase"},
		{"a chat channel with no target", "ops", map[string]any{"kind": "mattermost", "connection": "c"}, "target channel id"},
		{"an email channel with a connection", "ops", map[string]any{"kind": "email", "connection": "c", "recipients": []string{"a@example.com"}}, "remove the connection"},
		{"a malformed repeat_after", "ops", map[string]any{"kind": "webhook", "connection": "c", "repeat_after": "soon"}, "duration"},
		{"a malformed recipient", "ops", map[string]any{"kind": "email", "recipients": []string{"nope"}}, "not a valid email address"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeChannels()
			mux := testMux(Config{Channels: store, Mutable: true})
			res := doJSON(t, mux, http.MethodPut, channelPath(tc.path), tc.body)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), tc.wants) {
				t.Errorf("body %s does not mention %q", res.Body.String(), tc.wants)
			}
			if store.lastSet != nil {
				t.Error("a refused channel was written")
			}
		})
	}
}

func TestPutChannel_AnEditKeepsTheOriginalAuthor(t *testing.T) {
	// The administrator who owns a channel is the one who made it; an edit by
	// a colleague does not transfer that.
	store := newFakeChannels(notification.Channel{
		Name: "ops", Kind: notification.ChannelKindWebhook, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: "hook", CreatedBy: "first@example.com",
	})
	mux := testMux(Config{
		Channels: store, Mutable: true,
		Author: func(*http.Request) string { return "second@example.com" },
	})

	res := doJSON(t, mux, http.MethodPut, channelPath("ops"),
		map[string]any{"kind": "webhook", "connection": "hook", "description": "edited"})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	if store.lastSet.CreatedBy != "first@example.com" {
		t.Errorf("CreatedBy = %q, want the original author", store.lastSet.CreatedBy)
	}
}

func TestGetChannel_AndTheNotFoundCase(t *testing.T) {
	store := newFakeChannels(notification.Channel{
		Name: "ops", Kind: notification.ChannelKindEmail, Enabled: true,
		Mode: notification.ChannelModeImmediate, Recipients: []string{"a@example.com"},
	})
	mux := testMux(Config{Channels: store, Mutable: true})

	res := doJSON(t, mux, http.MethodGet, channelPath("ops"), nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}
	if got := decodeChannel(t, res.Body.Bytes()); got.Name != "ops" {
		t.Errorf("view = %+v", got)
	}
	if res := doJSON(t, mux, http.MethodGet, channelPath("gone"), nil); res.Code != http.StatusNotFound {
		t.Errorf("status for an absent channel = %d, want 404", res.Code)
	}
}

func TestListChannels_NamesTheKindsAndTheChannels(t *testing.T) {
	store := newFakeChannels(
		notification.Channel{Name: "b-ops", Kind: notification.ChannelKindWebhook, Connection: "hook"},
		notification.Channel{Name: "a-ops", Kind: notification.ChannelKindEmail, Recipients: []string{"a@example.com"}},
	)
	mux := testMux(Config{Channels: store, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, channelsPath, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}
	var out ChannelListView
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Channels) != 2 || out.Channels[0].Name != "a-ops" {
		t.Errorf("channels = %+v, want name order", out.Channels)
	}
	// The form offers what this deployment can deliver to, which is the set
	// the API reports rather than a list the form carries of its own.
	if len(out.Kinds) != 3 {
		t.Errorf("kinds = %v, want the three kinds", out.Kinds)
	}
}

func TestChannelView_WarnsAboutAConnectionNothingServes(t *testing.T) {
	// The defect an operator actually meets: they created the channel first,
	// or deleted the connection later, and without this the channel looks
	// configured and fails only when something is sent to it.
	store := newFakeChannels(notification.Channel{
		Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: "ghost", Target: "C1",
	})
	mux := testMux(Config{
		Channels: store, Mutable: true,
		ConnectionExists: func(context.Context, string) bool { return false },
	})
	res := doJSON(t, mux, http.MethodGet, channelPath("ops"), nil)
	view := decodeChannel(t, res.Body.Bytes())
	if len(view.Warnings) != 1 || !strings.Contains(view.Warnings[0], "ghost") {
		t.Errorf("warnings = %v, want one naming the missing connection", view.Warnings)
	}

	// An email channel names no connection, so it is never warned about.
	store.channels["mail"] = notification.Channel{
		Name: "mail", Kind: notification.ChannelKindEmail, Enabled: true,
		Mode: notification.ChannelModeImmediate, Recipients: []string{"a@example.com"},
	}
	res = doJSON(t, mux, http.MethodGet, channelPath("mail"), nil)
	if got := decodeChannel(t, res.Body.Bytes()); len(got.Warnings) != 0 {
		t.Errorf("an email channel was warned about: %v", got.Warnings)
	}
}

func TestDeleteChannel(t *testing.T) {
	store := newFakeChannels(notification.Channel{Name: "ops", Kind: notification.ChannelKindWebhook})
	mux := testMux(Config{Channels: store, Mutable: true})
	res := doJSON(t, mux, http.MethodDelete, channelPath("ops"), nil)
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", res.Code)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "ops" {
		t.Errorf("deleted = %v", store.deleted)
	}
}

func TestTestChannel_ReportsTheUpstreamsOwnAnswer(t *testing.T) {
	store := newFakeChannels(notification.Channel{
		Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: "c", Target: "C1",
	})
	t.Run("accepted", func(t *testing.T) {
		mux := testMux(Config{
			Channels: store, Mutable: true,
			SendChannelTest: func(context.Context, string) error { return nil },
		})
		res := doJSON(t, mux, http.MethodPost, channelPath("ops")+"/test", nil)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
		}
		var out ChannelTestResult
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if !out.Delivered {
			t.Error("a successful test reported no delivery")
		}
	})
	t.Run("refused", func(t *testing.T) {
		// The transport's own words: "not_in_channel" is the only thing that
		// tells the administrator what to fix. 503 rather than 502 because a
		// CDN replaces a 502 body with its own page (#1704).
		mux := testMux(Config{
			Channels: store, Mutable: true,
			SendChannelTest: func(context.Context, string) error {
				return errors.New("mattermost refused the message: the bot is not in that channel")
			},
		})
		res := doJSON(t, mux, http.MethodPost, channelPath("ops")+"/test", nil)
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", res.Code)
		}
		if !strings.Contains(res.Body.String(), "not in that channel") {
			t.Errorf("the upstream's answer did not reach the operator: %s", res.Body.String())
		}
	})
	t.Run("an absent channel", func(t *testing.T) {
		mux := testMux(Config{
			Channels: store, Mutable: true,
			SendChannelTest: func(context.Context, string) error { return nil },
		})
		res := doJSON(t, mux, http.MethodPost, channelPath("gone")+"/test", nil)
		if res.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", res.Code)
		}
	})
}

func TestChannelRoutes_AreUnmountedInFileConfigMode(t *testing.T) {
	// Reads stay; writes answer 405, as every other admin configuration
	// surface does.
	store := newFakeChannels(notification.Channel{Name: "ops", Kind: notification.ChannelKindWebhook})
	mux := testMux(Config{Channels: store, Mutable: false})
	if res := doJSON(t, mux, http.MethodGet, channelsPath, nil); res.Code != http.StatusOK {
		t.Errorf("GET status = %d, want 200 even in file mode", res.Code)
	}
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPut, channelPath("ops")},
		{http.MethodDelete, channelPath("ops")},
		{http.MethodPost, channelPath("ops") + "/test"},
	} {
		res := doJSON(t, mux, tc.method, tc.path, map[string]any{"kind": "webhook", "connection": "c"})
		if res.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", tc.method, tc.path, res.Code)
		}
	}
}

func TestChannelRoutes_AbsentWithoutAStore(t *testing.T) {
	// No database means no channels; the routes are not mounted at all.
	mux := testMux(Config{Mutable: true})
	if res := doJSON(t, mux, http.MethodGet, channelsPath, nil); res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no channel store is wired", res.Code)
	}
}

func TestChannelRoutes_ReportStoreFailures(t *testing.T) {
	failing := newFakeChannels()
	failing.listErr = errors.New("pool down")
	failing.getErr = errors.New("pool down")
	mux := testMux(Config{Channels: failing, Mutable: true})
	if res := doJSON(t, mux, http.MethodGet, channelsPath, nil); res.Code != http.StatusInternalServerError {
		t.Errorf("list status = %d, want 500", res.Code)
	}
	if res := doJSON(t, mux, http.MethodGet, channelPath("ops"), nil); res.Code != http.StatusInternalServerError {
		t.Errorf("get status = %d, want 500", res.Code)
	}

	writeFail := newFakeChannels()
	writeFail.setErr = errors.New("pool down")
	mux = testMux(Config{Channels: writeFail, Mutable: true})
	res := doJSON(t, mux, http.MethodPut, channelPath("ops"),
		map[string]any{"kind": "webhook", "connection": "hook"})
	if res.Code != http.StatusInternalServerError {
		t.Errorf("put status = %d, want 500", res.Code)
	}

	delFail := newFakeChannels()
	delFail.delErr = errors.New("pool down")
	mux = testMux(Config{Channels: delFail, Mutable: true})
	if res := doJSON(t, mux, http.MethodDelete, channelPath("ops"), nil); res.Code != http.StatusInternalServerError {
		t.Errorf("delete status = %d, want 500", res.Code)
	}
}

func TestTrimAll_DropsBlankRecipientLines(t *testing.T) {
	// A recipient list edited in a textarea must not save a blank line as an
	// address.
	if got := trimAll([]string{" a@example.com ", "", "  ", "b@example.com"}); len(got) != 2 {
		t.Errorf("trimAll = %v, want the two real addresses", got)
	}
	if got := trimAll([]string{"", " "}); got != nil {
		t.Errorf("trimAll of blanks = %v, want nil", got)
	}
}
