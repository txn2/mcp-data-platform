package notifylayer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
)

// memShares is the share store a publish writes through.
type memShares struct {
	shares  []portaldomain.Share
	revoked []string
	err     error
}

func (m *memShares) Insert(_ context.Context, s *portaldomain.Share) error {
	if m.err != nil {
		return m.err
	}
	s.CreatedAt = time.Now()
	m.shares = append(m.shares, *s)
	return nil
}

func (m *memShares) ListByAsset(_ context.Context, id string) ([]portaldomain.Share, error) {
	var out []portaldomain.Share
	for _, s := range m.shares {
		if s.AssetID == id {
			out = append(out, s)
		}
	}
	return out, m.err
}

func (m *memShares) Revoke(_ context.Context, id string) error {
	m.revoked = append(m.revoked, id)
	for i := range m.shares {
		if m.shares[i].ID == id {
			m.shares[i].Revoked = true
		}
	}
	return nil
}

// memTargets answers every listing with the same chat channels.
type memTargets struct {
	targets []notification.ChatTarget
	err     error
	calls   int
}

func (m *memTargets) Targets(context.Context, notification.Channel) ([]notification.ChatTarget, error) {
	m.calls++
	return m.targets, m.err
}

// botChannel is a mattermost channel with no target of its own: it stands
// for its bot, whose channels a post chooses among.
func botChannel() notification.Channel {
	return notification.Channel{
		Name: "mm", Kind: notification.ChannelKindMattermost, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: "chat",
	}
}

// ownedAsset is an asset the analyst owns.
func ownedAsset() *portaldomain.Asset {
	return &portaldomain.Asset{ID: "a1", OwnerID: "u1", OwnerEmail: "analyst@example.com", Name: "Weekly", ContentType: "text/markdown"}
}

// shareHandle is a tool that can create shares and list a bot's channels.
func shareHandle(t *testing.T, asset *portaldomain.Asset, opts ...func(*Config)) (*Handle, *memQueue, *memShares) {
	t.Helper()
	shares := &memShares{}
	h, queue := testHandle(t, []notification.Channel{botChannel(), emailChannel("list")}, []string{"chat"}, append([]func(*Config){func(c *Config) {
		c.Assets = &memAssets{asset: asset, content: []byte("# Weekly\n\nnumbers")}
		c.Access = allowAll{}
		c.Shares = shares
		c.Targets = &memTargets{targets: []notification.ChatTarget{{ID: "town", Name: "town-square"}}}
	}}, opts...)...)
	return h, queue, shares
}

func TestPublish_SignedInCreatesAChannelShareAndPostsItsLink(t *testing.T) {
	h, queue, shares := shareHandle(t, ownedAsset())
	res := callNotify(t, h, `{"action":"publish","channel":"mm","target":"town","asset":"a1"}`)
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	if len(shares.shares) != 1 {
		t.Fatalf("created %d shares, want one", len(shares.shares))
	}
	s := shares.shares[0]
	if s.AccessMode != shareaccess.ModeAuthenticated || s.Origin != portaldomain.OriginChannelPost ||
		s.PostedTo != "#town-square (mm)" || s.ExpiresAt != nil {
		t.Errorf("share = %+v, want a signed-in channel_post share naming where it went", s)
	}
	doc := queue.rows[0].Payload.Document
	if doc.Link != "https://portal.example.com/portal/view/"+s.Token || doc.Target != "town" {
		t.Errorf("posted link %q to %q, want the share's view link to the chosen channel", doc.Link, doc.Target)
	}
	out := resultJSON(t, res)
	if out["access"] != accessSignedIn || out["share_id"] != s.ID {
		t.Errorf("result = %v", out)
	}
}

