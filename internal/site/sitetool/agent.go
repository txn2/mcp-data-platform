package sitetool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/site/siteenv"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// The error code and category an agent's move that waits on the person
// answers with: the codes a held tool call answers with (#2437), so a client
// reads both alike.
const (
	codeApprovalRequired = "approval_required"
	codeApprovalFiled    = "approval_filed"
	categoryApproval     = "approval_required"
)

// multiRoundTripVersion is the first MCP revision on which a server asks the
// person through an input request a client answers by calling again.
const multiRoundTripVersion = "2026-07-28"

// answerOf is the person's answer to the question about a move, from a call
// that carries it, and the key of the move it answers.
func answerOf(req *mcp.CallToolRequest) (string, *mcp.ElicitResult) {
	if req == nil || req.Params == nil {
		return "", nil
	}
	for key, v := range req.Params.InputResponses {
		if r, ok := v.(*mcp.ElicitResult); ok && strings.HasPrefix(key, "site_move:") {
			return key, r
		}
	}
	return "", nil
}

// personDecides answers a move that waits on the person (#2643): the question
// their client asks them (an input request on a 2026-07-28 client, asked in
// band on an older one that can be asked), and otherwise a request filed in
// the portal's Approvals with APPROVAL_REQUIRED. run carries the move out once
// the person accepted it.
func (t *Tool) personDecides(ctx context.Context, req *mcp.CallToolRequest, actor sitesvc.Actor, wait *siteenv.PersonDecisionError,
	run func(sitesvc.Actor) (any, error),
) *mcp.CallToolResult {
	m := wait.Move
	if caps := clientCaps(req); caps != nil && caps.Elicitation != nil {
		if req.ProtocolVersion() >= multiRoundTripVersion {
			return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{m.Key(): prompt(m)}}
		}
		if req.Session != nil {
			if r, err := req.Session.Elicit(ctx, prompt(m)); err == nil && r != nil {
				return answered(actor, m.Key(), r, run)
			}
		}
	}
	filed, err := t.ops().RequestMove(ctx, actor, m)
	if err != nil {
		res, _, _ := errorResult(message(err))
		return res
	}
	msg := fmt.Sprintf("APPROVAL_REQUIRED: not done. %s Approval request %s is filed. A person decides it in the portal (Approvals); this session cannot. "+
		"Give the person the request and the address to look at, and call again once it is approved.", m.Question(), filed.ID)
	return middleware.BuildErrorResult(middleware.NewToolError(codeApprovalFiled, categoryApproval, msg,
		"Stop here and tell the person: the move waits on their decision."))
}

// answered carries a move out on the person's acceptance, and nothing on any
// other answer.
func answered(actor sitesvc.Actor, key string, r *mcp.ElicitResult, run func(sitesvc.Actor) (any, error)) *mcp.CallToolResult {
	switch r.Action {
	case "accept":
	case "decline":
		return middleware.BuildErrorResult(middleware.NewToolError(codeApprovalRequired, middleware.ErrCategoryDeclined,
			"Not done: the person declined. Nothing changed.", ""))
	default:
		return middleware.BuildErrorResult(middleware.NewToolError(codeApprovalRequired, audit.CategoryPromptUnanswered,
			"Not done: the question was not answered. Nothing changed.", ""))
	}
	actor.Decided = key
	v, err := run(actor)
	if _, again := errors.AsType[*siteenv.PersonDecisionError](err); again {
		// The site moved between the question and the answer: what was
		// accepted is no longer what would happen.
		res, _, _ := errorResult("Not done: the site changed after the person was asked, so their answer no longer fits. Call again to ask them about the move as it stands now.")
		return res
	}
	if err != nil {
		res, _, _ := errorResult(message(err))
		return res
	}
	res, _, _ := jsonResult(v)
	return res
}

func clientCaps(req *mcp.CallToolRequest) *mcp.ClientCapabilities {
	if req == nil {
		return nil
	}
	return req.ClientCapabilities()
}

// prompt is the question the person's client shows. It asks for nothing but
// the decision.
func prompt(m siteenv.AgentMove) *mcp.ElicitParams {
	return &mcp.ElicitParams{
		Message:         m.Question(),
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
}
