package notifypost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// maxTargets bounds how many chat channels one connection's listing returns.
// A bot is in the channels it was invited to, which is a handful; the cap is
// what keeps a bot added to every channel of a large workspace from turning
// one dialog into a long walk of the upstream's pages.
const maxTargets = 500

// slackListPage is how many channels one users.conversations page asks for.
const slackListPage = 200

// The Web API method and REST routes a listing is read from.
const (
	slackUserConversations = "/users.conversations"
	mattermostMyTeams      = "/api/v4/users/me/teams"
	mattermostTeamChannels = "/api/v4/users/me/teams/%s/channels"
)

// Slack's two channel types a bot posts to, and the error code a token
// without groups:read answers a listing that asks for private ones with.
const (
	slackPublicAndPrivate = "public_channel,private_channel"
	slackPublicOnly       = "public_channel"
	slackMissingScope     = "missing_scope"
)

// Mattermost's channel types: open and private. Direct and group messages
// are left out, since an asset shared there reaches one or two people, which
// sharing with a person already does.
const (
	mattermostOpen    = "O"
	mattermostPrivate = "P"
)

// errNoListing is a kind that has no chat channels to choose among.
var errNoListing = errors.New("this kind has no chat channels to choose among")

// Targets returns the chat channels the bot of ch's connection is in, by
// name: Slack's users.conversations, Mattermost's channels of every team the
// bot is on (#2241). Read live, so a channel the bot was invited to a minute
// ago is listed and one it left is not.
func (s *Senders) Targets(ctx context.Context, ch notification.Channel) ([]notification.ChatTarget, error) {
	if s == nil {
		return nil, errNoListing
	}
	u, err := resolve(s.upstream, ch)
	if err != nil {
		return nil, err
	}
	var out []notification.ChatTarget
	switch ch.Kind {
	case notification.ChannelKindSlack:
		out, err = slackTargets(ctx, u)
	case notification.ChannelKindMattermost:
		out, err = mattermostTargets(ctx, u)
	default:
		return nil, errNoListing
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// slackTargets reads the bot's channels page by page. A token without
// groups:read is refused a listing that includes private channels, so that
// refusal asks again for the public ones alone rather than listing nothing.
func slackTargets(ctx context.Context, u Upstream) ([]notification.ChatTarget, error) {
	out, err := slackConversations(ctx, u, slackPublicAndPrivate)
	if err != nil && strings.Contains(err.Error(), slackMissingScope) {
		return slackConversations(ctx, u, slackPublicOnly)
	}
	return out, err
}

// slackConversationsPage is the part of a users.conversations answer a
// listing reads.
type slackConversationsPage struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	Channels []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		IsPrivate bool   `json:"is_private"`
	} `json:"channels"`
	Metadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// slackConversations walks users.conversations for one set of types.
func slackConversations(ctx context.Context, u Upstream, types string) ([]notification.ChatTarget, error) {
	var out []notification.ChatTarget
	cursor := ""
	for len(out) < maxTargets {
		q := url.Values{}
		q.Set("types", types)
		q.Set("exclude_archived", "true")
		q.Set("limit", fmt.Sprint(slackListPage))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page slackConversationsPage
		if err := getJSON(ctx, u, slackUserConversations+"?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		if !page.OK {
			return nil, fmt.Errorf("the Slack Web API refused the channel listing (%s)", page.Error)
		}
		for _, c := range page.Channels {
			out = append(out, notification.ChatTarget{ID: c.ID, Name: c.Name, Private: c.IsPrivate})
		}
		if cursor = page.Metadata.NextCursor; cursor == "" {
			break
		}
	}
	return capTargets(out), nil
}

// mattermostTeam and mattermostChannelInfo are the parts of the answers a
// listing reads.
type mattermostTeam struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type mattermostChannelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	DeleteAt    int64  `json:"delete_at"`
}

// mattermostTargets reads the open and private channels of every team the bot
// is on, naming each by its team when the bot is on more than one.
func mattermostTargets(ctx context.Context, u Upstream) ([]notification.ChatTarget, error) {
	var teams []mattermostTeam
	if err := getJSON(ctx, u, mattermostMyTeams, &teams); err != nil {
		return nil, err
	}
	var out []notification.ChatTarget
	for _, team := range teams {
		var channels []mattermostChannelInfo
		if err := getJSON(ctx, u, fmt.Sprintf(mattermostTeamChannels, url.PathEscape(team.ID)), &channels); err != nil {
			return nil, err
		}
		prefix := ""
		if len(teams) > 1 {
			prefix = team.DisplayName + " / "
		}
		out = append(out, teamTargets(channels, prefix)...)
	}
	return capTargets(out), nil
}

// teamTargets keeps one team's open and private channels that are not
// archived, each named with prefix in front.
func teamTargets(channels []mattermostChannelInfo, prefix string) []notification.ChatTarget {
	var out []notification.ChatTarget
	for _, c := range channels {
		if c.DeleteAt != 0 || (c.Type != mattermostOpen && c.Type != mattermostPrivate) {
			continue
		}
		name := c.DisplayName
		if name == "" {
			name = c.Name
		}
		out = append(out, notification.ChatTarget{ID: c.ID, Name: prefix + name, Private: c.Type == mattermostPrivate})
	}
	return out
}

// capTargets holds a listing to maxTargets.
func capTargets(out []notification.ChatTarget) []notification.ChatTarget {
	if len(out) > maxTargets {
		return out[:maxTargets]
	}
	return out
}

// getJSON reads one GET answer under the upstream's base URL into dst,
// classifying the status as a send does.
func getJSON(ctx context.Context, u Upstream, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(u.BaseURL(), path), http.NoBody)
	if err != nil {
		return fmt.Errorf("building the request failed: %w", err)
	}
	payload, err := doRead(u, req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, dst); err != nil {
		return fmt.Errorf("the channel listing is not JSON (%s); check the connection's base_url", excerpt(payload))
	}
	return nil
}