func TestPublish_PostingAgainReusesTheSignedInShare(t *testing.T) {
	h, queue, shares := shareHandle(t, ownedAsset())
	for range 2 {
		if res := callNotify(t, h, `{"action":"publish","channel":"mm","target":"town","asset":"a1"}`); res.IsError {
			t.Fatal(resultText(t, res))
		}
	}
	if len(shares.shares) != 1 || len(queue.rows) != 2 {
		t.Fatalf("two posts made %d shares and %d rows, want one share posted twice", len(shares.shares), len(queue.rows))
	}
	if queue.rows[0].Payload.Document.Link != queue.rows[1].Payload.Document.Link {
		t.Error("the second post carries a different link")
	}
	// A revoked share is not reused: the next post makes a new one.
	shares.shares[0].Revoked = true
	if res := callNotify(t, h, `{"action":"publish","channel":"mm","target":"town","asset":"a1"}`); res.IsError {
		t.Fatal(resultText(t, res))
	}
	if len(shares.shares) != 2 {
		t.Errorf("after the revoke, %d shares exist, want a new one", len(shares.shares))
	}
}

func TestPublish_PublicNeedsAnExpiryAndExpires(t *testing.T) {
	h, _, shares := shareHandle(t, ownedAsset())
	res := callNotify(t, h, `{"action":"publish","access":"public","channel":"list","asset":"a1"}`)
	if !res.IsError || !strings.Contains(resultText(t, res), "expires_in is required") {
		t.Fatalf("a public post with no expiry was not refused: %s", resultText(t, res))
	}
	res = callNotify(t, h, `{"action":"publish","access":"public","expires_in":"24h","channel":"list","asset":"a1"}`)
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	if len(shares.shares) != 1 || shares.shares[0].AccessMode != shareaccess.ModePublic || shares.shares[0].ExpiresAt == nil {
		t.Fatalf("shares = %+v, want one expiring public share", shares.shares)
	}
	if shares.shares[0].PostedTo != "list" {
		t.Errorf("posted_to = %q, want the email channel's name", shares.shares[0].PostedTo)
	}
	if resultJSON(t, res)["expires_at"] == nil {
		t.Error("the result does not say when the link expires")
	}
}

func TestPublish_ExistingPostsTheAssetPageAndMakesNoShare(t *testing.T) {
	notOwned := ownedAsset()
	notOwned.OwnerID, notOwned.OwnerEmail = "u9", "someone@example.com"
	h, queue, shares := shareHandle(t, notOwned)
	res := callNotify(t, h, `{"action":"publish","access":"existing","channel":"mm","target":"town","asset":"a1"}`)
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	if len(shares.shares) != 0 || queue.rows[0].Payload.Document.Link != "https://portal.example.com/portal/assets/a1" {
		t.Errorf("existing made %d shares and linked %q", len(shares.shares), queue.rows[0].Payload.Document.Link)
	}
	res = callNotify(t, h, `{"action":"publish","access":"existing","expires_in":"1h","channel":"mm","target":"town","asset":"a1"}`)
	if !res.IsError {
		t.Error("an expiry on an existing-access post was accepted")
	}
}

func TestPublish_ARefusalNamesTheWayOut(t *testing.T) {
	notOwned := ownedAsset()
	notOwned.OwnerID, notOwned.OwnerEmail = "u9", "someone@example.com"
	for _, tc := range []struct {
		name  string
		asset *portaldomain.Asset
		opt   func(*Config)
		args  string
		wants string
	}{
		{"a non-owner", notOwned, nil, `{"action":"publish","channel":"mm","target":"town","asset":"a1"}`, "only the asset's owner"},
		{
			"a persona without sharing", ownedAsset(), func(c *Config) { c.SharingAllowed = func(string) bool { return false } },
			`{"action":"publish","channel":"mm","target":"town","asset":"a1"}`, "sharing is not available",
		},
		{
			"no share store", ownedAsset(), func(c *Config) { c.Shares = nil },
			`{"action":"publish","channel":"mm","target":"town","asset":"a1"}`, "cannot create a share",
		},
		{"an unknown access", ownedAsset(), nil, `{"action":"publish","access":"everyone","channel":"mm","target":"town","asset":"a1"}`, "must be signed_in"},
		{"a target the bot is not in", ownedAsset(), nil, `{"action":"publish","channel":"mm","target":"elsewhere","asset":"a1"}`, "not in a chat channel"},
		{"no target on a bot channel", ownedAsset(), nil, `{"action":"publish","channel":"mm","asset":"a1"}`, "no chat channel of its own"},
		{"a target on an email channel", ownedAsset(), nil, `{"action":"publish","channel":"list","target":"town","asset":"a1"}`, "remove the target"},
		{"an expiry on signed_in", ownedAsset(), nil, `{"action":"publish","expires_in":"1h","channel":"mm","target":"town","asset":"a1"}`, "signed-in users"},
		{
			"a listing that fails", ownedAsset(), func(c *Config) { c.Targets = &memTargets{err: errors.New("down")} },
			`{"action":"publish","channel":"mm","target":"town","asset":"a1"}`, "reading the chat channels",
		},
		{
			"no listing at all", ownedAsset(), func(c *Config) { c.Targets = nil },
			`{"action":"publish","channel":"mm","target":"town","asset":"a1"}`, "cannot list a bot's chat channels",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var opts []func(*Config)
			if tc.opt != nil {
				opts = append(opts, tc.opt)
			}
			h, queue, _ := shareHandle(t, tc.asset, opts...)
			res := callNotify(t, h, tc.args)
			if !res.IsError || !strings.Contains(resultText(t, res), tc.wants) {
				t.Fatalf("got %s, want a refusal naming %q", resultText(t, res), tc.wants)
			}
			if len(queue.rows) != 0 {
				t.Error("a refused publish queued a row")
			}
		})
	}
}

