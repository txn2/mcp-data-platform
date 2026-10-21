package notifylayer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
)

// Who a published asset's link opens for (#2088, #2241).
const (
	// accessSignedIn creates, or reuses, a link share any signed-in user
	// opens, and posts its URL. The default.
	accessSignedIn = "signed_in"
	// accessPublic creates a link share anyone holding the URL opens until
	// it expires, and posts its URL.
	accessPublic = "public"
	// accessExisting posts the asset page's link, which opens only for the
	// people who already have access: what a reader who cannot share posts.
	accessExisting = "existing"
)

// ShareStore is the part of the portal's share store a publish writes the
// channel's link through.
type ShareStore interface {
	// Insert stores a new share.
	Insert(ctx context.Context, share *portaldomain.Share) error
	// ListByAsset returns every share on an asset.
	ListByAsset(ctx context.Context, assetID string) ([]portaldomain.Share, error)
	// Revoke ends a share.
	Revoke(ctx context.Context, id string) error
}

// TargetLister reads the chat channels a slack or mattermost channel's bot is
// in. *notifypost.Senders implements it.
type TargetLister interface {
	Targets(ctx context.Context, ch notification.Channel) ([]notification.ChatTarget, error)
}

// targetListTimeout bounds one connection's channel listing, so a chat
// service that does not answer costs a list its own channel's entry rather
// than the whole answer.
const targetListTimeout = 10 * time.Second

// postLink is what a publish posts as its link, and the share that makes it
// open for the channel's readers when the access asked for one.
type postLink struct {
	url   string
	share *portaldomain.Share
	// created reports a share made by this publish rather than reused, which
	// a publish that queued nothing revokes again.
	created bool
}

// normalizeAccess reads the access parameter, applying the default.
func normalizeAccess(in string) (string, error) {
	switch v := strings.TrimSpace(in); v {
	case "":
		return accessSignedIn, nil
	case accessSignedIn, accessPublic, accessExisting:
		return v, nil
	default:
		return "", fmt.Errorf("access %q must be %s, %s or %s", in, accessSignedIn, accessPublic, accessExisting)
	}
}

// resolveTarget decides the chat channel a post goes to: the one the caller
// named, which has to be among the channels the connection's bot is in, or
// the channel's own. It returns the chosen target, empty when the post goes
// to the channel's own or the kind has none.
func (h *Handle) resolveTarget(ctx context.Context, ch notification.Channel, want string) (notification.ChatTarget, error) {
	want = strings.TrimSpace(want)
	if decided, err := ownTarget(ch, want); decided {
		return notification.ChatTarget{}, err
	}
	if h.cfg.Targets == nil {
		return notification.ChatTarget{}, errors.New("this deployment cannot list a bot's chat channels, so a target cannot be chosen")
	}
	listCtx, cancel := context.WithTimeout(ctx, targetListTimeout)
	defer cancel()
	targets, err := h.cfg.Targets.Targets(listCtx, ch)
	if err != nil {
		return notification.ChatTarget{}, fmt.Errorf("reading the chat channels of %q failed: %s", ch.Name, err.Error())
	}
	for _, t := range targets {
		if t.ID == want {
			return t, nil
		}
	}
	return notification.ChatTarget{}, fmt.Errorf("the bot of channel %q is not in a chat channel %q; call action=list for the ones it is in", ch.Name, want)
}

// ownTarget settles a post whose chat channel is not chosen among the bot's:
// a kind with none, a daily channel posting its bulletin to its own, and a
// post naming no target. decided is false when want has to be looked up.
func ownTarget(ch notification.Channel, want string) (decided bool, err error) {
	switch {
	case !notification.ChannelHasTarget(ch.Kind):
		if want != "" {
			return true, fmt.Errorf("channel %q is a %s channel, which has no chat channels to choose among; remove the target", ch.Name, ch.Kind)
		}
		return true, nil
	case ch.Mode == notification.ChannelModeDaily:
		if want != "" {
			return true, fmt.Errorf("channel %q collects a daily bulletin for its own chat channel; a target is for an immediate channel", ch.Name)
		}
		return true, nil
	case want == "":
		if ch.Target == "" {
			return true, fmt.Errorf("channel %q has no chat channel of its own; pass target, one of the channels action=list reports under it", ch.Name)
		}
		return true, nil
	default:
		return false, nil
	}
}

// postedTo names where a post went, as the share it created records it.
func postedTo(ch notification.Channel, target notification.ChatTarget) string {
	if target.Name != "" {
		return "#" + target.Name + " (" + ch.Name + ")"
	}
	return ch.Name
}

// linkRequest is what a publish asked its link to be: who it opens for, a
// public link's lifetime, and where it is being posted.
type linkRequest struct {
	mode, expiresIn, where string
}

