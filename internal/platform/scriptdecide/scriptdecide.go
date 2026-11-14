// Package scriptdecide is the part of a managed script's host a tool-call
// handler decides and counts with (#2436): what decide(call) returns, the
// count over a rolling window a rate limit reads, and the messages a
// handler's read-only run queues for the platform to send after the call.
package scriptdecide

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.starlark.net/starlark"
)

// The bindings a policy decides with (#2436): what decide(call) returns, and
// the count over a rolling window a rate limit reads.
const (
	CapabilityAllow           = "platform.allow"
	CapabilityDeny            = "platform.deny"
	CapabilityRequireApproval = "platform.require_approval"
	CapabilityCount           = "platform.count"
)

// The decisions a policy returns, as the "decision" key of the dict the three
// constructors build.
const (
	DecisionAllow   = "allow"
	DecisionDeny    = "deny"
	DecisionApprove = "require_approval"
)

// maxCountWindow is the longest window platform.count reaches back over.
const maxCountWindow = 31 * 24 * time.Hour

// Counter adds amount under key and returns the total over the window, this
// call included (callhandler.Store.Count, scoped to one handler and call).
type Counter func(ctx context.Context, key string, window time.Duration, amount int) (int, error)

// Allow is platform.allow(): the call may run.
func Allow(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, wrapIn(b, err)
	}
	return decision(DecisionAllow, nil), nil
}

// Deny is platform.deny(reason): the call is refused, the caller told why.
func Deny(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var reason string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "reason", &reason); err != nil {
		return nil, wrapIn(b, err)
	}
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("in %s: reason is empty; say why the call is refused, as the caller will read it", b.Name())
	}
	return decision(DecisionDeny, map[string]starlark.Value{"reason": starlark.String(reason)}), nil
}

// RequireApproval is platform.require_approval(approvers, summary): the call
// waits for one of approvers (addresses or personas; "administrators", or
// none, is an administrator) to approve it.
func RequireApproval(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		approvers *starlark.List
		summary   string
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "approvers?", &approvers, "summary?", &summary); err != nil {
		return nil, wrapIn(b, err)
	}
	list := starlark.NewList(nil)
	if approvers != nil {
		for i := range approvers.Len() {
			s, ok := starlark.AsString(approvers.Index(i))
			if !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("in %s: approvers are addresses or persona names, as strings", b.Name())
			}
			_ = list.Append(starlark.String(strings.TrimSpace(s)))
		}
	}
	return decision(DecisionApprove, map[string]starlark.Value{"approvers": list, "summary": starlark.String(summary)}), nil
}

// decision is the frozen dict a policy's decide(call) returns.
func decision(kind string, fields map[string]starlark.Value) *starlark.Dict {
	d := starlark.NewDict(len(fields) + 1)
	_ = d.SetKey(starlark.String("decision"), starlark.String(kind))
	// In one order, so a draft and its test's replay print a decision alike.
	for _, k := range []string{"reason", "approvers", "summary"} {
		if v, ok := fields[k]; ok {
			_ = d.SetKey(starlark.String(k), v)
		}
	}
	d.Freeze()
	return d
}

// Count is platform.count(key, window, add=1): add this call under key and
// return the total over the window ("90s", "1h", "7d"), this call included.
// Counts are the handler's own and are released when its call does not run.
// With no counter (a draft, a test) it counts only this call.
func Count(ctx context.Context, counter Counter, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		key, window string
		add         = 1
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "key", &key, "window", &window, "add?", &add); err != nil {
		return nil, wrapIn(b, err)
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("in %s: key is empty; name what is counted, as call[\"user\"] + call[\"operation\"]", b.Name())
	}
	if add < 0 {
		return nil, fmt.Errorf("in %s: add is %d; a count only grows", b.Name(), add)
	}
	d, err := ParseWindow(window)
	if err != nil {
		return nil, wrapIn(b, err)
	}
	if counter == nil {
		return starlark.MakeInt(add), nil
	}
	n, err := counter(ctx, key, d, add)
	if err != nil {
		return nil, fmt.Errorf("in %s: the count could not be read", b.Name())
	}
	return starlark.MakeInt(n), nil
}

// MaxNotices is the most messages one tool-call handler's run may queue.
const MaxNotices = 5

// Queue keeps a tool-call handler's message on notices for the platform to
// send once the call has been answered.
func Queue(b *starlark.Builtin, notices *[]map[string]any, payload map[string]any) (starlark.Value, error) {
	if len(*notices) >= MaxNotices {
		return nil, fmt.Errorf("in %s: a handler queues at most %d messages for one call", b.Name(), MaxNotices)
	}
	*notices = append(*notices, payload)
	queued := starlark.NewDict(1)
	_ = queued.SetKey(starlark.String("queued"), starlark.True)
	queued.Freeze()
	return queued, nil
}

// ParseWindow reads a count's window: a Go duration ("90s", "1h30m") or a
// number of days ("7d"), at most 31 days.
func ParseWindow(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("window %q is not a number of days", s)
		}
		d = time.Duration(n) * hoursPerDay * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil || d <= 0 {
			return 0, fmt.Errorf("window %q is not a duration like \"1h\" or \"7d\"", s)
		}
	}
	if d > maxCountWindow {
		return 0, fmt.Errorf("window %q is longer than 31 days", s)
	}
	return d, nil
}

// hoursPerDay converts a window in days.
const hoursPerDay = 24

// wrapIn names the builtin an error came from.
func wrapIn(b *starlark.Builtin, err error) error {
	return fmt.Errorf("in %s: %w", b.Name(), err)
}