func TestPublish_AShareIsWithdrawnWhenNothingWasPosted(t *testing.T) {
	// A key the fake queue absorbs: the send is held back, so the link the
	// publish made was handed to nobody.
	sh := &memShares{}
	h, queue := testHandle(t, []notification.Channel{botChannel()}, []string{"chat"}, func(c *Config) {
		c.Assets = &memAssets{asset: ownedAsset(), content: []byte("x")}
		c.Access = allowAll{}
		c.Shares = sh
		c.Targets = &memTargets{targets: []notification.ChatTarget{{ID: "town", Name: "town-square"}}}
	})
	queue.suppress = true
	res := callNotify(t, h, `{"action":"publish","channel":"mm","target":"town","asset":"a1","key":"k"}`)
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	if len(sh.revoked) != 1 {
		t.Errorf("revoked %v, want the share a held-back post made", sh.revoked)
	}
	if _, ok := resultJSON(t, res)["share_id"]; ok {
		t.Error("a held-back post reports a share")
	}
}

func TestPublish_AScriptRunPublishesWhatItsAuthorOwns(t *testing.T) {
	asset := ownedAsset()
	asset.OwnerID = "script:weekly"
	h, _, _ := shareHandle(t, asset)
	c := caller{userID: "script:other", email: "", onBehalfOf: "analyst@example.com", persona: "analyst"}
	if err := h.maySharePost(asset, c); err != nil {
		t.Errorf("a run acting for the owner was refused: %v", err)
	}
	if err := h.maySharePost(asset, caller{userID: "script:other", persona: "analyst"}); err == nil {
		t.Error("a run acting for nobody was allowed to share")
	}
}

func TestList_ReportsEachBotsChannels(t *testing.T) {
	targets := &memTargets{targets: []notification.ChatTarget{{ID: "town", Name: "town-square"}}}
	h, _, _ := shareHandle(t, ownedAsset(), func(c *Config) { c.Targets = targets })
	out := resultJSON(t, callNotify(t, h, `{"action":"list"}`))
	var mm, list map[string]any
	for _, v := range listed(t, out) {
		e := entry(t, v)
		switch e["name"] {
		case "mm":
			mm = e
		case "list":
			list = e
		}
	}
	got, ok := mm["targets"].([]any)
	if !ok || len(got) != 1 || entry(t, got[0])["id"] != "town" {
		t.Errorf("the bot channel lists %v, want its bot's channels", mm["targets"])
	}
	if emails, ok := list["targets"].([]any); !ok || len(emails) != 0 {
		t.Errorf("the email channel lists %v, want an empty list", list["targets"])
	}
	if targets.calls != 1 {
		t.Errorf("listed %d times, want once for the one bot channel", targets.calls)
	}

	failing, _, _ := shareHandle(t, ownedAsset(), func(c *Config) { c.Targets = &memTargets{err: errors.New("down")} })
	for _, v := range listed(t, resultJSON(t, callNotify(t, failing, `{"action":"list"}`))) {
		if e := entry(t, v); e["name"] == "mm" && e["targets_error"] != "down" {
			t.Errorf("a failed listing reads %v", e)
		}
	}
}

