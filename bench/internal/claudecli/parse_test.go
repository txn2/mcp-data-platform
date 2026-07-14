package claudecli

import (
	"strings"
	"testing"
)

// sampleStream is a representative claude -p stream-json transcript for an
// episode that mints a handle via platform_info, calls search (success), calls
// trino_query (success), calls trino_query again (error), and answers. It
// exercises handle/version extraction, MCP-call accounting, success/error
// classification, search detection, and server-connection status.
const sampleStream = `
{"type":"system","subtype":"init","session_id":"cc-1","mcp_servers":[{"name":"bench","status":"connected"},{"name":"other","status":"failed"}],"model":"claude-sonnet-5"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t0","name":"mcp__bench__platform_info","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t0","is_error":false,"content":[{"type":"text","text":"{\"session_id\":\"dps_abc123\",\"version\":\"v9.9.9\"}"}]}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me search."},{"type":"tool_use","id":"t1","name":"mcp__bench__search","input":{"query":"revenue"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"found tables"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"mcp__bench__trino_query","input":{"sql":"SELECT 1"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","is_error":false,"content":"42"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"mcp__bench__trino_query","input":{"sql":"SELECT bad"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t3","is_error":true,"content":"syntax error"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"FINAL ANSWER: 42"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: 42","session_id":"cc-1","num_turns":5,"usage":{"input_tokens":100,"output_tokens":20}}
`

func TestParseFullEpisode(t *testing.T) {
	res, err := Parse("bench", "What is the revenue?", []byte(sampleStream))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.FinalText != "FINAL ANSWER: 42" {
		t.Errorf("FinalText = %q", res.FinalText)
	}
	if res.Handle != "dps_abc123" {
		t.Errorf("Handle = %q, want dps_abc123", res.Handle)
	}
	if res.PlatformVersion != "v9.9.9" {
		t.Errorf("PlatformVersion = %q, want v9.9.9", res.PlatformVersion)
	}
	if res.ClaudeSessionID != "cc-1" {
		t.Errorf("ClaudeSessionID = %q", res.ClaudeSessionID)
	}
	// platform_info excluded; search + two trino_query = 3 MCP calls.
	if res.MCPCalls != 3 {
		t.Errorf("MCPCalls = %d, want 3", res.MCPCalls)
	}
	// search + first trino_query succeeded; second errored.
	if res.SuccessfulMCPCalls != 2 {
		t.Errorf("SuccessfulMCPCalls = %d, want 2", res.SuccessfulMCPCalls)
	}
	if res.ToolErrors != 1 {
		t.Errorf("ToolErrors = %d, want 1", res.ToolErrors)
	}
	if !res.SearchCalled {
		t.Error("SearchCalled = false, want true")
	}
	if !res.ServerConnected {
		t.Errorf("ServerConnected = false, status %q", res.ServerStatus)
	}
	if res.Usage.InputTokens != 100 || res.Usage.OutputTokens != 20 {
		t.Errorf("Usage = %+v", res.Usage)
	}
	// Transcript: seeded user prompt + assistant/user turns. First turn is the
	// prompt; it must contain the tool-use and tool-result turns for review.
	if len(res.Transcript) < 6 {
		t.Errorf("Transcript has %d messages, want >= 6", len(res.Transcript))
	}
	if res.Transcript[0].Role != "user" || res.Transcript[0].Text != "What is the revenue?" {
		t.Errorf("Transcript[0] = %+v, want seeded prompt", res.Transcript[0])
	}
}

func TestParseNoResultEvent(t *testing.T) {
	// A truncated stream (process killed before the result event) is a harness
	// failure, not a silent empty answer.
	stream := `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}`
	_, err := Parse("bench", "q", []byte(stream))
	if err == nil {
		t.Fatal("expected error for stream with no result event")
	}
	if !strings.Contains(err.Error(), "no result event") {
		t.Errorf("error = %v, want 'no result event'", err)
	}
}

func TestParseServerNotConnected(t *testing.T) {
	stream := `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"failed"}]}
{"type":"result","subtype":"success","is_error":false,"result":"x","usage":{"input_tokens":1,"output_tokens":1}}`
	res, err := Parse("bench", "q", []byte(stream))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.ServerConnected {
		t.Error("ServerConnected = true, want false")
	}
	if res.ServerStatus != "failed" {
		t.Errorf("ServerStatus = %q, want failed", res.ServerStatus)
	}
}

func TestParseSkipsNonJSONLines(t *testing.T) {
	stream := `some stray log line
{"type":"result","subtype":"success","is_error":false,"result":"ok","usage":{"input_tokens":1,"output_tokens":1}}`
	res, err := Parse("bench", "q", []byte(stream))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.FinalText != "ok" {
		t.Errorf("FinalText = %q", res.FinalText)
	}
}

