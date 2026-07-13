package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic is an Adapter over the Anthropic Messages API via the official Go
// SDK. It sends no sampling parameters (removed on current models); run-to-run
// variance is handled by the harness's k-repeats and pass^k reporting, per the
// benchmark design. Thinking configuration is omitted so the request shape is
// valid across the full current model range.
type Anthropic struct {
	client    anthropic.Client
	model     string
	maxTokens int64
}

// NewAnthropic builds an adapter for the given model. The API key is read from
// ANTHROPIC_API_KEY (SDK default); a missing key is an immediate error so a
// misconfigured run fails before any session is minted.
func NewAnthropic(model string, maxTokens int64, timeout time.Duration) (*Anthropic, error) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil, errors.New("ANTHROPIC_API_KEY is not set; required for -llm anthropic")
	}
	if model == "" {
		return nil, errors.New("model must not be empty")
	}
	return &Anthropic{
		client:    anthropic.NewClient(option.WithRequestTimeout(timeout)),
		model:     model,
		maxTokens: maxTokens,
	}, nil
}

// Model implements Adapter.
func (a *Anthropic) Model() string { return a.model }

// Complete implements Adapter with one Messages API call. The SDK retries
// 429/5xx with backoff (default max_retries).
func (a *Anthropic) Complete(ctx context.Context, system string, msgs []Message, tools []ToolDef) (Message, Usage, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: a.maxTokens,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	apiTools, err := toAPITools(tools)
	if err != nil {
		return Message{}, Usage{}, err
	}
	params.Tools = apiTools
	apiMsgs, err := toAPIMessages(msgs)
	if err != nil {
		return Message{}, Usage{}, err
	}
	params.Messages = apiMsgs

	resp, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("anthropic messages: %w", err)
	}
	usage := Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return Message{}, usage, errors.New("anthropic: request refused (stop_reason refusal)")
	}
	return fromAPIContent(resp), usage, nil
}

// toAPITools converts ToolDefs, passing the MCP-provided JSON Schema through
// verbatim so the model sees exactly what the platform advertises.
func toAPITools(tools []ToolDef) ([]anthropic.ToolUnionParam, error) {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		var schema struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		if len(t.InputSchema) > 0 {
			if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
				return nil, fmt.Errorf("tool %s: parse input schema: %w", t.Name, err)
			}
		}
		tp := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: schema.Properties,
				Required:   schema.Required,
			},
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tp})
	}
	return out, nil
}

// toAPIMessages converts the provider-agnostic transcript to API params.
func toAPIMessages(msgs []Message) ([]anthropic.MessageParam, error) {
	out := make([]anthropic.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, anthropic.NewUserMessage(userBlocks(m)...))
		case "assistant":
			blocks, err := assistantBlocks(m)
			if err != nil {
				return nil, err
			}
			out = append(out, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleAssistant,
				Content: blocks,
			})
		default:
			return nil, fmt.Errorf("unsupported transcript role %q", m.Role)
		}
	}
	return out, nil
}

// userBlocks renders a user turn: tool results first (answering the preceding
// assistant tool calls), then any text.
func userBlocks(m Message) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.ToolResults)+1)
	for _, r := range m.ToolResults {
		blocks = append(blocks, anthropic.NewToolResultBlock(r.CallID, r.Text, r.IsError))
	}
	if m.Text != "" {
		blocks = append(blocks, anthropic.NewTextBlock(m.Text))
	}
	return blocks
}

// assistantBlocks renders an assistant turn with text and tool_use blocks.
func assistantBlocks(m Message) ([]anthropic.ContentBlockParamUnion, error) {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.ToolCalls)+1)
	if m.Text != "" {
		blocks = append(blocks, anthropic.NewTextBlock(m.Text))
	}
	for _, c := range m.ToolCalls {
		raw, err := json.Marshal(c.Args)
		if err != nil {
			return nil, fmt.Errorf("marshal tool call args for %s: %w", c.Name, err)
		}
		tu := anthropic.ToolUseBlockParam{ID: c.ID, Name: c.Name, Input: json.RawMessage(raw)}
		blocks = append(blocks, anthropic.ContentBlockParamUnion{OfToolUse: &tu})
	}
	return blocks, nil
}

// fromAPIContent converts a response into the transcript model.
func fromAPIContent(resp *anthropic.Message) Message {
	out := Message{Role: "assistant"}
	for _, block := range resp.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			if out.Text != "" {
				out.Text += "\n"
			}
			out.Text += v.Text
		case anthropic.ToolUseBlock:
			var args map[string]any
			if err := json.Unmarshal([]byte(v.JSON.Input.Raw()), &args); err != nil {
				args = map[string]any{}
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: v.ID, Name: v.Name, Args: args})
		}
	}
	return out
}
