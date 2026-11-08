package apptool

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// store is an in-memory application.Store.
type store struct {
	apps     map[string]*application.Application
	releases []application.Release
}

func (s *store) Create(_ context.Context, a *application.Application) error {
	if s.apps[a.Name] != nil {
		return application.ErrNameTaken
	}
	a.ID = "app_" + a.Name
	s.apps[a.Name] = a
	return nil
}

func (s *store) Get(_ context.Context, id string) (*application.Application, error) {
	for _, a := range s.apps {
		if a.ID == id {
			cp := *a
			return &cp, nil
		}
	}
	return nil, application.ErrNotFound
}

func (s *store) GetByName(_ context.Context, name string) (*application.Application, error) {
	if a := s.apps[name]; a != nil {
		cp := *a
		return &cp, nil
	}
	return nil, application.ErrNotFound
}

func (s *store) List(context.Context) ([]application.Application, error) {
	out := make([]application.Application, 0, len(s.apps))
	for _, a := range s.apps {
		out = append(out, *a)
	}
	return out, nil
}

func (s *store) Update(_ context.Context, a *application.Application) error {
	s.apps[a.Name].Description = a.Description
	return nil
}

func (s *store) Delete(_ context.Context, id string) error {
	for n, a := range s.apps {
		if a.ID == id {
			delete(s.apps, n)
		}
	}
	return nil
}

func (s *store) AddPart(_ context.Context, id string, p application.Part) error {
	for _, a := range s.apps {
		if a.ID == id {
			a.Parts = append(a.Parts, p)
		}
	}
	return nil
}

func (s *store) RemovePart(_ context.Context, id, kind, part string) error {
	for _, a := range s.apps {
		if a.ID == id {
			i := slices.IndexFunc(a.Parts, func(p application.Part) bool { return p.Kind == kind && p.ID == part })
			if i < 0 {
				return application.ErrNoPart
			}
			a.Parts = slices.Delete(a.Parts, i, i+1)
		}
	}
	return nil
}

func (*store) Owning(context.Context, string, string) (*application.Application, error) {
	return nil, application.ErrNotFound
}

func (s *store) SetSchema(_ context.Context, id string, h application.HeldSchema) error {
	a := s.apps[id]
	a.Connection, a.Schema = h.Connection, h.Schema
	a.Parts = append(a.Parts, application.Part{Kind: application.KindSchema, ID: application.SchemaPartID(h.Connection, h.Schema)})
	return nil
}

func (*store) SetRunRole(context.Context, string, application.HeldSchema) error { return nil }

func (s *store) ClearSchema(_ context.Context, id string) error {
	a := s.apps[id]
	a.Connection, a.Schema = "", ""
	return nil
}

func (s *store) Holding(_ context.Context, connection, schema string) (*application.Application, error) {
	for _, a := range s.apps {
		if a.Connection == connection && a.Schema == schema {
			return a, nil
		}
	}
	return nil, application.ErrNotFound
}

func (*store) RunRoleOf(context.Context, string) (*application.RunRole, error) {
	return nil, application.ErrNoRunRole
}

func (s *store) Releases(context.Context, string) ([]application.Release, error) {
	return s.releases, nil
}

func (s *store) Release(_ context.Context, _ string, n int) (*application.Release, error) {
	for _, r := range s.releases {
		if r.Number == n {
			return &r, nil
		}
	}
	return nil, application.ErrNoRelease
}

func (s *store) Record(_ context.Context, id string, r application.Recorded) (*application.Release, error) {
	rel := r.Release
	rel.Number = len(s.releases) + 1
	s.releases = append(s.releases, rel)
	for _, a := range s.apps {
		if a.ID == id {
			a.CurrentRelease = rel.Number
		}
	}
	return &rel, nil
}

type scripts struct{}

func (scripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	return &script.Script{ID: id, Name: "api", OwnerEmail: "jane@example.com", Version: 1}, nil
}

func (scripts) GetVersion(context.Context, string, int) (*script.Version, error) {
	return &script.Version{}, nil
}

type bundles struct{ exported, imported bool }

func (b *bundles) Export(context.Context, application.Actor, string, int) (any, error) {
	b.exported = true
	return map[string]any{"ok": true}, nil
}

func (b *bundles) Import(_ context.Context, _ application.Actor, ref string, _ map[string]string) (any, error) {
	b.imported = true
	if ref == "" {
		return nil, &appsvc.RefusedError{Msg: "name the bundle"}
	}
	return map[string]any{"ok": true}, nil
}

// session registers the tool on a server that sets the caller as the
// tool-call middleware would, and connects a client.
func session(t *testing.T, tool *Tool, pc *middleware.PlatformContext) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	tool.Register(server)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(middleware.WithPlatformContext(ctx, pc), method, req)
		}
	})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	_, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: ToolName, Arguments: args})
	require.NoError(t, err)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return text.Text, res.IsError
}

func pcFor(email string, admin bool) *middleware.PlatformContext {
	pc := middleware.NewPlatformContext("r")
	pc.UserEmail, pc.IsAdmin = email, admin
	return pc
}

