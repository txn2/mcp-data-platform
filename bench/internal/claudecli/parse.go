package claudecli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// infoToolName is the platform's session-mint tool. Its audit row carries the
// transport session id rather than the correlated user identity's data calls,
// and it is platform infrastructure rather than a data tool under test, so it
// is excluded from the tool-call accounting (both here and in the audit
// read-back). Mirrors mcpc.infoToolName.
const infoToolName = "platform_info"

// searchToolName is the discovery tool whose invocation marks that the agent
// surfaced saved knowledge itself. Mirrors the lifecycle runner's constant so
// the claude-cli path reports RecallSurfaced identically to the loop path.
const searchToolName = "search"

// Result is the parsed outcome of one `claude -p` episode. It carries what both
// bench runners need: the final answer and reconstructed transcript for
// grading and manual review, the tool-call accounting that bounds the audit
// read-back, and the dps_ handle and platform version claude read from
// platform_info.
type Result struct {
	// FinalText is the model's final answer (the result event's text).
	FinalText string
	// Transcript is the provider-agnostic conversation, seeded with the task
	// prompt so the per-episode transcript file matches the loop path's shape.
	Transcript []llm.Message
	// Usage is the token usage the CLI reported for the run.
	Usage llm.Usage
	// ClaudeSessionID is Claude Code's own session UUID (not the platform
	// handle), recorded for cross-referencing the CLI's session logs.
	ClaudeSessionID string
	// Handle is the platform dps_ session handle claude minted via
	// platform_info, parsed from that call's result (best effort).
	Handle string
	// PlatformVersion is the deployment version from the platform_info result.
	PlatformVersion string
	// MCPCalls counts tool calls to the bench MCP server excluding
	// platform_info: the upper bound on audit rows the run can have produced.
	MCPCalls int
	// SuccessfulMCPCalls counts those calls whose tool result was not an error:
	// each MUST have produced an audit row (it passed the gates and the handler
	// ran), so it is the lower bound the audit read-back enforces.
	SuccessfulMCPCalls int
	// ToolErrors counts bench MCP calls (excluding platform_info) whose result
	// was an error, for the report's tool-error column.
	ToolErrors int
	// SearchCalled is true when the agent called the search tool at least once.
	SearchCalled bool
	// ServerConnected is true when the init event reported the bench MCP server
	// as connected. A false value is a harness failure (claude never reached
	// the platform), surfaced by the caller.
	ServerConnected bool
	// ServerStatus is the raw status the init event reported for the bench
	// server, for a precise error message when it did not connect.
	ServerStatus string
	// IsError is the result event's error flag (claude itself failed).
	IsError bool
	// Subtype is the result event's subtype (e.g. "success", "error_max_turns").
	Subtype string
}

// streamLine is the union of the stream-json event shapes the parser reads. A
// single struct covers every line because the fields are disjoint across event
// types and JSON leaves absent fields at their zero value.
type streamLine struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype"`
	Message json.RawMessage `json:"message"`
	// Result is decoded raw and rendered leniently: claude emits it as a string
	// on success, but an error/variant terminal event may carry a non-string
	// value, and typing it as string would fail the whole-line decode and drop
	// the result event (making Parse report a spurious "no result event").
	Result     json.RawMessage `json:"result"`
	IsError    bool            `json:"is_error"`
	SessionID  string          `json:"session_id"`
	Usage      *usageRaw       `json:"usage"`
	MCPServers []mcpServer     `json:"mcp_servers"`
}

// mcpServer is one entry of the init event's server-status list.
type mcpServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// usageRaw is the token-usage subset the report records. The cache fields are
// present so a cached claude-cli run reports the same cost basis the anthropic
// adapter does; Claude Code emits them on the terminal result event's usage.
type usageRaw struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

// apiMessage is the Anthropic message envelope carried on assistant and user
// stream events.
type apiMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

// contentBlock is one content block; the fields used depend on Type.
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// serverToolPrefix builds the "mcp__<server>__" tool-name prefix Claude Code
// assigns to tools from the named MCP server.
func serverToolPrefix(server string) string {
	return "mcp__" + server + "__"
}

