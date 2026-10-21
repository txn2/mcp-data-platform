// Package channelhttp serves the portal's channel routes: the channels the
// signed-in person can reach with their bots' chat channels, sharing an asset
// to one from the Share dialog (#2241), and sending a message to one (#1723).
//
// Every route is a notify tool call, made over an in-memory session that
// authenticates as the person the portal signed in. That is the whole point of
// the shape: who may reach a channel, what an asset becomes when it is posted,
// the per-session rate limit and the audit row are the tool's, so the asset
// page and an agent asking for the same post cannot come to disagree about
// any of them.
package channelhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/inprocess"
	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// toolNotify is the tool every route calls. It is named rather than imported
// because this package reaches it the way any client does: by name.
const toolNotify = "notify"

// sessionLabel names the in-memory client in the server's logs.
const sessionLabel = "portal-channels"

// maxBodyBytes bounds a request body. A message is posted to a chat channel;
// the tool's own document cap is larger and applies after this one.
const maxBodyBytes = 64 << 10

// API serves the routes.
type API struct {
	// Server is the assembled MCP server the calls are made against. nil
	// answers every route 503.
	Server *mcp.Server
	// SharingAllowed reports whether the signed-in person's persona lets
	// them share (#2241). The channel list and the channel share refuse
	// without it. nil allows everyone.
	SharingAllowed func(*access.User) bool
}

// Register mounts the routes on the portal's authenticated mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/portal/channels", a.list)
	mux.HandleFunc("POST /api/v1/portal/assets/{id}/channel-shares", a.share)
	mux.HandleFunc("POST /api/v1/portal/channels/{name}/send", a.send)
}

// The two link modes a channel share takes, in the share vocabulary the
// Share dialog speaks.
const (
	modeAuthenticated = "authenticated"
	modePublic        = "public"
)

// ChannelShareRequest shares an asset to a channel (#2241).
type ChannelShareRequest struct {
	// Channel is the channel's name, as the list reports it.
	Channel string `json:"channel" example:"slack"`
	// Target is the chat channel of a slack or mattermost channel, one of
	// the ids the list reports under the channel's targets. Empty posts to
	// the channel's own.
	Target string `json:"target,omitempty" example:"C0123456789"`
	// AccessMode is who opens the posted link: authenticated (any signed-in
	// user, no expiration) or public (anyone with the link, expires_in
	// required).
	AccessMode string `json:"access_mode" example:"authenticated" enums:"authenticated,public"`
	// ExpiresIn is the public link's lifetime, a duration such as 168h.
	ExpiresIn string `json:"expires_in,omitempty" example:"168h"`
	// Message is an optional line posted above the asset.
	Message string `json:"message,omitempty" example:"This week's numbers"`
}

// SendRequest posts a message.
type SendRequest struct {
	Title string `json:"title" example:"Nightly load finished late"`
	// Body is markdown.
	Body string `json:"body,omitempty" example:"The load finished at 06:40, forty minutes past its window."`
	// Link is the absolute URL a reader follows from the message.
	Link string `json:"link,omitempty" example:"https://platform.example.com/portal/automations/scr_1/runs/run_9"`
}

// list handles GET /api/v1/portal/channels.
//
// @Summary      List the channels I can share to
// @Description  Returns the enabled notification channels the signed-in user reaches: every email channel, and each chat or webhook channel whose api connection the user's persona reaches. A Slack or Mattermost channel lists the chat channels its bot is in under targets, read from the upstream on each call. Answered by the notify tool's list action. Refused for a persona that does not allow sharing.
// @Tags         Notifications
// @Produce      json
// @Success      200  {object}  map[string]any
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/channels [get]
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	if !a.mayShare(w, r) {
		return
	}
	a.call(w, r, map[string]any{"action": "list"})
}

// share handles POST /api/v1/portal/assets/{id}/channel-shares.
//
// @Summary      Share an asset to a channel
// @Description  Creates a link share on an asset the signed-in user owns, in the access mode asked for (authenticated, or public with expires_in), and posts the asset to a channel they reach with the share's URL as its link: the asset's name, its content (a CSV as a table of its first rows) and its preview image when it has one. Posting the same asset to the same place again with authenticated reuses the share it made. The share appears in the asset's share list with posted_to naming the channel; revoking it stops the posted link from opening. Delivery is queued. Refused for a persona that does not allow sharing.
// @Tags         Shares
// @Accept       json
// @Produce      json
// @Param        id       path  string               true  "Asset ID"
// @Param        request  body  ChannelShareRequest  true  "The channel, who opens the link, and an optional message"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      429  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/channel-shares [post]
func (a *API) share(w http.ResponseWriter, r *http.Request) {
	if !a.mayShare(w, r) {
		return
	}
	var req ChannelShareRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Channel) == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "channel is required")
		return
	}
	accessArg, ok := map[string]string{modeAuthenticated: "signed_in", modePublic: "public"}[strings.TrimSpace(req.AccessMode)]
	if !ok {
		httpjson.WriteError(w, http.StatusBadRequest, "access_mode must be authenticated or public")
		return
	}
	a.call(w, r, map[string]any{
		"action": "publish", "channel": req.Channel, "target": req.Target,
		"asset": r.PathValue("id"), "message": req.Message,
		"access": accessArg, "expires_in": req.ExpiresIn,
	})
}

