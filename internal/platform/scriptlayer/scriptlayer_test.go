package scriptlayer

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// memStore is an in-memory script.Store + script.VersionStore, modeled on the
// PostgreSQL store's contracts: a miss is nil, nil; an edit that changes a
// versioned field snapshots a new applied version; a draft leaves the live row
// untouched.
type memStore struct {
	scripts  map[string]*script.Script
	versions map[string][]script.Version
	nextID   int
}

func newMemStore() *memStore {
	return &memStore{scripts: map[string]*script.Script{}, versions: map[string][]script.Version{}}
}

func (m *memStore) Create(_ context.Context, sc *script.Script, author script.Author) error {
	m.nextID++
	sc.ID = fmt.Sprintf("script_%d", m.nextID)
	sc.Version = 1
	if sc.Status == "" {
		sc.Status = script.StatusDraft
	}
	stored := *sc
	m.scripts[sc.ID] = &stored
	m.snapshot(sc, author, script.VersionStatusApplied)
	return nil
}

func (m *memStore) snapshot(sc *script.Script, author script.Author, status string) {
	m.versions[sc.ID] = append(m.versions[sc.ID], script.Version{
		ID: fmt.Sprintf("sver_%s_%d", sc.ID, sc.Version), ScriptID: sc.ID, Version: sc.Version,
		DisplayName: sc.DisplayName, Description: sc.Description, Source: sc.Source,
		Params: sc.Params, Tags: sc.Tags, Author: author.Email, AuthorRoles: author.Roles,
		Status: status,
	})
}

