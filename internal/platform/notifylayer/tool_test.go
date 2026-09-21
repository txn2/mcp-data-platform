package notifylayer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/connscope"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// memChannels serves the operator's channels from memory.
type memChannels struct {
	channels []notification.Channel
	err      error
}

func (m *memChannels) List(context.Context) ([]notification.Channel, error) {
	return m.channels, m.err
}

func (m *memChannels) Get(_ context.Context, name string) (*notification.Channel, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, ch := range m.channels {
		if ch.Name == name {
			out := ch
			return &out, nil
		}
	}
	return nil, errors.New("not found")
}

func (*memChannels) Set(context.Context, notification.Channel) error { return nil }
func (*memChannels) Delete(context.Context, string) error            { return nil }

// memQueue records what the tool enqueued.
type memQueue struct{ rows []notification.Notification }

func (q *memQueue) Enqueue(_ context.Context, n notification.Notification) error {
	q.rows = append(q.rows, n)
	return nil
}

func (*memQueue) ClaimImmediate(context.Context, time.Duration, notification.TransportFilter) (*notification.Notification, error) {
	return nil, notification.ErrNoWork
}

func (*memQueue) ClaimDigest(context.Context, time.Duration, notification.TransportFilter) ([]notification.Notification, error) {
	return nil, notification.ErrNoWork
}

func (*memQueue) MarkSent(context.Context, []int64) error                     { return nil }
func (*memQueue) Retry(context.Context, []int64, string, time.Duration) error { return nil }
func (*memQueue) Fail(context.Context, []int64, string) error                 { return nil }
func (*memQueue) PurgeOld(context.Context, time.Duration, time.Duration) (int64, error) {
	return 0, nil
}

// memPrefs answers with the platform defaults for everyone.
type memPrefs struct{}

func (memPrefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

func (memPrefs) Set(_ context.Context, email string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

// memAssets serves one asset and its content.
type memAssets struct {
	asset   *portaldomain.Asset
	content []byte
	err     error
}

func (m *memAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.asset == nil || m.asset.ID != id {
		return nil, errors.New("not found")
	}
	return m.asset, nil
}

func (m *memAssets) Content(context.Context, *portaldomain.Asset) ([]byte, error) {
	return m.content, m.err
}

// allowAll and denyAll are the two entitlement answers publish branches on.
type allowAll struct{}

func (allowAll) CanRead(context.Context, *portaldomain.Asset, string, string) bool { return true }

type denyAll struct{}

func (denyAll) CanRead(context.Context, *portaldomain.Asset, string, string) bool { return false }

// personaScope builds a connection scope from a persona registry allowing the
// named connections to the "analyst" persona.
func personaScope(t *testing.T, allowed ...string) *connscope.Scope {
	t.Helper()
	reg := persona.NewRegistry()
	if err := reg.Register(&persona.Persona{
		Name:        "analyst",
		Roles:       []string{"analyst"},
		Tools:       persona.ToolRules{Allow: []string{"*"}},
		Connections: persona.ConnectionRules{Allow: allowed},
	}); err != nil {
		t.Fatal(err)
	}
	return connscope.New(connscope.Deps{Registry: reg})
}

// testHandle builds the tool over the given channels, with the analyst
// persona reaching allowedConns.
func testHandle(t *testing.T, channels []notification.Channel, allowedConns []string, opts ...func(*Config)) (*Handle, *memQueue) {
	t.Helper()
	queue := &memQueue{}
	enq := notification.NewEnqueuer(memPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	cfg := Config{
		Channels:     &memChannels{channels: channels},
		Enqueuer:     enq,
		Scope:        personaScope(t, allowedConns...),
		PortalURL:    "https://portal.example.com",
		AdminPersona: "admin",
	}
	for _, o := range opts {
		o(&cfg)
	}
	h := New(cfg)
	if h == nil {
		t.Fatal("New returned nil with a store and a queue")
	}
	return h, queue
}

// asAnalyst puts the analyst persona on the context, as the tool-call
// middleware does for a real call.
func asAnalyst(t *testing.T) context.Context {
	t.Helper()
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserEmail: "analyst@example.com", UserID: "u1", PersonaName: "analyst",
	})
}

// asAdmin puts the admin persona on the context.
func asAdmin(t *testing.T) context.Context {
	t.Helper()
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserEmail: "admin@example.com", UserID: "u0", PersonaName: "admin",
	})
}

// resultJSON decodes a tool result's text block.
func resultJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	text := resultText(t, res)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decoding the result: %v (text: %s)", err, text)
	}
	return out
}

// resultText returns a tool result's text block, failing rather than
// panicking when the result is not shaped as one.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("the result carries no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("the result's content is %T, not text", res.Content[0])
	}
	return text.Text
}

