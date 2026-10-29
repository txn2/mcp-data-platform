package epcollect

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/scripttoken"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// SourceLister lists the webhook sources (whadmin.Service).
type SourceLister interface {
	List(ctx context.Context) ([]whsource.Source, error)
}

// Decrypter opens a form's sensitive fields for a script run whose persona
// the form's decrypt_personas names, or the administrator's, and audits each
// read with the fields it opened (#2179). It answers platform.decrypt.
type Decrypter struct {
	Sources SourceLister
	Sealer  Sealer
	Audit   middleware.AuditLogger
}

// Decrypt answers one platform.decrypt.
func (d *Decrypter) Decrypt(ctx context.Context, req scripttoken.DecryptRequest) (values map[string]any, opened []string, err error) {
	src, findErr := FindForm(ctx, d.Sources, req.Form)
	if findErr != nil {
		return nil, nil, findErr
	}
	personas := src.Config.Form.DecryptPersonas
	if !req.Admin && (req.Persona == "" || !slices.Contains(personas, req.Persona)) {
		d.audit(ctx, req, src, nil, "refused: persona not among decrypt_personas")
		return nil, nil, fmt.Errorf("form %s does not let persona %q decrypt its sensitive fields; its decrypt_personas are [%s], "+
			"set with manage_script endpoint_set", req.Form, req.Persona, strings.Join(personas, ", "))
	}
	stored, sensitive := PayloadValues(req.Payload)
	values, err = Reveal(d.Sealer, src.Name, stored, sensitive)
	if err != nil {
		d.audit(ctx, req, src, sensitive, "failed: "+err.Error())
		return nil, nil, err
	}
	d.audit(ctx, req, src, sensitive, "")
	return values, present(sensitive, stored), nil
}

// audit writes the read's row under the run's identity.
func (d *Decrypter) audit(ctx context.Context, req scripttoken.DecryptRequest, src whsource.Source, fields []string, failure string) {
	if d.Audit == nil {
		return
	}
	who := "script:" + req.At.ScriptID
	if req.At.Endpoint != "" {
		who = "endpoint:" + req.At.Endpoint
	}
	event := middleware.AuditEvent{
		Timestamp: time.Now().UTC(), UserID: who, Persona: req.Persona, ToolName: scripttoken.CapabilityDecrypt,
		Success: failure == "", Authorized: failure == "" || !strings.HasPrefix(failure, "refused"),
		Source: middleware.SourceScript, EventKind: string(audit.EventTypeFormDecrypt),
		Parameters: map[string]any{
			"form": src.Config.Form.Endpoint, "source": src.Name, "fields": nonNil(fields), "submissions": 1,
			"script_id": req.At.ScriptID, "endpoint": req.At.Endpoint,
		},
		ErrorMessage: failure,
	}
	if err := d.Audit.Log(ctx, event); err != nil {
		slog.WarnContext(ctx, "forms: recording a decrypt failed", "form", logsan.SanitizeForLog(src.Name),
			"error", logsan.SanitizeForLog(err.Error()))
	}
}

// FindForm is the form source named by its endpoint (as it was bound) or by
// its source's name.
func FindForm(ctx context.Context, sources SourceLister, form string) (whsource.Source, error) {
	list, err := sources.List(ctx)
	if err != nil {
		return whsource.Source{}, fmt.Errorf("listing webhook sources: %w", err)
	}
	for _, s := range list {
		if !s.IsForm() || s.Config.Form == nil {
			continue
		}
		if s.Config.Form.Endpoint == form || s.Name == form || s.Name == whsource.FormSourceName(form) {
			return s, nil
		}
	}
	return whsource.Source{}, fmt.Errorf("no form named %q stores submissions here", form)
}

// PayloadValues reads a stored submission's payload: its values and the
// names of its sensitive fields.
func PayloadValues(payload map[string]any) (values map[string]any, sensitive []string) {
	values, _ = payload[KeyValues].(map[string]any)
	if values == nil {
		values = map[string]any{}
	}
	switch list := payload[KeySensitive].(type) {
	case []string:
		sensitive = list
	case []any:
		for _, v := range list {
			if s, ok := v.(string); ok {
				sensitive = append(sensitive, s)
			}
		}
	}
	return values, sensitive
}

// present is the names in sensitive that values holds.
func present(sensitive []string, values map[string]any) []string {
	out := []string{}
	for _, name := range sensitive {
		if _, ok := values[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