// parser threads the mutable accounting state across stream events so the
// per-event handlers stay small and the maps are not passed around explicitly.
type parser struct {
	res    Result
	server string
	prefix string
	// toolErr records each bench tool_use id's error flag once its paired
	// tool_result arrives, so success/error accounting survives interleaving.
	toolErr map[string]bool
	// toolDone marks bench tool_use ids whose paired tool_result was parsed. A
	// call with no result is indeterminate (it counts toward MCPCalls, the upper
	// bound, but NOT SuccessfulMCPCalls, the lower bound the audit read-back
	// enforces) so an unresolved tool_use never inflates the minimum.
	toolDone map[string]bool
	// toolIsBench marks tool_use ids that target the bench server (excluding
	// platform_info), the set the audit accounting is derived from.
	toolIsBench map[string]bool
	// infoIDs are the platform_info tool_use ids, so only those results are
	// mined for the dps_ handle and platform version.
	infoIDs   map[string]bool
	sawResult bool
}

// Parse reconstructs an episode Result from claude's stream-json output. The
// server name is the key used in the generated MCP config; prompt seeds the
// transcript's first user turn so the persisted transcript matches the loop
// path. A stream that carries no result event is a protocol error (claude
// produced no final answer), returned to the caller as a harness failure.
func Parse(server, prompt string, stdout []byte) (Result, error) {
	p := &parser{
		res:         Result{Transcript: []llm.Message{{Role: "user", Text: prompt}}},
		server:      server,
		prefix:      serverToolPrefix(server),
		toolErr:     map[string]bool{},
		toolDone:    map[string]bool{},
		toolIsBench: map[string]bool{},
		infoIDs:     map[string]bool{},
	}
	// Read whole lines with no length cap: a single stream-json event can inline
	// a large tool result (a wide query, a big S3 object), and a bufio.Scanner
	// token limit would abort the parse — discarding a completed, paid run as a
	// harness failure. bufio.Reader.ReadBytes grows to whatever the line needs.
	br := bufio.NewReader(bytes.NewReader(stdout))
	for {
		line, err := br.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var ev streamLine
			// A non-JSON line (a stray log write) is skipped rather than failing
			// the whole parse: the result event is what matters.
			if uerr := json.Unmarshal(trimmed, &ev); uerr == nil {
				p.event(ev)
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return Result{}, fmt.Errorf("read claude stream: %w", err)
			}
			break
		}
	}
	if !p.sawResult {
		return Result{}, fmt.Errorf("claude stream carried no result event (stderr may explain; %d bytes of stdout)", len(stdout))
	}
	p.tallyErrors()
	return p.res, nil
}

// event dispatches one decoded stream line to its handler.
func (p *parser) event(ev streamLine) {
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			p.applyInit(ev.MCPServers)
		}
	case "assistant":
		p.applyAssistant(ev.Message)
	case "user":
		p.applyToolResults(ev.Message)
	case "result":
		p.sawResult = true
		p.applyResult(ev)
	}
}

// applyInit records the bench server's connection status from the init event.
func (p *parser) applyInit(servers []mcpServer) {
	for _, s := range servers {
		if s.Name == p.server {
			p.res.ServerStatus = s.Status
			p.res.ServerConnected = s.Status == "connected"
			return
		}
	}
}

// applyAssistant appends an assistant turn to the transcript and records the
// bench MCP tool_use blocks it issued (which id belongs to which tool).
func (p *parser) applyAssistant(raw json.RawMessage) {
	var msg apiMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	out := llm.Message{Role: "assistant"}
	for _, b := range msg.Content {
		switch b.Type {
		case "text":
			appendText(&out, b.Text)
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, llm.ToolCall{ID: b.ID, Name: b.Name, Args: decodeArgs(b.Input)})
			p.recordToolUse(b.ID, b.Name)
		}
	}
	if out.Text != "" || len(out.ToolCalls) > 0 {
		p.res.Transcript = append(p.res.Transcript, out)
	}
}

// recordToolUse classifies one tool_use block by name: search flips the
// surfaced flag, platform_info is tracked for handle extraction but not counted,
// and any other bench tool is counted toward the audit lower/upper bounds.
func (p *parser) recordToolUse(id, name string) {
	if !strings.HasPrefix(name, p.prefix) {
		return
	}
	switch strings.TrimPrefix(name, p.prefix) {
	case searchToolName:
		p.res.SearchCalled = true
		p.toolIsBench[id] = true
		p.res.MCPCalls++
	case infoToolName:
		p.infoIDs[id] = true
	default:
		p.toolIsBench[id] = true
		p.res.MCPCalls++
	}
}

