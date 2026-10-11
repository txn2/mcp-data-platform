package mcptask

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/wirejson"
)

// Result discriminators the extension's answers carry.
const (
	resultTypeTask     = "task"
	resultTypeComplete = "complete"
)

// wireTask is the Task shape every answer about a task carries.
type wireTask struct {
	TaskID        string `json:"taskId"`
	Status        string `json:"status"`
	StatusMessage string `json:"statusMessage,omitempty"`
	CreatedAt     string `json:"createdAt"`
	LastUpdatedAt string `json:"lastUpdatedAt"`
	// TTLMs is null for a task kept without limit, so it is always written.
	TTLMs          *int64 `json:"ttlMs"`
	PollIntervalMs int64  `json:"pollIntervalMs,omitempty"`
}

// CreateResult is the answer to a tools/call that became a task: the task's
// seed state under resultType "task".
type CreateResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
	wireTask
}

// GetResult is the answer to tasks/get: the task with its outcome inlined
// once it has one.
type GetResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
	wireTask
	Result json.RawMessage `json:"result,omitempty"`
	Error  *jsonrpc.Error  `json:"error,omitempty"`
}

// AckResult is the empty acknowledgement tasks/update and tasks/cancel answer
// with.
type AckResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
}

// NewCreateResult renders a task's seed state as the tools/call answer.
func NewCreateResult(s *Snapshot) *CreateResult {
	return &CreateResult{ResultType: resultTypeTask, wireTask: taskOf(s)}
}

// newGetResult renders a task as tasks/get answers it.
func newGetResult(s *Snapshot) *GetResult {
	out := &GetResult{ResultType: resultTypeComplete, wireTask: taskOf(s)}
	switch s.Status {
	case StatusCompleted:
		out.Result = s.Result
	case StatusFailed:
		out.Error = s.Error
		if out.Error == nil {
			out.Error = internalError(s.StatusMessage)
		}
	}
	return out
}

func newAck() *AckResult { return &AckResult{ResultType: resultTypeComplete} }

// taskOf renders the fields every answer about a task shares.
func taskOf(s *Snapshot) wireTask {
	t := wireTask{
		TaskID:         s.ID,
		Status:         s.Status,
		StatusMessage:  s.StatusMessage,
		CreatedAt:      s.CreatedAt.UTC().Format(time.RFC3339Nano),
		LastUpdatedAt:  s.UpdatedAt.UTC().Format(time.RFC3339Nano),
		PollIntervalMs: s.PollInterval.Milliseconds(),
	}
	if s.TTL > 0 {
		ms := s.TTL.Milliseconds()
		t.TTLMs = &ms
	}
	return t
}

// IsTask reports whether a tools/call answer is a task handle rather than the
// tool's result.
func IsTask(res mcp.Result) bool {
	_, ok := res.(*CreateResult)
	return ok
}

// EncodeResult renders a tool result in the wire form a client reads, the
// form tasks/get inlines. Structured content still held as a Go value is
// encoded by the platform's response encoder first, as the result-type layer
// does for a result answered directly (#1832), so an empty list reads [] here
// too.
func EncodeResult(res *mcp.CallToolResult) (json.RawMessage, error) {
	if res == nil {
		return nil, fmt.Errorf("encoding a task result: %w", errNoResult)
	}
	out := *res
	if out.StructuredContent != nil {
		if _, encoded := out.StructuredContent.(json.RawMessage); !encoded {
			raw, err := wirejson.Marshal(out.StructuredContent)
			if err != nil {
				return nil, fmt.Errorf("encoding a task result's structured content: %w", err)
			}
			out.StructuredContent = json.RawMessage(raw)
		}
	}
	if out.Content == nil {
		out.Content = []mcp.Content{}
	}
	raw, err := json.Marshal(&out)
	if err != nil {
		return nil, fmt.Errorf("encoding a task result: %w", err)
	}
	return raw, nil
}