func TestParseStringToolResultContent(t *testing.T) {
	// tool_result.content may be a bare JSON string rather than a block array.
	stream := `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t0","name":"mcp__bench__platform_info","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t0","is_error":false,"content":"{\"session_id\":\"dps_x\",\"version\":\"v1\"}"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"ok","usage":{"input_tokens":1,"output_tokens":1}}`
	res, err := Parse("bench", "q", []byte(stream))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.Handle != "dps_x" || res.PlatformVersion != "v1" {
		t.Errorf("handle/version = %q/%q, want dps_x/v1", res.Handle, res.PlatformVersion)
	}
}

func TestDecodeArgsMalformed(t *testing.T) {
	if got := decodeArgs(nil); len(got) != 0 {
		t.Errorf("nil input = %v, want empty", got)
	}
	if got := decodeArgs([]byte("not json")); len(got) != 0 {
		t.Errorf("malformed input = %v, want empty", got)
	}
	if got := decodeArgs([]byte(`{"a":1}`)); got["a"] != float64(1) {
		t.Errorf("valid input = %v", got)
	}
}

func TestParseInfoResultNonJSON(t *testing.T) {
	if h, v := parseInfoResult("just prose, no braces"); h != "" || v != "" {
		t.Errorf("got %q/%q, want empty", h, v)
	}
	if h, v := parseInfoResult(`{"bad json`); h != "" || v != "" {
		t.Errorf("got %q/%q, want empty", h, v)
	}
}

func TestParseLargeLineNotCapped(t *testing.T) {
	// A single stream-json event whose tool_result inlines a >16MB payload must
	// parse (a bufio.Scanner token cap would abort and discard a paid run).
	big := strings.Repeat("x", 17*1024*1024)
	stream := `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"q1","name":"mcp__bench__trino_query","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"q1","is_error":false,"content":"` + big + `"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: ok","usage":{"input_tokens":1,"output_tokens":1}}`
	res, err := Parse("bench", "q", []byte(stream))
	if err != nil {
		t.Fatalf("Parse of large line: %v", err)
	}
	if res.FinalText != "FINAL ANSWER: ok" {
		t.Errorf("FinalText = %q", res.FinalText)
	}
	if res.MCPCalls != 1 || res.SuccessfulMCPCalls != 1 {
		t.Errorf("MCPCalls=%d SuccessfulMCPCalls=%d, want 1/1", res.MCPCalls, res.SuccessfulMCPCalls)
	}
}

func TestParseUnpairedToolUseNotCountedSuccessful(t *testing.T) {
	// A bench tool_use with no paired tool_result is indeterminate: it counts
	// toward MCPCalls (the upper bound) but must NOT inflate SuccessfulMCPCalls
	// (the lower bound the audit read-back enforces), or the run would fail with
	// a spurious missing-rows error.
	stream := `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"q1","name":"mcp__bench__trino_query","input":{}}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: x","usage":{"input_tokens":1,"output_tokens":1}}`
	res, err := Parse("bench", "q", []byte(stream))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.MCPCalls != 1 {
		t.Errorf("MCPCalls = %d, want 1 (call is in the upper bound)", res.MCPCalls)
	}
	if res.SuccessfulMCPCalls != 0 {
		t.Errorf("SuccessfulMCPCalls = %d, want 0 (unresolved call is indeterminate)", res.SuccessfulMCPCalls)
	}
	if res.ToolErrors != 0 {
		t.Errorf("ToolErrors = %d, want 0 (unresolved is not a known error)", res.ToolErrors)
	}
}

func TestParseNonStringResult(t *testing.T) {
	// A terminal result event whose `result` is not a bare string must still be
	// recognized as the result event (not dropped, which would report a spurious
	// "no result event").
	stream := `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"result","subtype":"error_during_execution","is_error":true,"result":{"detail":"boom"},"usage":{"input_tokens":1,"output_tokens":1}}`
	res, err := Parse("bench", "q", []byte(stream))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !res.IsError {
		t.Error("IsError = false, want true")
	}
	if !strings.Contains(res.FinalText, "boom") {
		t.Errorf("FinalText = %q, want the raw object text", res.FinalText)
	}
}

func TestParseIgnoresNonBenchTools(t *testing.T) {
	// A tool from a different MCP server (or a built-in) must not count toward
	// the bench audit accounting.
	stream := `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"b0","name":"Bash","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b0","is_error":false,"content":"x"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"ok","usage":{"input_tokens":1,"output_tokens":1}}`
	res, err := Parse("bench", "q", []byte(stream))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.MCPCalls != 0 || res.SuccessfulMCPCalls != 0 {
		t.Errorf("MCPCalls=%d SuccessfulMCPCalls=%d, want 0/0", res.MCPCalls, res.SuccessfulMCPCalls)
	}
}
