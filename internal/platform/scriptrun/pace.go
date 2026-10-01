package scriptrun

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/internal/upstreamretry"
)

// minPace is the shortest wait the host makes on a rate-limit refusal that
// names no interval. The platform's limiter always names at least one second,
// so this floor is reached only by a refusal from some other producer of the
// code; it exists so a refusal can never turn into an immediate re-issue.
const minPace = time.Second

// callTool is the one funnel every host binding's tool call goes through. It
// issues the call and, when the call was refused for timing alone, waits the
// refusal's interval and issues it again; the script sees the result of the
// admitted call and nothing else. An upstream the call reached that answered
// it should be asked again later (a 429, or a 503 to a read; see
// internal/upstreamretry) is waited on the same way, at most
// upstreamretry.MaxRetries times and never past the deadline, after
// which the script has the upstream's answer as data (#1859). A call whose
// upstream did not answer at all fails as an upstream error, which records the
// run as retryable rather than as the script's.
//
// A rate-limit refusal is not a script error. The limiter is a backstop
// against a runaway loop, sized so ordinary use never touches it, and its
// refusal says to wait a second and retry. The dialect has no way to catch an
// error and no clock to wait with, both by design, so a refusal surfaced to
// the script is a run failed by the one condition its author cannot handle.
// The host has a clock, and this is where it is used: the wait is a timer
// against the run's own context, so a run whose deadline arrives while it is
// pacing fails as ErrTimeout exactly as one whose query took too long. There
// is no attempt cap of its own: the bucket refills at the sustained rate, so
// one wait is almost always enough, and the deadline is the bound that already
// exists. Every other refusal is returned unchanged, so a script whose call
// fails for any other reason fails as it always has.
//
// The interpreter does not advance while the host waits, so a paced run
// consumes the steps an unlimited one would; only wall-clock time differs, and
// wall-clock time was never part of the determinism contract.
func (h *hostState) callTool(tool string, args map[string]any) (map[string]any, error) {
	if h.opts.Test != nil {
		// A test's answers are the ones a run was finally given, so there is
		// nothing to pace or retry (#1939).
		return h.opts.Caller.CallTool(h.callCtx(), tool, args) //nolint:wrapcheck // wrapped by the calling binding
	}
	out, err := h.pacedCall(tool, args)
	if h.opts.OnCall != nil {
		h.opts.OnCall(tool, args, out, err)
	}
	return out, err
}

// pacedCall issues one call, waiting and issuing it again as callTool says.
func (h *hostState) pacedCall(tool string, args map[string]any) (map[string]any, error) {
	for retry := 0; ; {
		out, err := h.opts.Caller.CallTool(h.callCtx(), tool, args)
		if err != nil {
			again, refusedErr := h.onRefusal(tool, err)
			if !again {
				return nil, refusedErr
			}
			continue
		}
		done, err := h.answered(tool, out, retry)
		if err != nil {
			return nil, err
		}
		if done {
			return out, nil
		}
		retry++
	}
}

// onRefusal decides what callTool does with a failed call: again when it was
// refused for timing alone and the wait it named has been made, and
// otherwise the error the binding is handed.
func (h *hostState) onRefusal(tool string, err error) (bool, error) {
	var refusal *scriptsession.RefusalError
	if errors.As(err, &refusal) && refusal.Code == upstreamretry.CodeUnavailable {
		// The upstream did not answer; the run it ends is not the script's.
		return false, scriptguard.NewUpstreamError(tool, err)
	}
	if refusal == nil || refusal.Code != toolratelimit.CodeRateLimited {
		// Returned as the Caller produced it: the binding that asked names
		// itself around the error, and the text is the tool's own.
		h.upstream.Clear()
		return false, err
	}
	wait := refusal.RetryAfter
	if wait <= 0 {
		wait = minPace
	}
	if !sleepWithin(h.ctx, wait) {
		return false, fmt.Errorf("waiting %s to retry %s after a rate-limit refusal: %w", wait, tool, h.ctx.Err())
	}
	// Written after the wait, so the line records what was actually spent:
	// a deadline that arrives mid-wait fails the run with the reason above
	// rather than logging a wait that did not complete.
	h.log.Print(fmt.Sprintf("rate limit: %s was refused; waited %s and retried", tool, wait))
	return true, nil
}

// answered decides what callTool does with an answer: done when it is the one
// the script is handed, and otherwise the wait the upstream asked for has
// been made and the call is issued again. The error is a wait the run's
// deadline cut short.
func (h *hostState) answered(tool string, out map[string]any, retry int) (bool, error) {
	h.mem.Called(tool)
	advice := upstreamretry.FromResult(out)
	wait, again := advice.Wait(retry, h.remaining())
	if !again {
		h.noteUpstreamGiveUp(tool, advice, retry)
		h.upstream.Note(tool, out)
		return true, nil
	}
	if !sleepWithin(h.ctx, wait) {
		return false, scriptguard.NewUpstreamError(tool, fmt.Errorf("waiting %s to retry %s after the upstream answered %s: %w",
			wait, tool, advice.Answer(), h.ctx.Err()))
	}
	h.log.Print(fmt.Sprintf("upstream answered %s to %s; waited %s and retried (%d of %d)",
		advice.Answer(), tool, wait, retry+1, upstreamretry.MaxRetries))
	return false, nil
}

// noteUpstreamGiveUp records, when the host retried an upstream and it still
// refused, that the script now has the refusal (#1859).
func (h *hostState) noteUpstreamGiveUp(tool string, advice upstreamretry.Seen, retries int) {
	if !advice.Retryable || retries == 0 {
		return
	}
	h.log.Print(fmt.Sprintf("%s: the upstream still answered %s after %d retries; the script has the answer",
		tool, advice.Answer(), retries))
}

// remaining is how long the run has before its deadline, or an hour when it
// has none, which only a caller outside a run can arrange.
func (h *hostState) remaining() time.Duration {
	if deadline, ok := h.ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return time.Hour
}

// sleepWithin waits d or until ctx ends, whichever is first, and reports
// whether the whole of d elapsed.
func sleepWithin(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