func (m *memStore) Get(_ context.Context, name string) (*script.Script, error) {
	for _, sc := range m.scripts {
		if sc.Name == name && sc.Scope != script.ScopePersonal {
			out := *sc
			return &out, nil
		}
	}
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (m *memStore) GetPersonal(_ context.Context, owner, name string) (*script.Script, error) {
	for _, sc := range m.scripts {
		if sc.Name == name && sc.Scope == script.ScopePersonal && sc.OwnerEmail == owner {
			out := *sc
			return &out, nil
		}
	}
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (m *memStore) GetByID(_ context.Context, id string) (*script.Script, error) {
	sc, ok := m.scripts[id]
	if !ok {
		return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
	}
	out := *sc
	return &out, nil
}

func (m *memStore) Update(_ context.Context, sc *script.Script) error {
	if _, ok := m.scripts[sc.ID]; !ok {
		return fmt.Errorf("script %s not found", sc.ID)
	}
	stored := *sc
	m.scripts[sc.ID] = &stored
	return nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	if _, ok := m.scripts[id]; !ok {
		return fmt.Errorf("script %s not found", id)
	}
	delete(m.scripts, id)
	return nil
}

func (m *memStore) List(_ context.Context, filter script.ListFilter) ([]script.Script, error) {
	out := []script.Script{}
	for _, sc := range slices.SortedFunc(maps.Values(m.scripts), func(a, b *script.Script) int {
		return strings.Compare(a.Name, b.Name)
	}) {
		if filter.OwnerEmail != "" && sc.OwnerEmail != filter.OwnerEmail {
			continue
		}
		if filter.Scope != "" && sc.Scope != filter.Scope {
			continue
		}
		// The fake models the real predicate, not a convenient subset: a store
		// that ignored VisibleTo would let a listing test pass while the
		// PostgreSQL store returned a different set.
		if filter.VisibleTo != "" && !sc.VisibleTo(filter.VisibleTo, filter.VisiblePersona) {
			continue
		}
		out = append(out, *sc)
	}
	return out, nil
}

func (m *memStore) UpdateWithVersion(ctx context.Context, sc *script.Script, author script.Author) error {
	before, ok := m.scripts[sc.ID]
	if !ok {
		return fmt.Errorf("script %s not found", sc.ID)
	}
	if script.SnapshotChanged(before, sc) {
		sc.Version = len(m.versions[sc.ID]) + 1
		m.snapshot(sc, author, script.VersionStatusApplied)
	}
	return m.Update(ctx, sc)
}

func (m *memStore) CreateDraftVersion(_ context.Context, scriptID string, proposed *script.Script, author script.Author) (int, error) {
	n := len(m.versions[scriptID]) + 1
	draft := *proposed
	draft.ID, draft.Version = scriptID, n
	m.snapshot(&draft, author, script.VersionStatusDraft)
	return n, nil
}

func (m *memStore) ListVersions(_ context.Context, scriptID string) ([]script.Version, error) {
	return slices.Clone(m.versions[scriptID]), nil
}

func (m *memStore) GetVersionByID(_ context.Context, id string) (*script.Version, error) {
	for _, versions := range m.versions {
		for _, v := range versions {
			if v.ID == id {
				out := v
				return &out, nil
			}
		}
	}
	return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
}

func (m *memStore) GetVersion(_ context.Context, scriptID string, version int) (*script.Version, error) {
	for _, v := range m.versions[scriptID] {
		if v.Version == version {
			out := v
			return &out, nil
		}
	}
	return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
}

// ApproveVersion models the store-side approval action: it copies the version
// author's roles into the grant (never the caller's request), stamps the
// approval, and points the script's execution gate at that version. A fake that
// took the roles from the request would let a test pass while the real store
// refused to widen authority.
func (m *memStore) ApproveVersion(_ context.Context, scriptID string, version int, approver string, grants script.Grants) (*script.Version, error) {
	sc, ok := m.scripts[scriptID]
	if !ok {
		return nil, fmt.Errorf("script %s not found", scriptID)
	}
	for i, v := range m.versions[scriptID] {
		if v.Version != version {
			continue
		}
		approvedAt := time.Now().UTC()
		grants.Roles = v.AuthorRoles
		if err := grants.Validate(); err != nil {
			return nil, fmt.Errorf("this version cannot be approved with that capability set: %w (%w)", err, script.ErrInvalidGrant)
		}
		m.versions[scriptID][i].ApprovedBy = approver
		m.versions[scriptID][i].ApprovedAt = &approvedAt
		m.versions[scriptID][i].Grants = grants
		m.versions[scriptID][i].Status = script.VersionStatusApplied
		sc.ApprovedVersionID = m.versions[scriptID][i].ID
		if sc.Status == script.StatusDraft {
			sc.Status = script.StatusActive
		}
		out := m.versions[scriptID][i]
		return &out, nil
	}
	return nil, fmt.Errorf("script %s has no version %d: %w", scriptID, version, script.ErrVersionConflict)
}

// callerCtx returns a context carrying an authenticated caller. The roles are
// part of the identity rather than decoration: every version an author writes
// records the roles they held, and those roles are the ceiling on what
// approving that version can grant.
func callerCtx(email, persona string) context.Context {
	pc := middleware.NewPlatformContext("req_1")
	pc.UserID, pc.UserEmail, pc.PersonaName = "user-"+email, email, persona
	pc.Roles = []string{persona}
	return middleware.WithPlatformContext(context.Background(), pc)
}

func authorCtx() context.Context { return callerCtx("jane@example.com", "analyst") }
func adminCtx() context.Context  { return callerCtx("admin@example.com", "admin") }

// newHandle builds a Handle over an in-memory store.
func newHandle() (*Handle, *memStore) {
	store := newMemStore()
	return New(Config{Store: store, AdminPersona: "admin"}), store
}

// resultText returns a tool result's first text block.
func resultText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// resultFields decodes a JSON tool result.
func resultFields(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &out), resultText(res))
	return out
}

// call runs one manage_script command.
func call(t *testing.T, h *Handle, ctx context.Context, input manageScriptInput) *mcp.CallToolResult { //nolint:revive // t first is the testing convention this file follows throughout
	t.Helper()
	res, _, err := h.handleManageScript(ctx, input)
	require.NoError(t, err)
	return res
}

// createDaily creates a working script owned by the author.
func createDaily(t *testing.T, h *Handle) *mcp.CallToolResult {
	t.Helper()
	return call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", DisplayName: "Daily",
		Source: "print(\"hello\")\n",
	})
}

