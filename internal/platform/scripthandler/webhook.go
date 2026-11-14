package scripthandler

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// setWebhook runs the script for a webhook source: each event the source
// stores enqueues one run. Naming a source is reading every event it stores,
// so the source rule decides: an administrator any source, anyone else only
// the source made for a form whose script they own. Adding a handler for a
// source the script already runs for turns that handler on; naming the
// handler changes nothing, since its source does not change and turning it on
// is handler_enable's.
func (s *Service) setWebhook(ctx context.Context, req SetRequest) (map[string]any, error) {
	held, err := s.webhooksFor(ctx, req)
	if err != nil {
		return nil, err
	}
	existing, err := heldWebhook(held, req)
	if err != nil {
		return nil, err
	}
	sc := req.Script
	if existing != nil && req.Handler != "" {
		return webhookOut(sc, *existing, fmt.Sprintf("Webhook handler %s already runs %s for source %s; nothing changed.",
			existing.ID, sc.Name, existing.Source)), nil
	}
	if existing == nil && len(held) >= MaxPerKind {
		return nil, fmt.Errorf("script %s already has %d webhook handlers, the most a script may hold: %w", sc.Name, len(held), ErrNotAllowed)
	}
	w, created, err := s.Webhooks.AddHandler(ctx, sc.ID, req.EventSource, req.Email)
	if err != nil {
		return nil, storeErr(err)
	}
	if created {
		return webhookOut(sc, w, fmt.Sprintf("Each event source %s stores enqueues one run of %s, reading run.event.", w.Source, sc.Name)), nil
	}
	return webhookOut(sc, w, fmt.Sprintf("%s already runs for source %s; the handler is on.", sc.Name, w.Source)), nil
}

// webhooksFor is the webhook handlers req's script holds, once the caller
// may run it for req's source.
func (s *Service) webhooksFor(ctx context.Context, req SetRequest) ([]scriptevents.Handler, error) {
	if s.Webhooks == nil || s.Sources == nil {
		return nil, errNoSources
	}
	if err := s.checkSource(ctx, req.EventSource, req.Caller); err != nil {
		return nil, err
	}
	held, err := s.Webhooks.Handlers(ctx, scriptevents.HandlerFilter{ScriptIDs: []string{req.Script.ID}})
	if err != nil {
		return nil, storeErr(err)
	}
	return held, nil
}

// heldWebhook is the handler in held for req's source, nil for none. A
// handler req names that runs for another source is refused: a handler's
// source does not change.
func heldWebhook(held []scriptevents.Handler, req SetRequest) (*scriptevents.Handler, error) {
	var found *scriptevents.Handler
	for i := range held {
		h := &held[i]
		if h.ID == req.Handler && h.Source != req.EventSource {
			return nil, fmt.Errorf("webhook handler %s runs for %s, and a handler's source does not change: "+
				"add a handler for %s and delete this one: %w", h.ID, h.Source, req.EventSource, ErrInvalid)
		}
		if h.Source == req.EventSource {
			found = h
		}
	}
	return found, nil
}

// webhookOut is w as handler_set reports it, with the sentence saying what
// changed.
func webhookOut(sc *script.Script, w scriptevents.Handler, message string) map[string]any {
	out := webhookFields(sc, w)
	out["message"] = message
	return out
}

// webhookFields is one webhook handler as the commands report it. It holds
// the source's name only: a source's secrets are never read here.
func webhookFields(sc *script.Script, w scriptevents.Handler) map[string]any {
	return map[string]any{
		fieldHandler: w.ID, fieldKind: KindWebhook, fieldName: sc.Name, "event_source": w.Source,
		fieldEnabled: w.Enabled, fieldCreatedBy: w.CreatedBy, "created_at": w.CreatedAt,
		"updated_by": w.UpdatedBy, "updated_at": w.UpdatedAt,
	}
}
