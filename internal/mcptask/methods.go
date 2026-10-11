package mcptask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// taskParams is the params object of every tasks/* request.
type taskParams struct {
	mcp.ParamsBase
	TaskID string `json:"taskId"`
	// InputResponses is tasks/update's; no task the platform runs asks for
	// input, so it is read and ignored, as the extension directs for a
	// response to a request that is not outstanding.
	InputResponses map[string]json.RawMessage `json:"inputResponses,omitempty"`
}

// Register adds the extension's methods to server, answered from sources.
func Register(server *mcp.Server, sources *Sources) error {
	h := handlers{sources: sources}
	for method, fn := range map[string]func(context.Context, *mcp.ServerSession, *taskParams) (mcp.Result, error){
		MethodGet:    h.get,
		MethodUpdate: h.update,
		MethodCancel: h.cancel,
	} {
		if err := mcp.AddReceivingCustomMethod(server, method, fn); err != nil {
			return fmt.Errorf("registering %s: %w", method, err)
		}
	}
	return nil
}

type handlers struct {
	sources *Sources
}

// get answers tasks/get with the task, its outcome inlined once it has one.
func (h handlers) get(ctx context.Context, _ *mcp.ServerSession, p *taskParams) (mcp.Result, error) {
	snap, err := h.read(ctx, p)
	if err != nil {
		return nil, err
	}
	return newGetResult(snap), nil
}

// update acknowledges tasks/update for a task the caller may follow.
func (h handlers) update(ctx context.Context, _ *mcp.ServerSession, p *taskParams) (mcp.Result, error) {
	if _, err := h.read(ctx, p); err != nil {
		return nil, err
	}
	return newAck(), nil
}

// cancel asks the task's Source to stop it. The acknowledgement says only that
// the request was taken: the task reports its cancel once its work has stopped,
// or ends another way if it finished first.
func (h handlers) cancel(ctx context.Context, _ *mcp.ServerSession, p *taskParams) (mcp.Result, error) {
	src := h.sources.For(taskID(p))
	if src == nil {
		return nil, notFound()
	}
	if err := src.Cancel(ctx, p.TaskID); err != nil {
		return nil, refusal(ctx, p.TaskID, err)
	}
	return newAck(), nil
}

// read resolves a request's task through its Source.
func (h handlers) read(ctx context.Context, p *taskParams) (*Snapshot, error) {
	src := h.sources.For(taskID(p))
	if src == nil {
		return nil, notFound()
	}
	snap, err := src.Get(ctx, p.TaskID)
	if err != nil {
		return nil, refusal(ctx, p.TaskID, err)
	}
	return snap, nil
}

// refusal renders a Source's error as the JSON-RPC error the extension names
// for it.
func refusal(ctx context.Context, id string, err error) error {
	if errors.Is(err, ErrNotFound) {
		return notFound()
	}
	slog.ErrorContext(ctx, "a task could not be read", "task_id", logsan.SanitizeForLog(id),
		"error", logsan.SanitizeForLog(err.Error()))
	return internalError("Failed to retrieve task")
}

func taskID(p *taskParams) string {
	if p == nil {
		return ""
	}
	return p.TaskID
}
