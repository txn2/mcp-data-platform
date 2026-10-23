package resourcesharewire

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/httpserver/channelhttp"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// channelPath posts a file to a notification channel (#2258).
const channelPath = "POST /api/v1/resources/{id}/channel-shares"

// Who can open the link a file's post carries.
const (
	// accessAuthenticated shares the file with every signed-in person of an
	// internal persona, as viewer.
	accessAuthenticated = "authenticated"
	// accessExisting posts the file's page, which opens for the people who
	// can already open the file.
	accessExisting = "existing"
)

const maxChannelBody = 64 << 10

// poster sends one notify call as a person; *channelhttp.API satisfies it.
type poster interface {
	CallAs(w http.ResponseWriter, r *http.Request, user *access.User, args map[string]any) bool
}

// postToChannel handles POST /api/v1/resources/{id}/channel-shares.
//
// @Summary      Post a file to a channel
// @Description  Posts a link to a managed resource to one of the deployment's notification channels, with an optional line above it (#2258). access_mode authenticated (the default) shares the file with every signed-in person of an internal persona, as viewer, recorded as posted to the channel and withdrawn when the post does not go out; existing posts the file's page, which opens for the people who can already open it. A file has no public link. Someone who can change the file and whose persona allows sharing.
// @Tags         Resources
// @Accept       json
// @Produce      json
// @Param        id    path  string                           true  "Resource ID"
// @Param        body  body  channelhttp.ChannelShareRequest  true  "Where to post and who can open the link"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/channel-shares [post]
func (s *Shares) postToChannel(w http.ResponseWriter, r *http.Request) {
	claims, err := s.deps.Claims(r)
	if err != nil || claims == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "sign in to post to a channel")
		return
	}
	res, err := s.deps.Resources.Get(r.Context(), r.PathValue("id"))
	if err != nil || res == nil || !resource.CanAccessResource(*claims, res) {
		httpjson.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	in, ok := decodeChannelShare(w, r)
	if !ok {
		httpjson.WriteError(w, http.StatusBadRequest, "name the channel to post to")
		return
	}
	share, status, msg := s.linkFor(r, claims, res, in)
	if status != 0 {
		httpjson.WriteError(w, status, msg)
		return
	}
	user := &access.User{UserID: claims.Sub, Email: claims.Email, Roles: claims.Roles}
	sent := s.channels.CallAs(w, r, user, map[string]any{
		"action": "send", "channel": in.Channel, "target": in.Target,
		"title": res.DisplayName, "body": strings.TrimSpace(in.Message), "link": s.fileLink(res.ID),
	})
	if !sent && share != nil {
		s.svc.Withdraw(r.Context(), share)
	}
}

// decodeChannelShare reads a post's body, which must name a channel.
func decodeChannelShare(w http.ResponseWriter, r *http.Request) (channelhttp.ChannelShareRequest, bool) {
	var in channelhttp.ChannelShareRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChannelBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, false
	}
	return in, strings.TrimSpace(in.Channel) != ""
}

// linkFor makes the share a post opens the file by: a link share for any
// signed-in person of an internal persona (the default), or none when the
// post is for the people who can already open it. It answers a status and
// message when neither applies or the caller may not share the file.
func (s *Shares) linkFor(r *http.Request, claims *resource.Claims, res *resource.Resource,
	in channelhttp.ChannelShareRequest,
) (share *portaldomain.Share, status int, msg string) {
	switch mode := strings.TrimSpace(in.AccessMode); mode {
	case "", accessAuthenticated:
		return s.svc.LinkShare(r.Context(), claims, res, postedTo(in))
	case accessExisting:
		return nil, 0, ""
	default:
		return nil, http.StatusBadRequest, "access_mode must be authenticated or existing: a file has no public link"
	}
}

// fileLink is the file's page in the portal.
func (s *Shares) fileLink(id string) string {
	return strings.TrimRight(s.portalURL, "/") + "/portal/resources/" + id
}

// postedTo is where a post went, as the file's shares list it.
func postedTo(in channelhttp.ChannelShareRequest) string {
	if t := strings.TrimSpace(in.Target); t != "" {
		return t + " on " + strings.TrimSpace(in.Channel)
	}
	return strings.TrimSpace(in.Channel)
}
