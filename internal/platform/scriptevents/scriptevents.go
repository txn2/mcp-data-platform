// Package scriptevents enqueues a run of each automation that names a webhook
// source for every event the source stores (#2332). The webhook receiver
// (posted events and form submissions) and the imap poller hand it a source's
// events once they are durable; each event becomes one run with trigger
// event, carrying the event as run.event, executed by the run queue's
// workers under the script's saved author roles, as a scheduled run is.
//
// Nothing runs inline: the sender was answered when the event was stored, and
// enqueuing is one transaction per script that names the source. A failure to
// enqueue is logged, and never fails the delivery, which stored the event
// whatever happens to its runs.
package scriptevents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// DefaultMaxQueued is how many pending event runs a script may hold when
// scripts.events.max_queued names none; an event past it is recorded as
// skipped_backlog.
const DefaultMaxQueued = 100

// defaultTimeout bounds one source's enqueue.
const defaultTimeout = 10 * time.Second

// The keys run.event carries.
const (
	KeyID         = "id"
	KeyType       = "type"
	KeyKey        = "key"
	KeyReceivedAt = "received_at"
	KeySource     = "source"
	KeyPayload    = "payload"
)

// Store lists the scripts that run for a source and enqueues their runs
// (PostgresStore).
type Store interface {
	ScriptsForSource(ctx context.Context, source string) ([]string, error)
	EnqueueEvents(ctx context.Context, runs []*script.Run, maxQueued int) (EventEnqueue, error)
}

// Scripts reads a script and the version a run of it executes
// (scriptstore.Store).
type Scripts interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
	GetVersion(ctx context.Context, scriptID string, version int) (*script.Version, error)
}

// Enqueuer enqueues the runs of a source's events.
type Enqueuer struct {
	Store   Store
	Scripts Scripts
	// MaxQueued bounds each script's pending event runs; zero is
	// DefaultMaxQueued.
	MaxQueued int
	// Timeout bounds one source's enqueue; zero is ten seconds.
	Timeout time.Duration
}

// Stored enqueues one run of each script that names source for each of
// events, which the source has made durable. It does not return an error:
// the events are stored, and what failed is logged.
func (e *Enqueuer) Stored(ctx context.Context, source string, events []whevent.Event) {
	if e == nil || len(events) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.timeout())
	defer cancel()
	ids, err := e.Store.ScriptsForSource(ctx, source)
	if err != nil {
		e.fail(ctx, source, "", err)
		return
	}
	for _, id := range ids {
		if err := e.enqueue(ctx, source, id, events); err != nil {
			e.fail(ctx, source, id, err)
		}
	}
}

// enqueue writes the runs of events for one script.
func (e *Enqueuer) enqueue(ctx context.Context, source, scriptID string, events []whevent.Event) error {
	sc, err := e.Scripts.GetByID(ctx, scriptID)
	if err != nil {
		return fmt.Errorf("reading the script: %w", err)
	}
	if refusal := script.RefuseRun(sc); refusal != nil {
		slog.InfoContext(ctx, "scripts: an event's run was not enqueued: the script is not executable",
			"source", logsan.SanitizeForLog(source), "script_id", scriptID, "reason", logsan.SanitizeForLog(refusal.Error()))
		return nil
	}
	v, err := e.Scripts.GetVersion(ctx, sc.ID, sc.Version)
	if err != nil || v == nil {
		return fmt.Errorf("reading the script's current version: %w", err)
	}
	params, err := script.BindParams(v.Params, nil)
	if err != nil {
		return fmt.Errorf("the script's parameters have no value for an event run, which passes none: %w", err)
	}
	runs := make([]*script.Run, 0, len(events))
	for _, ev := range events {
		id, err := pkgsession.GenerateScriptSessionID()
		if err != nil {
			return fmt.Errorf("minting a run id: %w", err)
		}
		runs = append(runs, &script.Run{
			ID: id, ScriptID: sc.ID, VersionID: v.ID, Version: v.Version,
			Trigger: runstate.TriggerEvent, Params: params,
			// Nobody asked for an event run; its owner is who hears about
			// it, as a schedule's author is for a fire.
			RequestedBy: sc.OwnerEmail,
			EventSource: source, EventID: ev.EventID, Event: Value(source, ev),
		})
	}
	got, err := e.Store.EnqueueEvents(ctx, runs, e.maxQueued())
	if err != nil {
		return err //nolint:wrapcheck // the store names what failed
	}
	slog.InfoContext(ctx, "scripts: event runs enqueued", "source", logsan.SanitizeForLog(source), "script_id", sc.ID,
		"enqueued", got.Enqueued, "skipped", got.Skipped, "already", got.Already)
	return nil
}

// Value is ev as run.event reads it: its id, type, key, when it was received
// and from which source, and its payload decoded. A payload that is not JSON,
// which a source never stores, is handed over as its text.
func Value(source string, ev whevent.Event) map[string]any {
	var payload any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		payload = string(ev.Payload)
	}
	return map[string]any{
		KeyID: ev.EventID, KeyType: ev.EventType, KeyKey: ev.Key,
		KeyReceivedAt: ev.ReceivedAt.UTC().Format(time.RFC3339Nano), KeySource: source, KeyPayload: payload,
	}
}

func (*Enqueuer) fail(ctx context.Context, source, scriptID string, err error) {
	slog.WarnContext(ctx, "scripts: enqueuing an event's runs failed; the event is stored",
		"source", logsan.SanitizeForLog(source), "script_id", scriptID, "error", logsan.SanitizeForLog(err.Error()))
}

func (e *Enqueuer) maxQueued() int {
	if e.MaxQueued > 0 {
		return e.MaxQueued
	}
	return DefaultMaxQueued
}

func (e *Enqueuer) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return defaultTimeout
}
