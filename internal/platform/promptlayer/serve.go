package promptlayer

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Serve surfaces recorded as the ToolName of a prompt_serve audit event.
const (
	serveSurfacePromptsGet = "prompts/get"
	serveSurfaceUse        = "manage_prompt"
)

// _meta keys carrying prompt provenance on prompts/get results, so an agent
// can state what it is about to run ("Daily Sales Report v4, approved by
// jane@ on 2026-06-12") before executing (#1009).
const (
	metaPromptReference  = "prompt_reference"
	metaPromptVersion    = "prompt_version"
	metaPromptApprovedBy = "prompt_approved_by"
	metaPromptApprovedAt = "prompt_approved_at"
)

// attachProvenanceMeta stamps the served prompt's identity, version, and
// approval provenance into the result's _meta.
func attachProvenanceMeta(res *mcp.GetPromptResult, pr *prompt.Prompt) {
	if res.Meta == nil {
		res.Meta = mcp.Meta{}
	}
	res.Meta[metaPromptVersion] = pr.Version
	if pr.ID != "" {
		res.Meta[metaPromptReference] = promptRefPrefix + pr.ID
	}
	if pr.ApprovedBy != "" {
		res.Meta[metaPromptApprovedBy] = pr.ApprovedBy
	}
	if pr.ApprovedAt != nil {
		res.Meta[metaPromptApprovedAt] = pr.ApprovedAt.UTC().Format(time.RFC3339)
	}
}

// auditPromptServe records a prompt_serve audit event for a successfully
// served database prompt. These events are the source of the per-prompt run
// count and last-run rollup (prompt.UsageReader). email is the caller identity
// resolved by the serving path; it backstops the prompts/get path, where no
// PlatformContext exists (the tool-call middleware only handles tools/call).
// No-op without a bound audit logger.
func (h *Handle) auditPromptServe(ctx context.Context, pr *prompt.Prompt, surface, email string) {
	if h.auditLogger == nil || pr.ID == "" {
		return
	}
	ev := middleware.AuditEvent{
		Timestamp: time.Now().UTC(),
		ToolName:  surface,
		UserEmail: email,
		Success:   true,
		// The serve already passed the visibility checks; without this the
		// row would persist authorized=false and read as a denial.
		Authorized: true,
		Source:     "mcp",
		EventKind:  string(audit.EventTypePromptServe),
		Parameters: map[string]any{
			"prompt_id":   pr.ID,
			"prompt_name": pr.Name,
			"version":     pr.Version,
		},
	}
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		ev.RequestID = pc.RequestID
		ev.SessionID = pc.SessionID
		ev.UserID = pc.UserID
		ev.Persona = pc.PersonaName
		ev.Transport = pc.Transport
		if pc.UserEmail != "" {
			ev.UserEmail = pc.UserEmail
		}
	}
	// context.Background is intentional, matching the MCP audit middleware:
	// the async writer ignores the context and a sync write must not be
	// canceled when the serving request ends.
	if err := h.auditLogger.Log(context.Background(), ev); err != nil {
		slog.Warn("failed to log prompt serve event", logKeyError, err, promptLogKey, pr.Name)
	}
}

// applyUsage fills the prompt's audit-derived usage fields from the bound
// usage reader. No-op without a reader; a read failure logs and leaves the
// fields empty rather than failing the caller's read.
func (h *Handle) applyUsage(ctx context.Context, pr *prompt.Prompt) {
	if h.usage == nil || pr.ID == "" {
		return
	}
	usage, err := h.usage.PromptUsage(ctx, []string{pr.ID})
	if err != nil {
		slog.Warn("failed to read prompt usage", logKeyError, err, promptLogKey, pr.Name)
		return
	}
	if u, ok := usage[pr.ID]; ok {
		pr.RunCount = u.RunCount
		pr.LastRunAt = u.LastRunAt
	}
}