func TestSend_ATargetGoesOnTheDocument(t *testing.T) {
	h, queue, _ := shareHandle(t, ownedAsset())
	res := callNotify(t, h, `{"action":"send","channel":"mm","target":"town","title":"hello"}`)
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	if queue.rows[0].Payload.Document.Target != "town" {
		t.Errorf("target = %q", queue.rows[0].Payload.Document.Target)
	}
	if res := callNotify(t, h, `{"action":"send","channel":"mm","target":"nowhere","title":"hello"}`); !res.IsError {
		t.Error("a target the bot is not in was accepted")
	}
}

func TestDailyChannel_ListsNoTargetsAndTakesNone(t *testing.T) {
	daily := notification.Channel{
		Name: "digest", Kind: notification.ChannelKindMattermost, Enabled: true,
		Mode: notification.ChannelModeDaily, Connection: "chat", Target: "own",
	}
	targets := &memTargets{targets: []notification.ChatTarget{{ID: "town", Name: "town-square"}}}
	h, queue := testHandle(t, []notification.Channel{daily}, []string{"chat"}, func(c *Config) {
		c.Assets = &memAssets{asset: ownedAsset(), content: []byte("x")}
		c.Access = allowAll{}
		c.Shares = &memShares{}
		c.Targets = targets
	})
	for _, v := range listed(t, resultJSON(t, callNotify(t, h, `{"action":"list"}`))) {
		if got, _ := entry(t, v)["targets"].([]any); len(got) != 0 {
			t.Errorf("a daily channel listed targets %v", got)
		}
	}
	if targets.calls != 0 {
		t.Errorf("a daily channel's bot was listed %d times", targets.calls)
	}
	res := callNotify(t, h, `{"action":"send","channel":"digest","target":"town","title":"t"}`)
	if !res.IsError || !strings.Contains(resultText(t, res), "daily bulletin") {
		t.Errorf("a target on a daily channel: %s", resultText(t, res))
	}
	if res := callNotify(t, h, `{"action":"send","channel":"digest","title":"t"}`); res.IsError || len(queue.rows) != 1 {
		t.Errorf("a daily channel's own post was refused: %s", resultText(t, res))
	}
}

func TestPublish_AFailedShareOrRefusedSendLeavesNoLink(t *testing.T) {
	failing := &memShares{err: errors.New("db down")}
	h, queue, _ := shareHandle(t, ownedAsset(), func(c *Config) { c.Shares = failing })
	res := callNotify(t, h, `{"action":"publish","channel":"mm","target":"town","asset":"a1"}`)
	if !res.IsError || !strings.Contains(resultText(t, res), "creating the link share") || len(queue.rows) != 0 {
		t.Errorf("a failed share insert: %s, %d rows", resultText(t, res), len(queue.rows))
	}

	h, queue, shares := shareHandle(t, ownedAsset())
	res = callNotify(t, h, `{"action":"publish","channel":"mm","target":"town","asset":"a1","repeat_after":"soon"}`)
	if !res.IsError || len(queue.rows) != 0 {
		t.Fatalf("a refused send was posted: %s", resultText(t, res))
	}
	if len(shares.revoked) != 1 {
		t.Errorf("the share made for a refused send was not withdrawn: %v", shares.revoked)
	}

	// A deployment with no public address posts no link at all.
	h, queue, _ = shareHandle(t, ownedAsset(), func(c *Config) { c.PortalURL = "" })
	if res := callNotify(t, h, `{"action":"publish","channel":"mm","target":"town","asset":"a1"}`); res.IsError {
		t.Fatal(resultText(t, res))
	}
	if link := queue.rows[0].Payload.Document.Link; link != "" {
		t.Errorf("link = %q with no public address", link)
	}
}
