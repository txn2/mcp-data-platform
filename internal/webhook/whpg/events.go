package whpg

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/webhook/receiver"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// The fields of the document an event is built from. Each event is the
// payload in an envelope saying where it came from, so a source's paths
// (event_id_path, event_type_path, key_path, split) read the same names for
// every source of the input.
const (
	fieldChannel  = "channel"
	fieldPayload  = "payload"
	fieldPID      = "pid"
	fieldOutboxID = "outbox_id"
	fieldTable    = "table"
	fieldOp       = "op"
	fieldRow      = "row"
	fieldCreated  = "created_at"
)

// filing is one delivery: the source it files for, the paths that read its
// documents, when it was read, the events to store and what it refused.
type filing struct {
	src      whsource.Source
	paths    whevent.Paths
	now      time.Time
	events   []whevent.Event
	rejected []whstore.Rejection
}

// decimal is the base an outbox row's id is written in.
const decimal = 10

// newFiling starts a delivery for src, read now.
func newFiling(src whsource.Source, now time.Time) (*filing, error) {
	paths, err := whevent.ParsePaths(src.Config)
	if err != nil {
		return nil, fmt.Errorf("reading source %s's paths: %w", src.Name, err)
	}
	return &filing{src: src, paths: paths, now: now}, nil
}

// refuse records one refused payload, named by id when it has one.
func (f *filing) refuse(outcome, reason, id string) {
	r := whstore.Rejection{Source: f.src.Name, At: f.now, FirstAt: f.now, Count: 1, Outcome: outcome, Reason: reason}
	if id != "" {
		r.EventIDs = []string{id}
	}
	f.rejected = append(f.rejected, r)
}

// fileRows files an outbox's rows, refusing each past the size bound or that
// the source's paths cannot read. A refused row is deleted with the rest:
// kept, it would be refused again on every drain.
func (m *Manager) fileRows(ctx context.Context, src whsource.Source, rows []postgres.OutboxRow) error {
	f, err := newFiling(src, m.cfg.Now().UTC())
	if err != nil {
		return err
	}
	limit := src.Config.Postgres.MaxPayloadBytes
	for _, r := range rows {
		id := "outbox-" + strconv.FormatInt(r.ID, decimal)
		// Every column of the row is written by whoever can write the
		// outbox, so all of it counts against the bound, and the refusal
		// names the row by its id alone.
		if size := len(r.Payload) + len(r.Table) + len(r.Op); size > limit {
			f.refuse(receiver.OutcomeTooLarge, fmt.Sprintf(
				"outbox row %d is %d bytes, more than max_payload_bytes (%d)", r.ID, size, limit), id)
			continue
		}
		doc := map[string]any{
			fieldOutboxID: r.ID, fieldTable: r.Table, fieldOp: r.Op,
			fieldRow: json.RawMessage(r.Payload), fieldCreated: r.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
		m.build(f, doc, id)
	}
	return m.store(ctx, f)
}

// fileNotifications files a batch of notifications. A payload that is JSON
// is the event's payload as JSON; any other text is a JSON string.
func (m *Manager) fileNotifications(ctx context.Context, src whsource.Source, batch []postgres.Notification) error {
	f, err := newFiling(src, m.cfg.Now().UTC())
	if err != nil {
		return err
	}
	limit := src.Config.Postgres.MaxPayloadBytes
	for _, n := range batch {
		if len(n.Payload) > limit {
			f.refuse(receiver.OutcomeTooLarge, fmt.Sprintf(
				"a notification on channel %s is %d bytes, more than max_payload_bytes (%d)",
				logsan.SanitizeForLog(n.Channel), len(n.Payload), limit), "")
			continue
		}
		var payload any = n.Payload
		if json.Valid([]byte(n.Payload)) {
			payload = json.RawMessage(n.Payload)
		}
		doc := map[string]any{fieldChannel: n.Channel, fieldPayload: payload, fieldPID: n.PID}
		m.build(f, doc, m.nextID())
	}
	return m.store(ctx, f)
}

// nextID is a notification's event id when the source names no path for
// one: the replica and a counter, so two notifications with one payload are
// two events.
func (m *Manager) nextID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	return fmt.Sprintf("notify-%s-%d-%d", m.cfg.Replica, m.cfg.Now().UnixNano(), m.seq)
}

// build adds doc's events to f, or a refusal when the source's paths cannot
// read it. An event the source's event_id_path does not name takes id.
func (m *Manager) build(f *filing, doc map[string]any, id string) {
	body, err := json.Marshal(doc)
	if err != nil {
		f.refuse(receiver.OutcomeInvalidBody, "the payload could not be encoded as JSON", id)
		return
	}
	events, err := whevent.Build(body, f.paths, f.now, m.cfg.Replica)
	if err != nil {
		f.refuse(receiver.OutcomeInvalidBody, err.Error(), id)
		return
	}
	for i := range events {
		if f.src.Config.EventIDPath != "" {
			continue
		}
		events[i].EventID = id
		if len(events) > 1 {
			events[i].EventID = id + "-" + strconv.Itoa(i)
		}
	}
	f.events = append(f.events, events...)
}

// store writes f's events, then records its refusals and the listener's
// counts. A failed write returns before anything is recorded, so a retried
// drain records its refusals once.
func (m *Manager) store(ctx context.Context, f *filing) error {
	src := f.src
	if err := m.cfg.Writer.Write(ctx, src, f.events); err != nil {
		return fmt.Errorf("filing source %s's events: %w", src.Name, err)
	}
	if len(f.rejected) > 0 && m.cfg.Rejections != nil {
		if err := m.cfg.Rejections.RecordRejections(ctx, f.rejected); err != nil {
			m.cfg.Logger.WarnContext(ctx, "webhooks: recording a postgres source's refusals", "source", logsan.SanitizeForLog(src.Name),
				"error", logsan.SanitizeForLog(err.Error()))
		}
	}
	m.state(ctx, src.Name, func(ctx context.Context) error {
		return m.cfg.States.Filed(ctx, src.Name, int64(len(f.events)), int64(len(f.rejected)), f.now)
	})
	return nil
}