func TestCreate_StoresADraftThatIsNotExecutable(t *testing.T) {
	h, store := newHandle()
	res := createDaily(t, h)
	require.False(t, res.IsError, resultText(res))

	fields := resultFields(t, res)
	assert.Equal(t, "created", fields["status"])
	require.Len(t, store.scripts, 1)
	for _, sc := range store.scripts {
		assert.Equal(t, script.StatusDraft, sc.Status)
		assert.Empty(t, sc.ApprovedVersionID, "nothing in the authoring loop approves a version")
		assert.Equal(t, "jane@example.com", sc.OwnerEmail)
		assert.Equal(t, script.ScopePersonal, sc.Scope)
	}
}

// TestCreate_RefusesSourceThatDoesNotParse pins the rule that an unparseable
// script never reaches the store, so every stored version is one a reviewer
// could read.
func TestCreate_RefusesSourceThatDoesNotParse(t *testing.T) {
	h, store := newHandle()
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "broken", Source: "import os\n",
	})
	fields := resultFields(t, res)
	assert.Equal(t, "invalid", fields["status"])
	assert.Contains(t, resultText(res), "import")
	assert.Empty(t, store.scripts)
}

func TestCreate_Refusals(t *testing.T) {
	cases := []struct {
		name    string
		ctx     context.Context
		input   manageScriptInput
		wantErr string
	}{
		{"no name", authorCtx(), manageScriptInput{Command: cmdCreate, Source: "x = 1"}, "name is required"},
		{"no source", authorCtx(), manageScriptInput{Command: cmdCreate, Name: "a"}, "source is required"},
		{"bad params", authorCtx(), manageScriptInput{Command: cmdCreate, Name: "a", Source: "x = 1", Params: []script.Param{{Name: "A"}}}, "lowercase letter"},
		{"shared scope needs admin", authorCtx(), manageScriptInput{Command: cmdCreate, Name: "a", Source: "x = 1", Scope: script.ScopeGlobal}, "only admins"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newHandle()
			res := call(t, h, tc.ctx, tc.input)
			assert.True(t, res.IsError, resultText(res))
			assert.Contains(t, resultText(res), tc.wantErr)
		})
	}
}

func TestCreate_AdminCanCreateShared(t *testing.T) {
	h, _ := newHandle()
	res := call(t, h, adminCtx(), manageScriptInput{
		Command: cmdCreate, Name: "shared", Source: "x = 1",
		Scope: script.ScopePersona, Personas: []string{"analyst"},
	})
	require.False(t, res.IsError, resultText(res))
}

func TestUpdate_AppliesAndVersions(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Source: "print(\"changed\")\n",
	})
	require.False(t, res.IsError, resultText(res))
	fields := resultFields(t, res)
	assert.Equal(t, "updated", fields["status"])
	assert.EqualValues(t, 2, fields["version"])
	for _, sc := range store.scripts {
		assert.Equal(t, "print(\"changed\")\n", sc.Source)
	}
}

// TestUpdate_ApprovedScriptDefersToADraft is the gate as an author sees it: the
// live row keeps its source and the response says the change is waiting.
func TestUpdate_ApprovedScriptDefersToADraft(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)
	for _, sc := range store.scripts {
		sc.ApprovedVersionID = "sver_1"
		sc.Status = script.StatusActive
	}

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Source: "print(\"changed\")\n",
	})
	require.False(t, res.IsError, resultText(res))
	fields := resultFields(t, res)
	assert.Equal(t, "pending_approval", fields["status"])
	assert.EqualValues(t, 2, fields["pending_version"])
	for _, sc := range store.scripts {
		assert.Equal(t, "print(\"hello\")\n", sc.Source, "the live row keeps serving the approved source")
	}
}

