// Package mcptask is the platform's implementation of the MCP Tasks extension
// (io.modelcontextprotocol/tasks, protocol revision 2026-07-28; #2009).
//
// A client that declares the extension on a tools/call may be answered with a
// task handle (resultType "task") instead of the tool's result, and follows the
// work with tasks/get and tasks/cancel. The SDK the platform is built on does
// not implement the extension, so this package carries it at the protocol
// level: the handle and the tasks/* answers are its own result types, the
// tasks/* methods are custom methods on the server, and the tool calls that
// become tasks are chosen by a receiving middleware.
//
// A task is held by a Source, one per kind of work, addressed by the prefix of
// its id: a managed-script run is a task whose id is the run id, and a tool
// call executed apart from its request (an export) is a task kept in its own
// table. Nothing here stores a task; it routes each tasks/* request to the
// Source that owns the id and renders what the Source reports.
//
// The extension's spec:
// https://github.com/modelcontextprotocol/ext-tasks/blob/main/specification/2026-07-28/tasks.md
package mcptask

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ExtensionID is the extension's identifier, the key a client and the server
// declare it under.
const ExtensionID = "io.modelcontextprotocol/tasks"

// The extension's methods.
const (
	MethodGet    = "tasks/get"
	MethodUpdate = "tasks/update"
	MethodCancel = "tasks/cancel"
)

// methodToolsCall is the one request the extension augments.
const methodToolsCall = "tools/call"

// CodeMissingCapability is the JSON-RPC error a request that needs the
// extension is refused with when the client did not declare it.
const CodeMissingCapability = -32021

// metaKeyClientCapabilities is where a 2026-07-28 request carries the
// capabilities its client declares for that request.
const metaKeyClientCapabilities = "io.modelcontextprotocol/clientCapabilities"

// Task statuses.
const (
	StatusWorking   = "working"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled" //nolint:misspell // the extension's wire value
)

// ErrNotFound is what a Source answers for an id it does not hold, and for an
// id the caller may not read: the two are one answer, so a task's existence is
// never revealed to anybody but whoever may follow it.
var ErrNotFound = errors.New("task not found")

// errNoResult is a completed task reported without the result it completed
// with.
var errNoResult = errors.New("no result")

// Snapshot is a task's state as its Source reports it.
type Snapshot struct {
	ID            string
	Status        string
	StatusMessage string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// TTL is how long after CreatedAt the task may be discarded; zero is
	// unlimited.
	TTL time.Duration
	// PollInterval is how often a client should ask again.
	PollInterval time.Duration
	// Result is the tool's result on a completed task, in its wire form
	// (EncodeResult).
	Result json.RawMessage
	// Error is the JSON-RPC error on a failed task.
	Error *jsonrpc.Error
}

// Terminal reports whether the task has ended.
func (s *Snapshot) Terminal() bool {
	return s.Status == StatusCompleted || s.Status == StatusFailed || s.Status == StatusCancelled
}

// Source holds one kind of task. Both calls run with the caller's
// PlatformContext on ctx and apply the Source's own reading rule, answering
// ErrNotFound for a task the caller may not follow.
type Source interface {
	// Prefix is the id prefix of the tasks this Source holds.
	Prefix() string
	// Get reports a task's state.
	Get(ctx context.Context, id string) (*Snapshot, error)
	// Cancel asks for the task to stop. It is cooperative: the task may
	// still end another way if it finished first.
	Cancel(ctx context.Context, id string) error
}

// Sources routes a task id to the Source that holds it.
type Sources struct {
	sources []Source
}

// NewSources builds the routing over sources; a nil Source is skipped.
func NewSources(sources ...Source) *Sources {
	s := &Sources{}
	for _, src := range sources {
		if src != nil {
			s.sources = append(s.sources, src)
		}
	}
	return s
}

// Add registers one more Source; a nil Source is skipped.
func (s *Sources) Add(src Source) {
	if src != nil {
		s.sources = append(s.sources, src)
	}
}

// For returns the Source holding id, or nil.
func (s *Sources) For(id string) Source {
	if s == nil {
		return nil
	}
	for _, src := range s.sources {
		if strings.HasPrefix(id, src.Prefix()) {
			return src
		}
	}
	return nil
}

// Declared reports whether the request itself declares the extension in its
// per-request client capabilities. Only the request counts: the spec forbids
// answering with a task a request that did not declare it, whatever the
// session declared before.
func Declared(req mcp.Request) bool {
	if req == nil {
		return false
	}
	params := req.GetParams()
	// A request sent without params holds a typed nil, whose _meta cannot
	// be read.
	if params == nil || reflect.ValueOf(params).IsNil() {
		return false
	}
	caps, ok := params.GetMeta()[metaKeyClientCapabilities]
	if !ok {
		return false
	}
	raw, err := json.Marshal(caps)
	if err != nil {
		return false
	}
	var decoded struct {
		Extensions map[string]json.RawMessage `json:"extensions"`
	}
	if json.Unmarshal(raw, &decoded) != nil {
		return false
	}
	_, declared := decoded.Extensions[ExtensionID]
	return declared
}

// missingCapability is the refusal a request needing the extension gets from
// a client that did not declare it.
func missingCapability() *jsonrpc.Error {
	data, _ := json.Marshal(map[string]any{
		"requiredCapabilities": map[string]any{
			"extensions": map[string]any{ExtensionID: map[string]any{}},
		},
	})
	return &jsonrpc.Error{Code: CodeMissingCapability, Message: "Missing required client capability", Data: data}
}

// notFound is the refusal for an id no Source holds or the caller may not
// follow.
func notFound() *jsonrpc.Error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Failed to retrieve task: Task not found"}
}

// internalError is the refusal for a Source that could not answer.
func internalError(msg string) *jsonrpc.Error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: msg}
}
