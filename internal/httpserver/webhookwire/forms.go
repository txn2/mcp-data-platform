package webhookwire

import (
	"context"
	"errors"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcollect"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epsource"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/scripttoken"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// FormRunner is the endpoint runner, which stores a form's accepted
// submissions through what it is handed.
type FormRunner interface {
	SetCollector(c *epcollect.Collector)
}

// FormCommands is the endpoint commands, whose endpoint_set makes a form's
// source through what they are handed.
type FormCommands interface {
	SetForms(f epcmd.FormSources)
}

// errNoReceiver is a form submitted on a replica that does not run the
// receiver, which every stored event passes through.
var errNoReceiver = errors.New("this replica does not run the webhook receiver (webhooks.receiver.enabled is false), " +
	"so it stores no form submission")

// FormDeps are what forms are attached through.
type FormDeps struct {
	Runner   FormRunner
	Commands FormCommands
	// Tokens is the run members' issuer, which answers platform.decrypt.
	Tokens *scripttoken.Issuer
	// Audit records each decrypt.
	Audit middleware.AuditLogger
	// Sealer encrypts and decrypts sensitive fields.
	Sealer epcollect.Sealer
	// Connections names where a source's tables can be created.
	Connections func() []string
	// MaxFileBytes is resources.managed.max_upload_bytes, the most a form's
	// saved file may hold.
	MaxFileBytes int64
}

// AttachForms makes forms store their submissions in webhook sources
// (#2178): endpoint_set makes each form's source through the source service,
// and the runner stores each accepted submission through this replica's
// receiver, sealing sensitive fields first and saving its files as managed
// resources (#2333); platform.decrypt opens them for a run the form's
// decrypt_personas admit (#2179). A nil Webhooks attaches
// nothing, and the runner stores no submission, as before.
func (w *Webhooks) AttachForms(d FormDeps) {
	if w == nil || w.Service == nil {
		return
	}
	d.Commands.SetForms(&epsource.Maker{Admin: w.Service, Connections: d.Connections})
	c := &epcollect.Collector{Sources: noReceiver{}, Writer: noReceiver{}, Sealer: d.Sealer, MaxFileBytes: d.MaxFileBytes}
	if w.receiver != nil {
		c.Sources, c.Writer = w.receiver, w.receiver
		if w.files != nil {
			c.Files = w.files
		}
	}
	d.Runner.SetCollector(c)
	d.Tokens.SetDecrypter(&epcollect.Decrypter{Sources: w.Service, Sealer: d.Sealer, Audit: d.Audit})
}

// noReceiver refuses every submission on a replica without a receiver.
type noReceiver struct{}

// FormSource answers that every form has a source, so Deliver says why
// nothing is stored.
func (noReceiver) FormSource(context.Context, string) (whsource.Source, bool) {
	return whsource.Source{Config: whsource.Config{Input: whsource.InputForm}}, true
}

// Deliver refuses the submission.
func (noReceiver) Deliver(context.Context, whsource.Source, whevent.Event, []byte) error {
	return errNoReceiver
}

// AttachEventRuns has each event this replica stores -- posted, a form's
// submission, or a message an imap source filed -- told to stored once it is
// durable (#2332), which enqueues the runs of the automations that name its
// source. A nil Webhooks attaches nothing.
func (w *Webhooks) AttachEventRuns(stored func(ctx context.Context, source string, events []whevent.Event)) {
	if w == nil {
		return
	}
	if w.receiver != nil {
		w.receiver.SetStored(stored)
	}
	if w.imap != nil {
		w.imap.SetStored(stored)
	}
}

// AttachScriptRuns enqueues the runs of the automations that name a source
// for each event this replica stores (#2332), bounded per script by
// scripts.events.max_queued. A deployment with no database runs no script,
// and attaches nothing.
func (w *Webhooks) AttachScriptRuns(p *platform.Platform) {
	if w == nil || p == nil || p.DB() == nil {
		return
	}
	runs := &scriptevents.Enqueuer{Store: scriptevents.NewStore(p.DB()), Scripts: scriptstore.New(p.DB()), MaxQueued: p.Config().Scripts.Events.MaxQueued}
	w.AttachEventRuns(runs.Stored)
}
