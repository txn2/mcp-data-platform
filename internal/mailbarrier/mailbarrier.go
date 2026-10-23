// Package mailbarrier keeps mail from steering a managed script (#2247).
//
// A message is data. A script that reads mail and then writes something (a
// notification, an asset, an API POST, a trino_execute, an output) is a
// script whose action a sender can influence by what they write, so the
// combination is something its owner turns on knowingly rather than something
// that happens: the script's mail_actions setting. With it off, a run that has
// called any mail_* tool is refused every later call the platform classifies
// as a write (internal/toolwrite) and every output, and the refusal says what
// to turn on. With it on, the script's contract shows that it reads mail and
// acts on it.
//
// It also caps how many mail calls one run makes, so a loop over a large
// mailbox ends with an error rather than reading all of it.
//
// It wraps a run's Caller and Exporter, which is what keeps the rule out of
// the run engine and the same in every place a run is assembled: a platform
// run, a draft, an endpoint request.
package mailbarrier

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/toolwrite"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// MaxMailCallsPerRun bounds the mail_* calls one run makes.
const MaxMailCallsPerRun = 500

// mailPrefix names the mail kind's tools.
const mailPrefix = "mail_"

// Barrier is one run's barrier.
type Barrier struct {
	allowed    bool
	classifier toolwrite.Classifier

	mu       sync.Mutex
	mailRead string // the first mail tool the run called
	calls    int
}

// New returns the barrier for one run of a script whose mail_actions setting
// is allowed.
func New(allowed bool, classifier toolwrite.Classifier) *Barrier {
	return &Barrier{allowed: allowed, classifier: classifier}
}

// Guard wraps a run's caller and exporter in a new barrier. A nil caller or
// exporter stays nil: the run engine reads nil as "this run has none".
func Guard(allowed bool, classifier toolwrite.Classifier, c scriptrun.Caller, e scriptrun.Exporter) (scriptrun.Caller, scriptrun.Exporter) {
	b := New(allowed, classifier)
	if c == nil {
		return nil, e
	}
	if e != nil {
		e = b.Exporter(e)
	}
	return b.Caller(c), e
}

// RefusalError is a write refused because the run read mail.
type RefusalError struct {
	MailTool string
	Write    string
}

func (e *RefusalError) Error() string {
	return fmt.Sprintf("this run read mail (%s) and then %s, which writes; mail can steer what a script does, "+
		"so a script that acts on mail must have mail_actions turned on: manage_script update with "+
		"mail_actions=true, or \"Reads mail and acts on it\" on the script's page", e.MailTool, e.Write)
}

// noteMail records a mail call, refusing one past the run's cap.
func (b *Barrier) noteMail(tool string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.calls > MaxMailCallsPerRun {
		return fmt.Errorf("this run has made %d mail calls, the most one run makes; process the rest in the next run "+
			"with mail_search's since_uid", MaxMailCallsPerRun)
	}
	if b.mailRead == "" {
		b.mailRead = tool
	}
	return nil
}

// watching reports whether a write would be refused now: the script does not
// act on mail and the run has read some. Until then a call is not classified,
// so a run that never reads mail pays nothing for the barrier.
func (b *Barrier) watching() bool {
	if b.allowed {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mailRead != ""
}

// refuse returns the refusal of write when the run has read mail and the
// script does not act on mail, nil otherwise.
func (b *Barrier) refuse(write string) error {
	if b.allowed {
		return nil
	}
	b.mu.Lock()
	read := b.mailRead
	b.mu.Unlock()
	if read == "" {
		return nil
	}
	return &RefusalError{MailTool: read, Write: write}
}

// Caller wraps next.
func (b *Barrier) Caller(next scriptrun.Caller) scriptrun.Caller {
	return &caller{b: b, next: next}
}

type caller struct {
	b    *Barrier
	next scriptrun.Caller
}

// CallTool counts a mail call and refuses a write after one, then calls next.
func (c *caller) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if strings.HasPrefix(name, mailPrefix) {
		if err := c.b.noteMail(name); err != nil {
			return nil, err
		}
	} else if c.b.watching() && c.writes(ctx, name, args) {
		if err := c.b.refuse("called " + name); err != nil {
			return nil, err
		}
	}
	return c.next.CallTool(ctx, name, args) //nolint:wrapcheck // the run reads the tool's own error
}

// writes classifies a call the way the draft barrier does: the table, then
// for a tool no rule names, what the server advertises about it.
func (c *caller) writes(ctx context.Context, name string, args map[string]any) bool {
	d := c.b.classifier.Classify(name, args)
	if !d.Writes || d.Declared || toolwrite.Classified(name) {
		return d.Writes
	}
	if declarer, ok := c.next.(scriptrun.ReadOnlyDeclarer); ok {
		if readOnly, known := declarer.DeclaresReadOnly(ctx, name); known && readOnly {
			return false
		}
	}
	return true
}

// DeclaresReadOnly passes through to the wrapped caller, so the run engine's
// own classification keeps working.
func (c *caller) DeclaresReadOnly(ctx context.Context, name string) (readOnly, known bool) {
	if declarer, ok := c.next.(scriptrun.ReadOnlyDeclarer); ok {
		return declarer.DeclaresReadOnly(ctx, name)
	}
	return false, false
}

// Exporter wraps next. A replay's caller (scriptrun.HostValuer) is never
// wrapped: a test replays a recording and makes no call this would refuse.
func (b *Barrier) Exporter(next scriptrun.Exporter) scriptrun.Exporter {
	return &exporter{b: b, next: next}
}

type exporter struct {
	b    *Barrier
	next scriptrun.Exporter
}

// Export refuses an output after mail was read, else writes it.
func (e *exporter) Export(ctx context.Context, req scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	if err := e.b.refuse("wrote an output with platform.export"); err != nil {
		return nil, err
	}
	return e.next.Export(ctx, req) //nolint:wrapcheck // the run reads the exporter's own error
}

// PublishData refuses a refresh after mail was read, else makes it.
func (e *exporter) PublishData(ctx context.Context, req scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	if err := e.b.refuse("published data with platform.publish_data"); err != nil {
		return nil, err
	}
	return e.next.PublishData(ctx, req) //nolint:wrapcheck // the run reads the exporter's own error
}

// RecordToolOutput passes through to the wrapped exporter, so an output an
// export tool wrote is still listed on the run.
func (e *exporter) RecordToolOutput(ctx context.Context, out script.RunOutput) {
	if rec, ok := e.next.(scriptrun.ToolOutputRecorder); ok {
		rec.RecordToolOutput(ctx, out)
	}
}
