package toolhelp

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// repeatCapacity bounds how many (session, tool, refusal) keys are
// remembered; the oldest is forgotten first.
const repeatCapacity = 10000

// repeatSentence is what a refusal met the second time says (#2820).
const repeatSentence = " This call was refused the same way before in this session. If the platform cannot do what " +
	"you need, report it: manage_feedback action=create target_type=standalone topic=platform with trying_to and blocked_by."

// sessionOf is the session a call belongs to, "" when none.
type sessionOf func(ctx context.Context) string

// RepeatMiddleware tells an agent, the second time one session's call to a
// tool is refused with the same code, how to report what the platform could
// not do (#2820): at that point the refusal is no longer a mistake to correct.
// A refusal of a session-less call, and a refusal of manage_feedback itself,
// are left as they are.
func RepeatMiddleware(session func(ctx context.Context) string) mcp.Middleware {
	seen := &repeatSet{keys: map[string]struct{}{}}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != methodToolsCall || session == nil {
				return result, err
			}
			return seen.annotate(sessionOf(session)(ctx), req, result), nil
		}
	}
}

// repeatSet is the refusals seen, oldest first.
type repeatSet struct {
	mu    sync.Mutex
	keys  map[string]struct{}
	order []string
}

// annotate adds the sentence to a refusal the session already met.
func (s *repeatSet) annotate(session string, req mcp.Request, result mcp.Result) mcp.Result {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil || !ctr.IsError || session == "" {
		return result
	}
	var pe *middleware.PlatformError
	if !errors.As(ctr.GetError(), &pe) || pe.Code == "" {
		return result
	}
	tool, _ := callNameAndArgs(req)
	if tool == "" || tool == "manage_feedback" {
		return result
	}
	if !s.seenBefore(session + "\x00" + tool + "\x00" + pe.Code) {
		return result
	}
	rebuilt := *pe
	rebuilt.Message = strings.TrimSpace(pe.Message) + repeatSentence
	return middleware.BuildErrorResult(&rebuilt)
}

// seenBefore records a key and reports whether it was already recorded.
func (s *repeatSet) seenBefore(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[key]; ok {
		return true
	}
	s.keys[key] = struct{}{}
	s.order = append(s.order, key)
	if len(s.order) > repeatCapacity {
		delete(s.keys, s.order[0])
		s.order = s.order[1:]
	}
	return false
}
