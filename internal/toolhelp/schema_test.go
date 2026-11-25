package toolhelp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

func searchSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"intent"},
		"properties": map[string]any{
			"intent":  map[string]any{"type": "string"},
			"limit":   map[string]any{"type": "integer"},
			"dry_run": map[string]any{"type": "boolean"},
			"command": map[string]any{"type": "string", "enum": []any{"list", "get", "help"}},
		},
	}
}

func TestDescribeArguments(t *testing.T) {
	tests := []struct {
		name string
		args string
		want []string
		not  []string
	}{
		{
			name: "another tool's name for the required parameter",
			args: `{"query":"revenue"}`,
			want: []string{`search does not take "query"; did you mean "intent"?`, "intent (string, required)", "limit (integer)"},
		},
		{
			name: "a typo is matched by edit distance",
			args: `{"intent":"x","limt":5}`,
			want: []string{`did you mean "limit"?`},
		},
		{
			name: "a string where a boolean is declared",
			args: `{"intent":"x","dry_run":"true"}`,
			want: []string{"dry_run must be boolean; the call sent string."},
		},
		{
			name: "an unknown command lists the valid ones",
			args: `{"intent":"x","command":"assist"}`,
			want: []string{`"assist" is not one of command's values; it takes one of: list, get, help.`},
		},
		{
			name: "an integer satisfies number and a matching value is not reported",
			args: `{"intent":"x","limit":3}`,
			not:  []string{"must be", "does not take"},
		},
		{
			name: "no closest name when nothing is near and nothing required is missing",
			args: `{"intent":"x","zzz":1}`,
			want: []string{`search does not take "zzz".`},
			not:  []string{"did you mean"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, ok := describeArguments("search", searchSchema(), json.RawMessage(tt.args))
			if !ok {
				t.Fatal("describeArguments reported no properties to compare against")
			}
			msg := r.message()
			for _, w := range tt.want {
				if !strings.Contains(msg, w) {
					t.Errorf("message lacks %q:\n%s", w, msg)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(msg, n) {
					t.Errorf("message holds %q:\n%s", n, msg)
				}
			}
		})
	}
}

func TestDescribeArguments_NoProperties(t *testing.T) {
	if _, ok := describeArguments("x", map[string]any{"type": "object"}, nil); ok {
		t.Error("a schema with no properties was described")
	}
	if _, ok := describeArguments("x", nil, nil); ok {
		t.Error("a missing schema was described")
	}
}

func TestRefusalMiddleware_NamesAcceptedParameters(t *testing.T) {
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return middleware.BuildErrorResult(middleware.ClientInputError(codeInvalidArguments,
			`validating "arguments": validating root: unexpected additional properties ["query"]`, "fix it")), nil
	}
	lookup := func(_ context.Context, tool string) map[string]any {
		if tool == "search" {
			return searchSchema()
		}
		return nil
	}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "search", Arguments: json.RawMessage(`{"query":"revenue"}`)}}

	got, err := RefusalMiddleware(lookup)(leaf)(context.Background(), methodToolsCall, req)
	if err != nil {
		t.Fatal(err)
	}
	pe := platformError(t, got)
	if pe == nil || pe.Code != codeInvalidArguments || pe.Category != middleware.ErrCategoryClientInput {
		t.Fatalf("code/category changed: %+v", pe)
	}
	if !strings.Contains(pe.Message, `did you mean "intent"?`) || !strings.Contains(pe.Message, "intent (string, required)") {
		t.Errorf("refusal does not name the accepted parameter: %s", pe.Message)
	}

	// A tool whose schema is unknown keeps the verdict it had.
	req.Params.Name = "other"
	got, _ = RefusalMiddleware(lookup)(leaf)(context.Background(), methodToolsCall, req)
	if pe := platformError(t, got); pe == nil || !strings.Contains(pe.Message, "unexpected additional properties") {
		t.Errorf("an unknown tool's refusal was rewritten: %+v", pe)
	}

	// Any other result passes through untouched.
	ok := func(context.Context, string, mcp.Request) (mcp.Result, error) { return &mcp.CallToolResult{}, nil }
	if res, _ := RefusalMiddleware(lookup)(ok)(context.Background(), methodToolsCall, req); toolResult(t, res).IsError {
		t.Error("a success was rewritten")
	}
	other := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return middleware.BuildErrorResult(middleware.ClientInputError("other_code", "m", "h")), nil
	}
	if res, _ := RefusalMiddleware(lookup)(other)(context.Background(), methodToolsCall, req); !strings.Contains(toolResult(t, res).GetError().Error(), "m") {
		t.Error("another refusal was rewritten")
	}
	if name, args := callNameAndArgs(nil); name != "" || args != nil {
		t.Error("a nil request named a tool")
	}
}

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"", "", 0}, {"abc", "abc", 0}, {"limt", "limit", 1}, {"query", "intent", 5}, {"", "ab", 2}} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestJSONTypeAndDeclaredTypes(t *testing.T) {
	for v, want := range map[any]string{nil: "null", true: "boolean", 1.0: "integer", 1.5: "number", "s": "string"} {
		if got := jsonType(v); got != want {
			t.Errorf("jsonType(%v) = %s, want %s", v, got, want)
		}
	}
	if jsonType([]any{}) != "array" || jsonType(map[string]any{}) != "object" || jsonType(struct{}{}) != "unknown" {
		t.Error("jsonType misnamed a composite")
	}
	if got := declaredTypes(map[string]any{"type": []any{"string", "null"}}); len(got) != 1 || got[0] != "string" {
		t.Errorf("declaredTypes = %v", got)
	}
	if typeOf(map[string]any{}) != "any" {
		t.Error("an untyped property is not any")
	}
	if m := mismatch("x", map[string]any{}, 1.0); m != "" {
		t.Errorf("an untyped property reported a mismatch: %s", m)
	}
}

// toolResult is a result as a tool result, failing the test otherwise.
func toolResult(t *testing.T, res mcp.Result) *mcp.CallToolResult {
	t.Helper()
	ctr, ok := res.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("not a tool result: %T", res)
	}
	return ctr
}

// platformError is a tool result's platform error, nil when it carries none.
func platformError(t *testing.T, res mcp.Result) *middleware.PlatformError {
	t.Helper()
	pe, ok := errors.AsType[*middleware.PlatformError](toolResult(t, res).GetError())
	if !ok {
		return nil
	}
	return pe
}

// TestClosestNameFromDescriptions: with no near name and no single missing
// required parameter, the one parameter whose description names the unknown
// word is suggested; two such parameters suggest neither.
func TestClosestNameFromDescriptions(t *testing.T) {
	props := map[string]any{
		"intent":      map[string]any{"type": "string", "description": "Your query: what you are looking for."},
		"entity_urns": map[string]any{"type": "array", "description": "References to read."},
	}
	if got := closestName("query", props, nil); got != "intent" {
		t.Errorf("closestName(query) = %q, want intent", got)
	}
	props["context"] = map[string]any{"type": "string", "description": "The query's surroundings."}
	if got := closestName("query", props, nil); got != "" {
		t.Errorf("two parameters naming the word suggested %q", got)
	}
	if got := closestName("banana", props, nil); got != "" {
		t.Errorf("an unrelated word suggested %q", got)
	}
}
