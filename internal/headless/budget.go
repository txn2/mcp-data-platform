package headless

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sync/semaphore"
)

// MaxFileBytes bounds one file a page is answered with, of its own origin or
// a public one (#2721). A larger file is not handed to the page whole: an
// in-process read stops at it, and an open-ended range is cut to it, so a
// video's first frames still arrive.
const MaxFileBytes = 16 << 20

// answerBudget bounds the file bytes every render in the process is answering
// at once (#2721). A file answered to the browser is held three times over
// while it is sent (the bytes, their base64 and the protocol message), and a
// page asks for all of its files together; a site of large media drawn for
// its tile took a gigabyte of heap per page before this bound, and a replica
// redrawing every site tile after an upgrade was killed for its memory.
const answerBudget = 4 * MaxFileBytes

// budget is the share of answerBudget one answer holds: MaxFileBytes while
// its file is read, then only what it read until the answer is sent.
type budget struct {
	sem  *semaphore.Weighted
	held int64
}

// take waits for room to read one file, or ctx to end.
func (r *Renderer) take(ctx context.Context) (*budget, error) {
	r.answersOnce.Do(func() { r.answers = semaphore.NewWeighted(answerBudget) })
	if err := r.answers.Acquire(ctx, MaxFileBytes); err != nil {
		return nil, fmt.Errorf("headless: waiting to answer a file: %w", err)
	}
	return &budget{sem: r.answers, held: MaxFileBytes}, nil
}

// fit gives back what the answer read short of MaxFileBytes. A nil budget is
// an answer that holds none.
func (b *budget) fit(n int) {
	if b == nil || int64(n) >= b.held {
		return
	}
	b.sem.Release(b.held - int64(n))
	b.held = int64(n)
}

// release gives back the rest once the answer is sent.
func (b *budget) release() {
	if b == nil || b.held == 0 {
		return
	}
	b.sem.Release(b.held)
	b.held = 0
}

// openRange matches an open-ended single range, the form a media element
// asks with.
var openRange = regexp.MustCompile(`^\s*bytes\s*=\s*(\d{1,18})\s*-\s*$`)

// clampRange cuts an open-ended range in a paused request's headers to
// MaxFileBytes from its start, so the answer is a part a media element plays
// from rather than the rest of a file larger than the bound. Every other
// range, and a request with none, is left as it is.
func clampRange(headers map[string]string) {
	for k, v := range headers {
		if !strings.EqualFold(k, "Range") {
			continue
		}
		m := openRange.FindStringSubmatch(v)
		if m == nil {
			return
		}
		start, err := strconv.ParseInt(m[1], decimal, int64Bits)
		if err != nil {
			return
		}
		headers[k] = "bytes=" + m[1] + "-" + strconv.FormatInt(start+MaxFileBytes-1, decimal)
		return
	}
}
