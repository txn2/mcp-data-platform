// Package channelhttp serves the portal's "Post to a channel" routes (#1723):
// the channels the signed-in person can reach, posting an asset to one, and
// sending a message to one.
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
}

// Register mounts the routes on the portal's authenticated mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/portal/channels", a.list)
	mux.HandleFunc("POST /api/v1/portal/channels/{name}/publish", a.publish)
	mux.HandleFunc("POST /api/v1/portal/channels/{name}/send", a.send)
}

// PublishRequest posts an asset.
type PublishRequest struct {
	// AssetID is the asset to post.
	AssetID string `json:"asset_id" example:"ast_01HK7R8Z"`
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
// @Summary      List the channels I can post to
// @Description  Returns the enabled notification channels the signed-in user reaches: every email channel, and each chat or webhook channel whose api connection the user's persona reaches. Answered by the notify tool's list action.
// @Tags         Notifications
// @Produce      json
// @Success      200  {object}  map[string]any
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/channels [get]
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	a.call(w, r, map[string]any{"action": "list"})
}

// publish handles POST /api/v1/portal/channels/{name}/publish.
//
// @Summary      Post an asset to a channel
// @Description  Posts a portal asset the signed-in user can read to a channel they reach: the asset's name, its content (a CSV as a table of its first rows), its preview image when it has one, and a link to it. Delivery is queued.
// @Tags         Notifications
// @Accept       json
// @Produce      json
// @Param        name     path  string          true  "Channel name"
// @Param        request  body  PublishRequest  true  "The asset and an optional message"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      429  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/channels/{name}/publish [post]
func (a *API) publish(w http.ResponseWriter, r *http.Request) {
	var req PublishRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.AssetID) == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "asset_id is required")
		return
	}
	a.call(w, r, map[string]any{
		"action": "publish", "channel": r.PathValue("name"),
		"asset": req.AssetID, "message": req.Message,
	})
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