// linkFor decides the link a publish posts: the asset page's for
// access=existing, otherwise a link share the caller must be allowed to make.
func (h *Handle) linkFor(ctx context.Context, asset *portaldomain.Asset, c caller, req linkRequest) (postLink, error) {
	mode, expiresIn, where := req.mode, req.expiresIn, req.where
	if mode == accessExisting {
		if strings.TrimSpace(expiresIn) != "" {
			return postLink{}, errors.New("expires_in applies to access=public only")
		}
		return postLink{url: h.assetLink(asset.ID)}, nil
	}
	if err := h.maySharePost(asset, c); err != nil {
		return postLink{}, err
	}
	if mode == accessSignedIn {
		if strings.TrimSpace(expiresIn) != "" {
			return postLink{}, portaldomain.ErrExpiryOnAuthenticatedShare
		}
		if share := h.reusableShare(ctx, asset.ID, where); share != nil {
			return postLink{url: h.viewLink(share.Token), share: share}, nil
		}
	}
	spec := portaldomain.ShareSpec{AccessMode: string(shareaccess.ModeAuthenticated)}
	if mode == accessPublic {
		spec = portaldomain.ShareSpec{AccessMode: string(shareaccess.ModePublic), ExpiresIn: strings.TrimSpace(expiresIn)}
	}
	share, err := portaldomain.BuildShare(portaldomain.ShareTarget{AssetID: asset.ID}, c.email, spec)
	if err != nil {
		return postLink{}, err //nolint:wrapcheck // the message is the caller's to act on
	}
	share.Origin, share.PostedTo = portaldomain.OriginChannelPost, where
	if err := h.cfg.Shares.Insert(ctx, &share); err != nil {
		return postLink{}, errors.New("creating the link share for the post failed")
	}
	return postLink{url: h.viewLink(share.Token), share: &share, created: true}, nil
}

// maySharePost refuses a share-creating publish the caller may not make: one
// the deployment cannot write a share for, one by someone who does not own the
// asset, and one by a persona that withholds sharing.
func (h *Handle) maySharePost(asset *portaldomain.Asset, c caller) error {
	if h.cfg.Shares == nil {
		return errors.New("this deployment cannot create a share for a post; pass access=existing to post the asset page's link")
	}
	if !c.unrestricted && !access.OwnsAsset(asset, &access.User{UserID: c.userID, Email: c.ownerEmail()}) {
		return errors.New("only the asset's owner can post a link that opens for the channel; " +
			"pass access=existing to post the asset page's link, which opens for the people who already have access")
	}
	if !c.unrestricted && h.cfg.SharingAllowed != nil && !h.cfg.SharingAllowed(c.persona) {
		return errors.New("sharing is not available to your persona; pass access=existing to post the asset page's link")
	}
	return nil
}

// reusableShare is the active signed-in link an earlier post of the asset to
// the same place created, so posting again does not mint a share each time.
func (h *Handle) reusableShare(ctx context.Context, assetID, where string) *portaldomain.Share {
	shares, err := h.cfg.Shares.ListByAsset(ctx, assetID)
	if err != nil {
		return nil
	}
	for i := range shares {
		s := shares[i]
		if s.Origin == portaldomain.OriginChannelPost && s.PostedTo == where &&
			s.AccessMode == shareaccess.ModeAuthenticated && access.IsShareActive(s) {
			return &s
		}
	}
	return nil
}

// viewLink is the URL a share opens at, or nothing when the deployment does
// not know its own public address.
func (h *Handle) viewLink(token string) string {
	if h.cfg.PortalURL == "" {
		return ""
	}
	return strings.TrimRight(h.cfg.PortalURL, "/") + "/portal/view/" + token
}

// withdraw revokes a share this publish created when nothing was posted, so a
// refused or held-back post leaves no link that was never handed to anyone.
func (h *Handle) withdraw(ctx context.Context, link postLink) {
	if link.created && link.share != nil {
		_ = h.cfg.Shares.Revoke(ctx, link.share.ID)
	}
}

// summaries renders the list action's entries, reading the chat channels of
// every slack and mattermost channel's bot at once: each listing is an
// upstream call, and one slow chat service holds back only its own entry.
func (h *Handle) summaries(ctx context.Context, channels []notification.Channel) []channelSummary {
	out := make([]channelSummary, len(channels))
	var wg sync.WaitGroup
	for i, ch := range channels {
		out[i] = channelSummary{
			Name: ch.Name, Kind: ch.Kind, Mode: ch.Mode, Description: ch.Description,
			Carries: carries(ch.Kind), Target: ch.Target, Targets: []notification.ChatTarget{},
		}
		// A daily channel posts its bulletin to its own chat channel, so it
		// has none to choose among.
		if !notification.ChannelHasTarget(ch.Kind) || ch.Mode == notification.ChannelModeDaily || h.cfg.Targets == nil {
			continue
		}
		wg.Go(func() {
			listCtx, cancel := context.WithTimeout(ctx, targetListTimeout)
			defer cancel()
			targets, err := h.cfg.Targets.Targets(listCtx, ch)
			if err != nil {
				out[i].TargetsError = err.Error()
				return
			}
			out[i].Targets = append(out[i].Targets, targets...)
		})
	}
	wg.Wait()
	return out
}
