package apprtool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// store is an approval.Store in memory.
type store struct{ rows map[string]*approval.Request }

func (s *store) Insert(_ context.Context, r *approval.Request) error {
	r.Status = approval.StatusPending
	c := *r
	s.rows[r.ID] = &c
	return nil
}

func (s *store) Get(_ context.Context, id string) (*approval.Request, error) {
	r, ok := s.rows[id]
	if !ok {
		return nil, approval.ErrNotFound
	}
	c := *r
	return &c, nil
}

func (s *store) List(context.Context, approval.Filter) ([]approval.Request, error) {
	out := make([]approval.Request, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, *r)
	}
	return out, nil
}

func (s *store) Decide(_ context.Context, id string, d approval.Decision) (*approval.Request, error) {
	r, ok := s.rows[id]
	if !ok {
		return nil, approval.ErrNotFound
	}
	if r.Status != approval.StatusPending {
		return nil, approval.ErrDecided
	}
	at := d.At
	r.Status, r.DecidedBy, r.SelfApproved, r.DecidedAt = d.Status, d.By, d.SelfApproved, &at
	c := *r
	return &c, nil
}

func (*store) LatestApproved(context.Context, string, string) (*approval.Request, error) {
	return nil, approval.ErrNotFound
}

type scripts struct{}

var errNoScript = errors.New("no such script")

const form = "def form_spec(req):\n    return {}\n\ndef form_submit(req, values):\n    return None\n"

func (scripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	if id != "s1" {
		return nil, errNoScript
	}
	return &script.Script{ID: "s1", Name: "intake", OwnerEmail: "jane@example.com", Version: 1}, nil
}

func (scripts) GetByName(context.Context, string, string) (*script.Script, error) {
	return nil, errNoScript
}

func (scripts) GetVersion(_ context.Context, _ string, v int) (*script.Version, error) {
	if v != 1 {
		return nil, errNoScript
	}
	return &script.Version{Version: 1, Source: form}, nil
}

func as(email string, admin bool, source string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserEmail: email, IsAdmin: admin, Roles: []string{"r"}, Source: source,
	})
}

func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

// TestTool is #2494 through manage_approval: the owner submits, a
// non-administrator deciding is refused, an administrator approves, and a
// script run may read but not write.
func TestTool(t *testing.T) {
	tool := &Tool{
		Service: &approval.Service{Store: &store{rows: map[string]*approval.Request{}}, Scripts: scripts{}},
		Persona: func([]string) string { return "analyst" },
	}
	handle := func(ctx context.Context, in Input) *mcp.CallToolResult {
		t.Helper()
		res, _, err := tool.Handle(ctx, in)
		require.NoError(t, err)
		return res
	}
	res := handle(as("jane@example.com", false, ""), Input{Command: CmdSubmit, Script: "s1", Reason: "launch"})
	require.False(t, res.IsError, text(t, res))
	var submitted struct {
		Request approval.Request `json:"request"`
		Message string           `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(text(t, res)), &submitted))
	assert.Equal(t, "analyst", submitted.Request.RequesterPersona)
	assert.Contains(t, submitted.Message, "version 1 of intake")
	id := submitted.Request.ID

	res = handle(as("jane@example.com", false, ""), Input{Command: CmdDecide, ID: id, Decision: approval.DecisionApprove})
	require.True(t, res.IsError)
	assert.Contains(t, text(t, res), "administrators decide requests, including a request of your own")

	res = handle(as("jane@example.com", false, middleware.SourceScript), Input{Command: CmdSubmit, Script: "s1"})
	require.True(t, res.IsError)
	assert.Contains(t, text(t, res), "cannot be called from inside a script run")

	res = handle(as("admin@example.com", true, ""), Input{Command: CmdDecide, ID: id, Decision: approval.DecisionApprove})
	require.False(t, res.IsError, text(t, res))
	assert.Contains(t, text(t, res), `"status":"approved"`)

	res = handle(as("admin@example.com", true, ""), Input{Command: CmdDecide, ID: id, Decision: approval.DecisionApprove})
	assert.Equal(t, "the request was already decided", text(t, res))
	res = handle(as("admin@example.com", true, ""), Input{Command: CmdGet, ID: "none"})
	assert.Equal(t, "no such request", text(t, res))

	res = handle(as("jane@example.com", false, middleware.SourceScript), Input{Command: CmdList})
	require.False(t, res.IsError, "a run reads")
	assert.Contains(t, text(t, res), `"count":1`)
	res = handle(as("jane@example.com", false, ""), Input{Command: CmdGet, ID: id})
	require.False(t, res.IsError)
	res = handle(as("jane@example.com", false, ""), Input{Command: "nope"})
	assert.Contains(t, text(t, res), "unknown command")
	res = handle(context.Background(), Input{Command: CmdList})
	assert.True(t, res.IsError, "no caller")
}

func TestRegister(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t"}, nil)
	(&Tool{Service: &approval.Service{}}).Register(server)
	(*Tool)(nil).Register(server)
	(&Tool{}).Register(server)
	assert.NotNil(t, schema()["properties"])
}