// appendText appends a non-empty text block onto a message, newline-separated.
func appendText(out *llm.Message, text string) {
	if text == "" {
		return
	}
	if out.Text != "" {
		out.Text += "\n"
	}
	out.Text += text
}

// applyToolResults appends a user turn carrying tool results and records each
// bench call's error flag, plus the platform handle/version from platform_info.
func (p *parser) applyToolResults(raw json.RawMessage) {
	var msg apiMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	out := llm.Message{Role: "user"}
	for _, b := range msg.Content {
		if b.Type != "tool_result" {
			if b.Type == "text" {
				appendText(&out, b.Text)
			}
			continue
		}
		text := extractResultText(b.Content)
		out.ToolResults = append(out.ToolResults, llm.ToolResult{CallID: b.ToolUseID, Text: text, IsError: b.IsError})
		p.recordToolResult(b.ToolUseID, text, b.IsError)
	}
	if out.Text != "" || len(out.ToolResults) > 0 {
		p.res.Transcript = append(p.res.Transcript, out)
	}
}

// recordToolResult folds one tool_result into the accounting: a bench call's
// error flag, and the handle/version from the first platform_info result.
func (p *parser) recordToolResult(id, text string, isError bool) {
	if p.toolIsBench[id] {
		p.toolErr[id] = isError
		p.toolDone[id] = true
	}
	if p.res.Handle == "" && p.infoIDs[id] {
		p.res.Handle, p.res.PlatformVersion = parseInfoResult(text)
	}
}

// applyResult records the terminal result event's answer, usage and flags.
func (p *parser) applyResult(ev streamLine) {
	p.res.FinalText = rawToText(ev.Result)
	p.res.IsError = ev.IsError
	p.res.Subtype = ev.Subtype
	if ev.SessionID != "" {
		p.res.ClaudeSessionID = ev.SessionID
	}
	if ev.Usage != nil {
		p.res.Usage = llm.Usage{
			InputTokens:              ev.Usage.InputTokens,
			OutputTokens:             ev.Usage.OutputTokens,
			CacheReadInputTokens:     ev.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: ev.Usage.CacheCreationInputTokens,
		}
	}
}

// tallyErrors folds the per-call error flags into success and error counts:
// every non-error bench call is a confirmed audit row (the lower bound), and
// error calls are only the upper bound (a gate refusal leaves no row, a handler
// error does).
func (p *parser) tallyErrors() {
	for id := range p.toolIsBench {
		if !p.toolDone[id] {
			// No paired result: indeterminate. Already counted in MCPCalls (the
			// upper bound); must not enter the confirmed-success lower bound.
			continue
		}
		if p.toolErr[id] {
			p.res.ToolErrors++
		} else {
			p.res.SuccessfulMCPCalls++
		}
	}
}

// rawToText renders a raw JSON value as text: a JSON string is unquoted, any
// other shape (object, null, absent) is returned as its trimmed raw bytes so a
// non-string result still yields something for the graders and the transcript.
func rawToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// decodeArgs unmarshals a tool_use input object, tolerating an absent or
// malformed value (recorded as empty args, matching the anthropic adapter).
func decodeArgs(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return map[string]any{}
	}
	return args
}

// extractResultText renders a tool_result's content, which is either a JSON
// string or an array of content blocks, into flat text.
func extractResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// infoPayload is the platform_info result subset the harness reads.
type infoPayload struct {
	SessionID string `json:"session_id"`
	Version   string `json:"version"`
}

// parseInfoResult extracts the dps_ handle and platform version from a
// platform_info tool-result text. The result is JSON (possibly wrapped in other
// prose); a leading-brace parse covers the common structured case.
func parseInfoResult(text string) (handle, version string) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") {
		return "", ""
	}
	var p infoPayload
	if err := json.Unmarshal([]byte(trimmed), &p); err != nil {
		return "", ""
	}
	return p.SessionID, p.Version
}
