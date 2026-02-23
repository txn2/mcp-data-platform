package middleware

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

// DefaultWarningMessage is the default message prepended to query results
// when no discovery has been performed in the session.
const DefaultWarningMessage = "⚠️ REQUIRED: You must call datahub_search first to discover the table's " +
	"business context (descriptions, owners, tags, glossary terms) before running queries. " +
	"This ensures you understand the data semantics and any access restrictions."

// DefaultEscalationMessage is the default message after repeated warnings.
// The placeholder {count} is replaced with the current warning count.
const DefaultEscalationMessage = "🚫 MANDATORY — datahub_search has not been called yet ({count} queries without discovery). " +
	"Call datahub_search NOW before issuing any more SQL. " +
	"Querying without understanding the data context risks incorrect results and policy violations."

// RuleEnforcementConfig configures the rule enforcement middleware.
type RuleEnforcementConfig struct {
	// Engine is the tuning rule engine for static rules (backward compat).
	Engine *tuning.RuleEngine

	// WorkflowTracker enables session-aware workflow gating. If nil, the
	// middleware falls back to the static engine.ShouldRequireDataHubCheck().
	WorkflowTracker *SessionWorkflowTracker

	// WorkflowConfig configures session-aware workflow behavior.
	WorkflowConfig WorkflowRulesConfig
}

// WorkflowRulesConfig configures session-aware workflow gating behavior.
type WorkflowRulesConfig struct {
	// RequireDiscoveryBeforeQuery enables session-aware gating.
	RequireDiscoveryBeforeQuery bool

	// WarningMessage is prepended to query results when no discovery has occurred.
	// Defaults to DefaultWarningMessage.
	WarningMessage string

	// EscalationAfterWarnings is the number of standard warnings before escalation.
	// Defaults to 3.
	EscalationAfterWarnings int

	// EscalationMessage replaces the standard warning after the threshold.
	// The placeholder {count} is replaced with the current warning count.
	// Defaults to DefaultEscalationMessage.
	EscalationMessage string
}

// MCPRuleEnforcementMiddleware creates MCP protocol-level middleware that enforces
// operational rules and adds guidance to tool responses.
//
// When a WorkflowTracker is provided and RequireDiscoveryBeforeQuery is true,
// the middleware uses session-aware gating: it only warns when the current
// session has not yet called any discovery tool, and escalates after repeated
// warnings.
//
// Without a tracker, it falls back to the static RequireDataHubCheck rule
// (backward compatible).
func MCPRuleEnforcementMiddleware(cfg RuleEnforcementConfig) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			pc := GetPlatformContext(ctx)
			if pc == nil {
				return next(ctx, method, req)
			}

			hints := collectRuleHints(pc, cfg)

			result, err := next(ctx, method, req)

			if len(hints) > 0 && err == nil {
				result = prependHintsToResult(result, hints)
			}

			return result, err
		}
	}
}

// collectRuleHints determines which hints/warnings to prepend based on
// the current workflow state.
func collectRuleHints(pc *PlatformContext, cfg RuleEnforcementConfig) []string {
	var hints []string

	// Session-aware path: tracker is configured and enabled
	if cfg.WorkflowTracker != nil && cfg.WorkflowConfig.RequireDiscoveryBeforeQuery {
		if cfg.WorkflowTracker.IsQueryTool(pc.ToolName) && !cfg.WorkflowTracker.HasPerformedDiscovery(pc.SessionID) {
			count := cfg.WorkflowTracker.IncrementWarningCount(pc.SessionID)
			threshold := cfg.WorkflowConfig.EscalationAfterWarnings
			if threshold > 0 && count > threshold {
				msg := effectiveEscalationMessage(cfg.WorkflowConfig)
				hints = append(hints, formatEscalationMessage(msg, count))
			} else {
				hints = append(hints, effectiveWarningMessage(cfg.WorkflowConfig))
			}
		}
		return hints
	}

	// Static fallback path: use the old RequireDataHubCheck rule
	if cfg.Engine != nil && cfg.Engine.ShouldRequireDataHubCheck() && isQueryTool(pc.ToolName) {
		hints = append(hints,
			"💡 Tip: Consider using datahub_search or datahub_get_entity first "+
				"to understand the data context before querying.")
	}

	return hints
}

// effectiveWarningMessage returns the configured warning or the default.
func effectiveWarningMessage(cfg WorkflowRulesConfig) string {
	if cfg.WarningMessage != "" {
		return cfg.WarningMessage
	}
	return DefaultWarningMessage
}

// effectiveEscalationMessage returns the configured escalation message or the default.
func effectiveEscalationMessage(cfg WorkflowRulesConfig) string {
	if cfg.EscalationMessage != "" {
		return cfg.EscalationMessage
	}
	return DefaultEscalationMessage
}

// formatEscalationMessage replaces the {count} placeholder in the escalation message.
func formatEscalationMessage(template string, count int) string {
	return strings.ReplaceAll(template, "{count}", fmt.Sprintf("%d", count))
}

// isQueryTool returns true if the tool name indicates a query/write operation.
func isQueryTool(toolName string) bool {
	queryTools := []string{
		"trino_query",
		"trino_execute",
	}

	return slices.Contains(queryTools, toolName)
}

// prependHintsToResult adds hints as text content at the beginning of the result.
func prependHintsToResult(result mcp.Result, hints []string) mcp.Result {
	callResult, ok := result.(*mcp.CallToolResult)
	if !ok || callResult == nil {
		return result
	}

	// Don't add hints to error results
	if callResult.IsError {
		return result
	}

	// Build hints text
	hintsText := strings.Join(hints, "\n") + "\n\n---\n\n"

	// Prepend hints as text content
	hintContent := &mcp.TextContent{Text: hintsText}
	callResult.Content = append([]mcp.Content{hintContent}, callResult.Content...)

	return callResult
}