// TestUpdate_MixedEditRefusedThroughTheTool proves the funnel's refusal reaches
// the caller rather than being swallowed into a generic failure.
func TestUpdate_MixedEditRefusedThroughTheTool(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)
	for _, sc := range store.scripts {
		sc.ApprovedVersionID = "sver_1"
		sc.Status = script.StatusActive
	}

	res := call(t, h, adminCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", OwnerEmail: "jane@example.com",
		Source: "print(\"changed\")\n", Scope: script.ScopeGlobal,
	})
	require.True(t, res.IsError, resultText(res))
	assert.Contains(t, resultText(res), "cannot be combined with")
	for _, sc := range store.scripts {
		assert.Equal(t, script.ScopePersonal, sc.Scope, "a refused edit lands nothing")
		assert.Equal(t, "print(\"hello\")\n", sc.Source)
	}
}

func TestUpdate_Authorization(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)

	other := callerCtx("bob@example.com", "analyst")
	res := call(t, h, other, manageScriptInput{Command: cmdUpdate, Name: "daily", DisplayName: "x"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not found", "another user's personal script is not even visible")

	res = call(t, h, other, manageScriptInput{
		Command: cmdUpdate, Name: "daily", OwnerEmail: "jane@example.com", DisplayName: "x",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "only address your own")

	// Scope and status are admin-only even for the owner.
	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Scope: script.ScopeGlobal})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "only admins can change a script's scope")

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Status: script.StatusDeprecated})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "only admins can change a script's lifecycle status")
}

func TestUpdate_FieldsAndFlags(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	no := false
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", DisplayName: "New", Description: "d",
		Tags: []string{"sales"}, Enabled: &no,
		Params: []script.Param{{Name: "day", Type: script.ParamTypeDate, Required: true}},
	})
	require.False(t, res.IsError, resultText(res))
	for _, sc := range store.scripts {
		assert.Equal(t, "New", sc.DisplayName)
		assert.Equal(t, "d", sc.Description)
		assert.Equal(t, []string{"sales"}, sc.Tags)
		assert.False(t, sc.Enabled, "enabled is a pointer so false is distinguishable from unsent")
		assert.Len(t, sc.Params, 1)
	}

	res = call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Params: []script.Param{{Name: "Day"}},
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "lowercase letter")

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Source: "import os\n"})
	assert.Equal(t, "invalid", resultFields(t, res)["status"])
}

func TestUpdate_AdminStatusTransition(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, adminCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", OwnerEmail: "jane@example.com",
		Status: script.StatusDeprecated,
	})
	require.False(t, res.IsError, resultText(res))
	for _, sc := range store.scripts {
		assert.Equal(t, script.StatusDeprecated, sc.Status)
	}

	// Activation is refused because there is no approved version to activate.
	res = call(t, h, adminCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", OwnerEmail: "jane@example.com",
		Status: script.StatusActive,
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "no approved version")
}

func TestDelete(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdDelete, Name: "daily"})
	require.False(t, res.IsError, resultText(res))
	assert.Empty(t, store.scripts)
}

// TestDelete_RefusedForAnApprovedScript keeps a script that the platform may be
// executing from vanishing out from under its runs.
func TestDelete_RefusedForAnApprovedScript(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)
	for _, sc := range store.scripts {
		sc.ApprovedVersionID = "sver_1"
	}

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdDelete, Name: "daily"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "deprecate it")
	assert.Len(t, store.scripts, 1)
}

func TestGet_ReportsTheExecutionGate(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))
	assert.Equal(t, false, fields["executable"])
	assert.Contains(t, fields["executable_note"], "no approved version")

	for _, sc := range store.scripts {
		sc.ApprovedVersionID = "sver_1"
	}
	fields = resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))
	assert.Equal(t, true, fields["executable"])
	assert.Contains(t, fields["executable_note"], "may execute it")
}

// TestGet_ResolvesABuiltInExample covers the seeded examples: an author reads a
// worked script with the same command they read their own with.
func TestGet_ResolvesABuiltInExample(t *testing.T) {
	h, _ := newHandle()
	for _, ex := range examples {
		fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: ex.name}))
		assert.Equal(t, true, fields["builtin"])
		assert.NotEmpty(t, fields["source"])
	}
}

func TestGet_NotFound(t *testing.T) {
	h, _ := newHandle()
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "nope"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not found")

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdGet})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "name is required")
}