// listed returns the channels a list result carries, failing rather than
// panicking when the result is not the documented shape.
func listed(t *testing.T, out map[string]any) []any {
	t.Helper()
	channels, ok := out["channels"].([]any)
	if !ok {
		t.Fatalf("the result carries no channels array: %v", out)
	}
	return channels
}

// entry reads one listed channel as an object.
func entry(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("a listed channel is %T, not an object", v)
	}
	return m
}

// numberField reads a numeric field out of a tool result.
func numberField(t *testing.T, out map[string]any, key string) float64 {
	t.Helper()
	n, ok := out[key].(float64)
	if !ok {
		t.Fatalf("result field %q is %T, not a number: %v", key, out[key], out)
	}
	return n
}

// chatChannel and emailChannel are the two shapes every case below is built
// from.
func chatChannel(name, conn string) notification.Channel {
	return notification.Channel{
		Name: name, Kind: notification.ChannelKindMattermost, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: conn, Target: "C1",
		Description: "operations",
	}
}

func emailChannel(name string) notification.Channel {
	return notification.Channel{
		Name: name, Kind: notification.ChannelKindEmail, Enabled: true,
		Mode: notification.ChannelModeImmediate, Recipients: []string{"ops@example.com"},
	}
}

func TestList_ShowsOnlyWhatTheCallerReaches(t *testing.T) {
	// A channel's authorization is its connection's. A persona denied the
	// connection must not even learn the channel exists.
	h, _ := testHandle(t, []notification.Channel{
		chatChannel("reachable", "slack-ok"),
		chatChannel("denied", "slack-secret"),
		emailChannel("ops-email"),
	}, []string{"slack-ok"})

	res, _, err := h.handle(asAnalyst(t), notifyInput{Action: "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	out := resultJSON(t, res)
	names := map[string]bool{}
	for _, c := range listed(t, out) {
		name, _ := entry(t, c)["name"].(string)
		names[name] = true
	}
	if !names["reachable"] {
		t.Error("a channel whose connection the persona reaches was not listed")
	}
	if names["denied"] {
		t.Error("a channel whose connection the persona is denied was listed")
	}
	// An email channel names no connection, so there is no upstream
	// authorization to inherit and everyone the tool reaches it.
	if !names["ops-email"] {
		t.Error("an email channel was withheld though it names no connection")
	}
}

func TestList_AdminReachesEverything(t *testing.T) {
	h, _ := testHandle(t, []notification.Channel{chatChannel("denied", "slack-secret")}, nil)
	res, _, err := h.handle(asAdmin(t), notifyInput{Action: "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := len(listed(t, resultJSON(t, res))); got != 1 {
		t.Errorf("an administrator saw %d channels, want 1: admin reach is unrestricted by design", got)
	}
}

func TestList_DisabledChannelsAreNotOffered(t *testing.T) {
	ch := chatChannel("off", "slack-ok")
	ch.Enabled = false
	h, _ := testHandle(t, []notification.Channel{ch}, []string{"slack-ok"})
	res, _, err := h.handle(asAnalyst(t), notifyInput{Action: "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	out := resultJSON(t, res)
	if got := len(listed(t, out)); got != 0 {
		t.Errorf("listed %d disabled channels; a list exists to be chosen from", got)
	}
	if note, _ := out["note"].(string); !strings.Contains(note, "Admin") {
		t.Errorf("an empty list said %q; it should say where channels come from", note)
	}
}

func TestSend_QueuesTheDocument(t *testing.T) {
	h, queue := testHandle(t, []notification.Channel{chatChannel("ops", "slack-ok")}, []string{"slack-ok"})
	res, _, err := h.handle(asAnalyst(t), notifyInput{
		Action: "send", Channel: "ops", Title: "Threshold crossed",
		Body: "queue depth 812", Link: "https://portal.example.com/r/1",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.IsError {
		t.Fatalf("send was refused: %v", resultJSON(t, res))
	}
	if len(queue.rows) != 1 {
		t.Fatalf("queued %d rows, want 1", len(queue.rows))
	}
	doc := queue.rows[0].Payload.Document
	if doc == nil || doc.Title != "Threshold crossed" || doc.Body != "queue depth 812" {
		t.Errorf("queued document = %+v, want the one that was sent", doc)
	}
	out := resultJSON(t, res)
	if out["delivered"] != any(false) {
		t.Error("the result claimed delivery; a send is queued, not delivered")
	}
	if numberField(t, out, "queued") != 1 {
		t.Errorf("queued = %v, want 1", out["queued"])
	}
}

func TestSend_RefusesAChannelTheCallerCannotReach(t *testing.T) {
	// The refusal must not distinguish "exists but is not yours" from "does
	// not exist": two distinguishable answers enumerate the channel list one
	// guess at a time.
	h, queue := testHandle(t, []notification.Channel{chatChannel("secret", "slack-secret")}, nil)
	denied, _, err := h.handle(asAnalyst(t), notifyInput{Action: "send", Channel: "secret", Title: "t"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	absent, _, err := h.handle(asAnalyst(t), notifyInput{Action: "send", Channel: "no-such-channel", Title: "t"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !denied.IsError || !absent.IsError {
		t.Fatal("a channel the caller cannot reach was accepted")
	}
	deniedText := resultText(t, denied)
	absentText := resultText(t, absent)
	if strings.ReplaceAll(deniedText, "secret", "X") != strings.ReplaceAll(absentText, "no-such-channel", "X") {
		t.Errorf("the two refusals differ, which enumerates the channel list:\n  denied: %s\n  absent: %s", deniedText, absentText)
	}
	if len(queue.rows) != 0 {
		t.Error("a refused send queued a row")
	}
}

func TestSend_RefusesAMalformedDocument(t *testing.T) {
	h, queue := testHandle(t, []notification.Channel{chatChannel("ops", "slack-ok")}, []string{"slack-ok"})
	res, _, err := h.handle(asAnalyst(t), notifyInput{Action: "send", Channel: "ops", Body: "no title"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !res.IsError {
		t.Fatal("a document with no title was accepted")
	}
	if len(queue.rows) != 0 {
		t.Error("a refused send queued a row")
	}
}

func TestSend_RefusesAnEmptyChannelName(t *testing.T) {
	h, _ := testHandle(t, nil, nil)
	res, _, err := h.handle(asAnalyst(t), notifyInput{Action: "send", Title: "t"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "action=list") {
		t.Error("an empty channel was not refused with a pointer to the list action")
	}
}

func TestHandle_UnknownAndMissingActions(t *testing.T) {
	h, _ := testHandle(t, nil, nil)
	for _, tc := range []struct{ action, wants string }{
		{"", "action is required"},
		{"broadcast", "unknown action"},
	} {
		res, _, err := h.handle(asAnalyst(t), notifyInput{Action: tc.action})
		if err != nil {
			t.Fatalf("%q: %v", tc.action, err)
		}
		if !res.IsError || !strings.Contains(resultText(t, res), tc.wants) {
			t.Errorf("action %q was not refused with %q", tc.action, tc.wants)
		}
	}
}

func TestPublish_BuildsTheDocumentFromTheAsset(t *testing.T) {
	asset := &portaldomain.Asset{
		ID: "asset_1", Name: "Weekly report", Description: "Revenue by region",
		ContentType: "text/markdown",
	}
	h, queue := testHandle(t, []notification.Channel{chatChannel("ops", "slack-ok")}, []string{"slack-ok"},
		func(c *Config) {
			c.Assets = &memAssets{asset: asset, content: []byte("# Revenue\n\n| a | b |")}
			c.Access = allowAll{}
		})

	res, _, err := h.handle(asAnalyst(t), notifyInput{
		Action: "publish", Channel: "ops", Asset: "asset_1", Message: "This week's numbers",
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.IsError {
		t.Fatalf("publish was refused: %s", resultText(t, res))
	}
	doc := queue.rows[0].Payload.Document
	if doc.Title != "Weekly report" {
		t.Errorf("title = %q, want the asset's name", doc.Title)
	}
	for _, want := range []string{"This week's numbers", "Revenue by region", "# Revenue"} {
		if !strings.Contains(doc.Body, want) {
			t.Errorf("body is missing %q; got %q", want, doc.Body)
		}
	}
	if doc.Link != "https://portal.example.com/portal/assets/asset_1" {
		t.Errorf("link = %q, want the asset's portal page", doc.Link)
	}
}

func TestPublish_RefusesAnAssetTheCallerCannotRead(t *testing.T) {
	asset := &portaldomain.Asset{ID: "asset_1", Name: "Private", ContentType: "text/markdown"}
	h, queue := testHandle(t, []notification.Channel{chatChannel("ops", "slack-ok")}, []string{"slack-ok"},
		func(c *Config) {
			c.Assets = &memAssets{asset: asset, content: []byte("secret")}
			c.Access = denyAll{}
		})
	res, _, err := h.handle(asAnalyst(t), notifyInput{Action: "publish", Channel: "ops", Asset: "asset_1"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !res.IsError {
		t.Fatal("an asset the caller cannot read was published")
	}
	if len(queue.rows) != 0 {
		t.Error("a refused publish queued a row")
	}
}

func TestPublish_UnavailableWithoutThePortalStores(t *testing.T) {
	// A deployment that cannot check entitlement must not publish on the
	// strength of an id.
	h, _ := testHandle(t, []notification.Channel{chatChannel("ops", "slack-ok")}, []string{"slack-ok"})
	res, _, err := h.handle(asAnalyst(t), notifyInput{Action: "publish", Channel: "ops", Asset: "asset_1"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "action=send") {
		t.Error("publish without portal stores did not refuse with the alternative")
	}
}

func TestPublish_ContentTypesThatCannotBeShownContributeNoBody(t *testing.T) {
	// A rendered HTML page in a chat message is markup nobody reads. The
	// message is then the name and the link, which is what a reader acts on.
	asset := &portaldomain.Asset{ID: "a1", Name: "Dashboard", ContentType: "text/html"}
	h, queue := testHandle(t, []notification.Channel{chatChannel("ops", "slack-ok")}, []string{"slack-ok"},
		func(c *Config) {
			c.Assets = &memAssets{asset: asset, content: []byte("<html><body>…</body></html>")}
			c.Access = allowAll{}
		})
	if _, _, err := h.handle(asAnalyst(t), notifyInput{Action: "publish", Channel: "ops", Asset: "a1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if body := queue.rows[0].Payload.Document.Body; strings.Contains(body, "<html>") {
		t.Errorf("raw html reached the message body: %q", body)
	}
}

func TestAssetID_AcceptsAPastedPortalURL(t *testing.T) {
	// Somebody publishing the page they are looking at should not have to
	// find the id inside the URL.
	for _, tc := range []struct{ in, want string }{
		{"asset_1", "asset_1"},
		{"https://portal.example.com/portal/assets/asset_1", "asset_1"},
		{"https://portal.example.com/portal/assets/asset_1?tab=history", "asset_1"},
		{"https://portal.example.com/portal/assets/asset_1#top", "asset_1"},
		{"  asset_1  ", "asset_1"},
		{"", ""},
	} {
		if got := assetID(tc.in); got != tc.want {
			t.Errorf("assetID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExcerptOf_CutsAtALineBoundaryAndSaysSo(t *testing.T) {
	long := strings.Repeat("a line of the report\n", 2000)
	got := excerptOf(long)
	if len(got) > publishBodyBytes+64 {
		t.Errorf("excerpt is %d bytes, well over the %d-byte bound", len(got), publishBodyBytes)
	}
	if !strings.Contains(got, "truncated") {
		t.Error("a cut excerpt did not say it was cut")
	}
	if short := excerptOf("all of it"); short != "all of it" {
		t.Errorf("a short body was changed: %q", short)
	}
}

func TestReadableAsText(t *testing.T) {
	for ct, want := range map[string]bool{
		"text/markdown":                true,
		"text/markdown; charset=utf-8": true,
		"text/csv":                     true,
		"application/json":             true,
		"text/html":                    false,
		"text/jsx":                     false,
		"image/png":                    false,
		"":                             false,
	} {
		if got := readableAsText(ct); got != want {
			t.Errorf("readableAsText(%q) = %v, want %v", ct, got, want)
		}
	}
}

func TestNew_RefusesToRegisterWithoutAStoreOrAQueue(t *testing.T) {
	// A tool that lists destinations it cannot write to advertises a
	// capability the deployment does not have.
	if h := New(Config{}); h != nil {
		t.Error("New built a handle with no channel store and no queue")
	}
	if h := New(Config{Channels: &memChannels{}}); h != nil {
		t.Error("New built a handle with no queue")
	}
}

func TestRegisterTool_NilHandleRegistersNothing(_ *testing.T) {
	var h *Handle
	h.RegisterTool(nil) // must not panic
}

func TestCarries_DescribesEveryKind(t *testing.T) {
	for _, kind := range []string{
		notification.ChannelKindMattermost, notification.ChannelKindMattermost,
		notification.ChannelKindWebhook, notification.ChannelKindEmail, "future-kind",
	} {
		if carries(kind) == "" {
			t.Errorf("kind %q describes nothing it can carry", kind)
		}
	}
}

// TestInputSchema_ActionEnum pins the verbs notify advertises as an enum
// (#1827): the draft write barrier's verb gate reads them from the schema, and
// a verb added to the handler without it is one the gate cannot see.
func TestInputSchema_ActionEnum(t *testing.T) {
	s := inputSchema()
	action := s.Properties["action"]
	if action == nil {
		t.Fatal("the schema has no action property")
	}
	want := []any{actionList, actionSend, actionPublish}
	if len(action.Enum) != len(want) {
		t.Fatalf("action enum = %v, want %v", action.Enum, want)
	}
	for i := range want {
		if action.Enum[i] != want[i] {
			t.Fatalf("action enum = %v, want %v", action.Enum, want)
		}
	}
	if _, ok := s.Properties["channel"]; !ok {
		t.Error("the rest of the inferred schema should be kept")
	}
}