func TestTool_ThroughTheProtocol(t *testing.T) {
	st := &store{apps: map[string]*application.Application{}}
	b := &bundles{}
	tool := &Tool{Service: &appsvc.Service{Store: st, Scripts: scripts{}}, Bundles: b}
	owner := session(t, tool, pcFor("jane@example.com", false))
	adm := session(t, tool, pcFor("admin@example.com", true))

	text, isErr := call(t, owner, map[string]any{"command": "create", "name": "shop", "description": "a shop"})
	require.False(t, isErr, text)
	var created application.Application
	require.NoError(t, json.Unmarshal([]byte(text), &created))
	assert.Equal(t, "jane@example.com", created.OwnerEmail)

	text, isErr = call(t, owner, map[string]any{"command": "create", "name": "shop"})
	assert.True(t, isErr)
	assert.Contains(t, text, "exists")

	text, isErr = call(t, owner, map[string]any{
		"command": "add_part", "application": "shop", "kind": "script",
		"part_id": "11111111-1111-1111-1111-111111111111",
	})
	require.False(t, isErr, text)

	text, isErr = call(t, owner, map[string]any{"command": "release", "application": "shop"})
	assert.True(t, isErr, "an owner does not release")
	assert.Contains(t, text, "administrators")

	text, isErr = call(t, adm, map[string]any{"command": "release", "application": "shop", "note": "first"})
	require.False(t, isErr, text)
	assert.Contains(t, text, `"number":1`)

	text, isErr = call(t, owner, map[string]any{"command": "get", "application": "shop"})
	require.False(t, isErr, text)
	assert.Contains(t, text, `"current_release"`)
	text, isErr = call(t, owner, map[string]any{"command": "releases", "application": "shop"})
	require.False(t, isErr, text)
	assert.Contains(t, text, `"releases"`)
	text, isErr = call(t, owner, map[string]any{"command": "list"})
	require.False(t, isErr, text)
	assert.Contains(t, text, `"total":1`)
	_, isErr = call(t, owner, map[string]any{"command": "update", "application": "shop", "description": "new"})
	assert.False(t, isErr)
	text, isErr = call(t, adm, map[string]any{"command": "rollback", "application": "shop"})
	assert.True(t, isErr, "no earlier release")
	assert.Contains(t, text, "no earlier release")
	text, isErr = call(t, owner, map[string]any{
		"command": "remove_part", "application": "shop", "kind": "script",
		"part_id": "11111111-1111-1111-1111-111111111111",
	})
	assert.True(t, isErr, "the owner may not take a pinned part out from behind the release")
	assert.Contains(t, text, "administrators")
	_, isErr = call(t, adm, map[string]any{
		"command": "remove_part", "application": "shop", "kind": "script",
		"part_id": "11111111-1111-1111-1111-111111111111",
	})
	assert.False(t, isErr)
	_, isErr = call(t, owner, map[string]any{"command": "remove_part", "application": "shop", "kind": "script", "part_id": "x"})
	assert.True(t, isErr)

	_, isErr = call(t, adm, map[string]any{"command": "export", "application": "shop"})
	assert.False(t, isErr)
	assert.True(t, b.exported)
	text, isErr = call(t, adm, map[string]any{"command": "import"})
	assert.True(t, isErr)
	assert.Equal(t, "name the bundle", text)

	text, isErr = call(t, owner, map[string]any{"command": "get", "application": "nope"})
	assert.True(t, isErr)
	assert.Contains(t, text, "no such application")
	_, isErr = call(t, owner, map[string]any{"command": "delete", "application": "shop"})
	assert.True(t, isErr, "the owner may not delete a released application")
	_, isErr = call(t, adm, map[string]any{"command": "delete", "application": "shop"})
	assert.False(t, isErr)
}

func TestTool_RefusedInsideARunAndWithoutBundles(t *testing.T) {
	tool := &Tool{Service: &appsvc.Service{Store: &store{apps: map[string]*application.Application{}}}}
	pc := pcFor("jane@example.com", true)
	pc.Source = middleware.SourceScript
	cs := session(t, tool, pc)
	text, isErr := call(t, cs, map[string]any{"command": "create", "name": "shop"})
	assert.True(t, isErr)
	assert.Contains(t, text, "inside a script run")
	_, isErr = call(t, cs, map[string]any{"command": "list"})
	assert.False(t, isErr, "a run may read")

	plain := session(t, tool, pcFor("admin@example.com", true))
	text, isErr = call(t, plain, map[string]any{"command": "export", "application": "shop"})
	assert.True(t, isErr)
	assert.Contains(t, text, "cannot export")
	text, _ = call(t, plain, map[string]any{"command": "nope"})
	assert.NotEmpty(t, text)
}

func TestMessageAndRegisterNil(t *testing.T) {
	assert.Equal(t, "boom", message(errors.New("boom")))
	assert.Equal(t, "x", message(&appsvc.RefusedError{Msg: "x"}))
	assert.Contains(t, message(application.ErrStale), "first")
	var nilTool *Tool
	assert.NotPanics(t, func() { nilTool.Register(nil) })
}
