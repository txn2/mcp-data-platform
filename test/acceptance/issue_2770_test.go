//go:build integration

package acceptance

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// Issue #2770: several Slack or Mattermost channels sharing one bot. Each
// lists the bot's chat channels, and the list reads the bot once per
// connection however many channels deliver through it. The Share dialog then
// offers each chat channel once, posting through the channel addressed to it
// (ui/src/components/share/ChannelView.tsx destinations, held by
// model.test.ts and e2e/interactive/share-to-channel.spec.ts); a post
// through that channel is recorded against it.
//
// Upstream: Mattermost from make dev (dev/.mattermost-env), on the dev
// stack's mattermost-dev connection.
//
// Wire forms: notify takes action as the string "list". GET
// /api/v1/portal/channels has no body. The admin channel PUT body takes kind,
// connection and target as strings. The channel-share body takes channel,
// target and access_mode ("authenticated") as strings.

const (
	conn2770 = "mattermost-dev"
	bot2770  = "acc-2770-bot"
	ownA2770 = "acc-2770-a"
	ownB2770 = "acc-2770-b"
)

// outbound2770 is how many requests every replica has sent through the
// connection, summed.
func outbound2770(t *testing.T) float64 {
	t.Helper()
	return metricSeries(scrapeRaw(t), "http_client_requests_total", map[string]string{"connection": conn2770})
}

// listCost2770 lists the channels as the Share dialog does and returns the
// requests the listing sent through the connection.
func listCost2770(t *testing.T, c *client) float64 {
	t.Helper()
	before := outbound2770(t)
	if status, out := c.rest(http.MethodGet, "/api/v1/portal/channels", http.NoBody); status != http.StatusOK {
		t.Fatalf("listing channels: %d %v", status, out)
	}
	return outbound2770(t) - before
}

// TestIssue2770_ChannelsOnOneBotShareOneListing: with one channel on the bot
// the listing costs some number of requests; with two more channels on the
// same bot, each addressed to its own chat channel, it costs the same, and
// all three list the same chat channels under targets, through the portal
// route and through notify.
func TestIssue2770_ChannelsOnOneBotShareOneListing(t *testing.T) {
	admin := connect(t)
	env := readMattermostEnv(t)
	putChannel(t, admin, bot2770, map[string]any{"kind": "mattermost", "connection": conn2770})
	t.Cleanup(func() { deleteChannel(t, admin, bot2770) })

	chats := channelTargets(t, admin, bot2770)
	if _, ok := chats[env.channelID]; !ok {
		t.Fatalf("the bot's chat channels %v do not hold the fixture channel %s", chats, env.channelID)
	}
	other := env.channelID
	for id := range chats {
		if id != env.channelID {
			other = id
			break
		}
	}

	alone := listCost2770(t, admin)
	if alone < 1 {
		t.Fatalf("listing the bot sent %v requests through %s; the connection label is not what the listing reports under", alone, conn2770)
	}

	putChannel(t, admin, ownA2770, map[string]any{"kind": "mattermost", "connection": conn2770, "target": env.channelID})
	t.Cleanup(func() { deleteChannel(t, admin, ownA2770) })
	putChannel(t, admin, ownB2770, map[string]any{"kind": "mattermost", "connection": conn2770, "target": other})
	t.Cleanup(func() { deleteChannel(t, admin, ownB2770) })

	if shared := listCost2770(t, admin); shared != alone {
		t.Errorf("with three channels on the bot the listing sent %v requests, with one %v; want one listing per bot", shared, alone)
	}

	want := slices.Sorted(maps.Keys(chats))
	for _, name := range []string{bot2770, ownA2770, ownB2770} {
		if got := slices.Sorted(maps.Keys(channelTargets(t, admin, name))); !slices.Equal(got, want) {
			t.Errorf("the portal lists %v under %s, want the bot's %v", got, name, want)
		}
	}

	listed := admin.call("notify", map[string]any{"action": "list"})
	entries, _ := listed["channels"].([]any)
	seen := map[string]bool{}
	for _, raw := range entries {
		ch, _ := raw.(map[string]any)
		name, _ := ch["name"].(string)
		if !strings.HasPrefix(name, "acc-2770-") {
			continue
		}
		seen[name] = true
		var got []string
		ts, _ := ch["targets"].([]any)
		for _, tr := range ts {
			tm, _ := tr.(map[string]any)
			got = append(got, fmt.Sprint(tm["id"]))
		}
		if slices.Sort(got); !slices.Equal(got, want) {
			t.Errorf("notify lists %v under %s, want the bot's %v", got, name, want)
		}
	}
	if len(seen) != 3 {
		t.Errorf("notify listed %v of the three channels", seen)
	}
}

// TestIssue2770_APostThroughTheAddressedChannelIsRecordedAgainstIt: the
// dialog posts a chat channel through the channel addressed to it, and the
// asset's share list names that channel.
func TestIssue2770_APostThroughTheAddressedChannelIsRecordedAgainstIt(t *testing.T) {
	admin := connect(t)
	env := readMattermostEnv(t)
	putChannel(t, admin, bot2770, map[string]any{"kind": "mattermost", "connection": conn2770})
	t.Cleanup(func() { deleteChannel(t, admin, bot2770) })
	putChannel(t, admin, ownA2770, map[string]any{"kind": "mattermost", "connection": conn2770, "target": env.channelID})
	t.Cleanup(func() { deleteChannel(t, admin, ownA2770) })

	assetID, _ := saveReport(t, admin, "2770")
	status, out := shareToChannel(t, admin, assetID, map[string]any{
		"channel": ownA2770, "target": env.channelID, "access_mode": "authenticated",
	})
	if status != http.StatusOK {
		t.Fatalf("posting through %s: %d %v", ownA2770, status, out)
	}
	var postedTo []string
	for _, s := range assetShares(t, admin, assetID) {
		if p, _ := s["posted_to"].(string); p != "" {
			postedTo = append(postedTo, p)
		}
	}
	if len(postedTo) != 1 || !strings.HasSuffix(postedTo[0], "("+ownA2770+")") {
		t.Errorf("the asset's posts read %v, want one naming %s", postedTo, ownA2770)
	}
}
