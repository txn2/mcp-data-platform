package trino

import (
	"context"
	"errors"
	"log/slog"
	"regexp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

// transportEnvelopeRe matches one connector transport-envelope segment that
// Trino wraps around upstream engine errors, e.g.
//
//	method [GET], host [https://internal-service:9200], URI [/idx/_search], status line [HTTP/1.1 400 Bad Request]
//
// The method/host/URI segments disclose internal topology (service DNS names,
// ports, index paths) to any caller who can run a failing query; the engine's
// own error payload (parse reason, line/col, status line) is what the caller
// actually needs and is left intact.
//
// A segment's bracketed value may itself contain ']' (IPv6 hosts, bracketed
// index paths), so the value is matched lazily up to the '], ' that precedes
// the next segment keyword, or to the end of the message. RE2 has no
// lookahead, so the following keyword is captured and re-emitted by the
// replacement; sanitizeUpstreamError loops to a fixpoint because the re-emit
// hides adjacent segments from a single pass.
var transportEnvelopeRe = regexp.MustCompile(`(?:method|host|URI) \[.*?\](?:, ((?:method|host|URI|status line) \[)|$)`)

// sanitizeUpstreamError strips the connector transport envelope from an
// upstream engine error string, keeping the engine's diagnostic payload.
func sanitizeUpstreamError(msg string) string {
	for {
		out := transportEnvelopeRe.ReplaceAllString(msg, "$1")
		if out == msg {
			return out
		}
		msg = out
	}
}

// ErrorSanitizerMiddleware scrubs internal topology from upstream engine
// errors before they reach tool callers. The full original error is logged
// at debug level for operators.
type ErrorSanitizerMiddleware struct{}

// Before implements trinotools.ToolMiddleware; it passes through unchanged.
func (*ErrorSanitizerMiddleware) Before(ctx context.Context, _ *trinotools.ToolContext) (context.Context, error) {
	return ctx, nil
}

// After sanitizes the handler error and any error-result text.
func (*ErrorSanitizerMiddleware) After(_ context.Context, tc *trinotools.ToolContext, result *mcp.CallToolResult, handlerErr error) (*mcp.CallToolResult, error) {
	if handlerErr != nil {
		original := handlerErr.Error()
		if sanitized := sanitizeUpstreamError(original); sanitized != original {
			slog.Debug("trino: sanitized upstream error", "tool", tc.Name, "original", original)
			return result, errors.New(sanitized)
		}
		return result, handlerErr
	}

	if result != nil && result.IsError {
		for _, content := range result.Content {
			textContent, ok := content.(*mcp.TextContent)
			if !ok {
				continue
			}
			if sanitized := sanitizeUpstreamError(textContent.Text); sanitized != textContent.Text {
				slog.Debug("trino: sanitized upstream error", "tool", tc.Name, "original", textContent.Text)
				textContent.Text = sanitized
			}
		}
	}
	return result, nil
}

// Verify interface compliance.
var _ trinotools.ToolMiddleware = (*ErrorSanitizerMiddleware)(nil)
