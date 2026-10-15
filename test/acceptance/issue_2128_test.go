//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Acceptance for #2128: none of the platform's own descriptions or
// instructions tells an agent to record a query's answer; the capture and the
// review guidance both state the rule and its reason (knowledge is read by
// personas that cannot query every source); and the capture guidance's
// examples hold no derived statistic. Everything is read as an agent reads it:
// platform_info's agent instructions, tools/list, prompts/get and fetch of the
// built-in page.
//
// Wire forms: platform_info takes an empty object or absent params, both sent;
// fetch's reference is a string; tools/list and prompts/get take no tool
// arguments.

// instructions2128 reads platform_info's agent instructions.
func instructions2128(t *testing.T, c *client, args map[string]any) string {
	t.Helper()
	res, err := c.session.CallTool(c.ctx, &mcp.CallToolParams{Name: "platform_info", Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("platform_info: %v %s", err, firstText(res))
	}
	var out struct {
		AgentInstructions string `json:"agent_instructions"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &out); err != nil {
		t.Fatalf("platform_info is not JSON: %v", err)
	}
	return out.AgentInstructions
}

// prompt2128 reads a prompt's text.
func prompt2128(t *testing.T, c *client, name string) string {
	t.Helper()
	res, err := c.session.GetPrompt(c.ctx, &mcp.GetPromptParams{Name: name})
	if err != nil {
		t.Fatalf("prompts/get %s: %v", name, err)
	}
	var b strings.Builder
	for _, m := range res.Messages {
		if tc, ok := m.Content.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestIssue2128_TheInstructionsAskForTheMethodNotTheResult: the agent
// instructions, in both forms platform_info admits, state the rule and its
// reason and no longer ask for the answer.
func TestIssue2128_TheInstructionsAskForTheMethodNotTheResult(t *testing.T) {
	for name, args := range map[string]map[string]any{"absent params": nil, "empty object": {}} {
		t.Run(name, func(t *testing.T) {
			text := instructions2128(t, connect(t), args)
			for _, want := range []string{"never the data", "cannot query every source", "row values", "`memory_capture` `sources`"} {
				if !strings.Contains(text, want) {
					t.Errorf("the agent instructions do not say %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "including when the answer only went into the conversation") {
				t.Errorf("the agent instructions still ask for the answer:\n%s", text)
			}
		})
	}
}

// TestIssue2128_NoToolDescriptionAsksForTheAnswer: across every tool the
// platform lists, no description asks for what a query answers; the query
// tools' capture route asks for what the query computes and not its values,
// memory_capture says facts about data and never results, on the tool and on
// content, and apply_knowledge has the reviewer strip data.
func TestIssue2128_NoToolDescriptionAsksForTheAnswer(t *testing.T) {
	c := connect(t)
	byName := map[string]*mcp.Tool{}
	for _, tl := range c.tools() {
		byName[tl.Name] = tl
		if strings.Contains(tl.Description, "what the query answers") {
			t.Errorf("%s still asks for what the query answers", tl.Name)
		}
	}
	for _, name := range []string{"trino_query", "api_invoke_endpoint", "graphql_query"} {
		tl := byName[name]
		if tl == nil {
			continue
		}
		if !strings.Contains(tl.Description, "not the values it returned") {
			t.Errorf("%s's capture route does not exclude the values: %s", name, tl.Description)
		}
	}
	mc := byName["memory_capture"]
	if mc == nil {
		t.Fatal("memory_capture is not listed")
	}
	if !strings.Contains(mc.Description, "never query results or row values") {
		t.Errorf("memory_capture does not exclude results: %s", mc.Description)
	}
	schema, _ := json.Marshal(mc.InputSchema)
	if !strings.Contains(string(schema), "never query results or row values") {
		t.Errorf("memory_capture's content does not exclude results: %s", schema)
	}
	ak := byName["apply_knowledge"]
	if ak == nil {
		t.Fatal("apply_knowledge is not listed")
	}
	if !strings.Contains(ak.Description, "strip data") || !strings.Contains(ak.Description, "cannot query its sources") {
		t.Errorf("apply_knowledge does not have the reviewer strip data: %s", ak.Description)
	}
	schema, _ = json.Marshal(ak.InputSchema)
	if !strings.Contains(string(schema), "never figures or row values") {
		t.Errorf("apply_knowledge's page body does not exclude figures: %s", schema)
	}
}

// TestIssue2128_TheGuidancePromptsStateTheRule: the capture guidance excludes
// results and holds no derived statistic as an example; the review guidance
// strips data before applying.
func TestIssue2128_TheGuidancePromptsStateTheRule(t *testing.T) {
	c := connect(t)
	capture := prompt2128(t, c, "knowledge_capture_guidance")
	if !strings.Contains(capture, "Query results, metrics, aggregates") || !strings.Contains(capture, "cannot query every source") {
		t.Errorf("the capture guidance does not exclude results with its reason:\n%s", capture)
	}
	if strings.Contains(capture, "23%") {
		t.Errorf("the capture guidance still gives a derived statistic as its example")
	}
	review := prompt2128(t, c, "knowledge_apply_guidance")
	if !strings.Contains(review, "**Strip data.**") || !strings.Contains(review, "cannot query its sources") {
		t.Errorf("the review guidance does not strip data with its reason:\n%s", review)
	}
}

// TestIssue2128_TheBuiltInPageSaysWhatACaptureMustNotHold: the built-in page
// on the capture loop carries the rule with a before and after.
func TestIssue2128_TheBuiltInPageSaysWhatACaptureMustNotHold(t *testing.T) {
	c := connect(t)
	res, text, err := c.callRaw("fetch", map[string]any{
		"reference": "mcp:knowledge_page:platform-provenance-and-the-capture-loop",
		"purpose":   "Acceptance for #2128: what a capture must not hold.",
	})
	if err != nil || res.IsError {
		t.Fatalf("fetch: %v %s", err, text)
	}
	for _, want := range []string{"What a capture holds and what it must not", "Before:", "After:"} {
		if !strings.Contains(text, want) {
			t.Errorf("the page does not carry %q", want)
		}
	}
}