// mayShare refuses a caller whose persona withholds sharing, answering the
// request itself when it does.
func (a *API) mayShare(w http.ResponseWriter, r *http.Request) bool {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "sign in to share to a channel")
		return false
	}
	if a.SharingAllowed != nil && !a.SharingAllowed(user) {
		httpjson.WriteError(w, http.StatusForbidden, "sharing is not available to your persona")
		return false
	}
	return true
}

// send handles POST /api/v1/portal/channels/{name}/send.
//
// @Summary      Send a message to a channel
// @Description  Posts a title, a markdown body and an optional link to a channel the signed-in user reaches. Delivery is queued.
// @Tags         Notifications
// @Accept       json
// @Produce      json
// @Param        name     path  string       true  "Channel name"
// @Param        request  body  SendRequest  true  "The message"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      429  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/channels/{name}/send [post]
func (a *API) send(w http.ResponseWriter, r *http.Request) {
	var req SendRequest
	if !decode(w, r, &req) {
		return
	}
	a.call(w, r, map[string]any{
		"action": "send", "channel": r.PathValue("name"),
		"title": req.Title, "body": req.Body, "link": req.Link,
	})
}

// decode reads a bounded JSON body, answering 400 itself when it cannot.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the request body is not valid: "+err.Error())
		return false
	}
	return true
}

// call issues one notify call as the signed-in user and writes what the tool
// answered: its result, or its refusal in the tool's own words.
func (a *API) call(w http.ResponseWriter, r *http.Request, args map[string]any) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "sign in to post to a channel")
		return
	}
	if a.Server == nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "posting to a channel is unavailable in this deployment")
		return
	}
	session, cleanup, err := a.connect(asUser(r.Context(), user))
	if err != nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "posting to a channel is unavailable right now")
		return
	}
	defer cleanup()
	res, err := session.CallTool(r.Context(), &mcp.CallToolParams{Name: toolNotify, Arguments: args})
	if err != nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "posting to a channel is unavailable right now")
		return
	}
	text := textOf(res)
	if res.IsError {
		if text == "" {
			text = "the post was refused"
		}
		httpjson.WriteError(w, refusalStatus(res), text)
		return
	}
	var out map[string]any
	if structured, ok := res.StructuredContent.(map[string]any); ok {
		out = structured
	} else if err := json.Unmarshal([]byte(text), &out); err != nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "the notify tool answered in a shape this route cannot read")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// connect opens the in-memory session the call travels over.
func (a *API) connect(ctx context.Context) (*mcp.ClientSession, func(), error) {
	st, ct := mcp.NewInMemoryTransports()
	server, err := a.Server.Connect(ctx, st, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("server connect: %w", err)
	}
	// The request this serves is drained on its own (#2058).
	untrack := inprocess.Track(server)
	client, err := mcp.NewClient(&mcp.Implementation{Name: sessionLabel, Version: "v1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		_ = server.Close()
		untrack()
		return nil, nil, fmt.Errorf("client connect: %w", err)
	}
	return client, func() {
		_ = client.Close()
		_ = server.Close()
		untrack()
	}, nil
}

// refusalStatus is the HTTP status a tool refusal answers with: 429 for the
// per-session rate limit, 400 for everything else the tool refused.
func refusalStatus(res *mcp.CallToolResult) int {
	if sc, ok := res.StructuredContent.(map[string]any); ok {
		if env, ok := sc["error"].(map[string]any); ok && env["code"] == toolratelimit.CodeRateLimited {
			return http.StatusTooManyRequests
		}
	}
	return http.StatusBadRequest
}

// textOf is a result's first text block.
func textOf(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

// asUser is the context the session authenticates as: the person the portal
// signed in, presenting the authentication their request arrived with, and
// labeled as a portal-driven call for audit.
func asUser(ctx context.Context, u *access.User) context.Context {
	ctx = middleware.WithSource(ctx, middleware.SourceAdmin)
	return middleware.WithPreAuthenticatedUser(ctx, &middleware.UserInfo{
		UserID: u.UserID, Email: u.Email, Roles: u.Roles, Claims: u.Claims, AuthType: u.AuthType,
	})
}

// SharingFor answers whether a portal user's persona lets them share
// (#2241): the persona their roles resolve to allows it, or is the admin
// persona. A user whose roles resolve to no persona shares nothing. nil with
// no registry, which allows everyone.
func SharingFor(reg *persona.Registry, adminPersona string) func(*access.User) bool {
	if reg == nil {
		return nil
	}
	return func(u *access.User) bool {
		p, ok := reg.GetForRoles(u.Roles)
		if !ok {
			return false
		}
		return reg.AdminRule(adminPersona)(u.Roles) || p.SharingAllowed()
	}
}