// TestList_AppliesTheSameScopeRuleAsTheReadPath: a listing filtered by owner
// alone would hide the shared scripts a caller is entitled to see and could
// run, and a listing filtered by nothing would leak the persona-scoped scripts
// of personas they do not hold.
func TestList_AppliesTheSameScopeRuleAsTheReadPath(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h) // personal, owned by jane
	call(t, h, adminCtx(), manageScriptInput{Command: cmdCreate, Name: "admin-private", Source: "x = 1"})
	call(t, h, adminCtx(), manageScriptInput{
		Command: cmdCreate, Name: "shared-global", Source: "x = 1", Scope: script.ScopeGlobal,
	})
	call(t, h, adminCtx(), manageScriptInput{
		Command: cmdCreate, Name: "for-analysts", Source: "x = 1",
		Scope: script.ScopePersona, Personas: []string{"analyst"},
	})
	call(t, h, adminCtx(), manageScriptInput{
		Command: cmdCreate, Name: "for-engineers", Source: "x = 1",
		Scope: script.ScopePersona, Personas: []string{"data-engineer"},
	})

	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdList}))
	names := listedNames(t, fields)
	assert.ElementsMatch(t, []string{"daily", "shared-global", "for-analysts"}, names,
		"the analyst sees their own, the global one, and the one scoped to their persona")

	fields = resultFields(t, call(t, h, adminCtx(), manageScriptInput{Command: cmdList}))
	assert.EqualValues(t, 5, fields["count"], "an admin sees every script")
}

// listedNames extracts the script names from a list response.
func listedNames(t *testing.T, fields map[string]any) []string {
	t.Helper()
	items, ok := fields["scripts"].([]any)
	require.True(t, ok)
	names := make([]string, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		require.True(t, ok)
		name, _ := row["name"].(string)
		names = append(names, name)
	}
	return names
}

// TestRead_HidesAPersonaScopedScriptFromOtherPersonas keeps the read path and
// the list path answering the same question, with a message that does not
// confirm the script exists.
func TestRead_HidesAPersonaScopedScriptFromOtherPersonas(t *testing.T) {
	h, _ := newHandle()
	call(t, h, adminCtx(), manageScriptInput{
		Command: cmdCreate, Name: "for-engineers", Source: "x = 1",
		Scope: script.ScopePersona, Personas: []string{"data-engineer"},
	})

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "for-engineers"})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not found")

	engineer := callerCtx("bob@example.com", "data-engineer")
	res = call(t, h, engineer, manageScriptInput{Command: cmdGet, Name: "for-engineers"})
	assert.False(t, res.IsError, resultText(res))
}

func TestUnknownCommand(t *testing.T) {
	h, _ := newHandle()
	res := call(t, h, authorCtx(), manageScriptInput{Command: "frobnicate"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "unknown command")
}

func TestHelp_StatesTheDialectAndTheExamples(t *testing.T) {
	h, _ := newHandle()
	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdHelp}))

	dialect, ok := fields["dialect"].(string)
	require.True(t, ok)
	for _, want := range []string{"platform.query", "run.fire_time", "There is no module system", "while", "deterministic"} {
		assert.Contains(t, strings.ToLower(dialect), strings.ToLower(want))
	}
	assert.Len(t, fields["examples"], len(examples))
}

// TestNilHandleIsSafe covers the deployment shapes that register nothing: no
// handle at all, and a handle with no database to keep a script in.
func TestNilHandleIsSafe(t *testing.T) {
	var h *Handle
	h.RegisterTool(nil)

	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	New(Config{}).RegisterTool(server)

	tools := listToolNames(t, server)
	assert.NotContains(t, tools, ToolNameManageScript,
		"a deployment with no database registers no script tool")
}

// listToolNames returns the tools a server advertises.
func listToolNames(t *testing.T, server *mcp.Server) []string {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestResolveEmail_Anonymous(t *testing.T) {
	assert.Equal(t, "anonymous", resolveEmail(context.Background()))
}
