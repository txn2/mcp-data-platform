package scriptlayer

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// memStore is an in-memory script.Store + script.VersionStore, modeled on the
// PostgreSQL store's contracts: a miss is nil, nil; an edit that changes a
// versioned field snapshots a new applied version and advances the live row.
type memStore struct {
	scripts  map[string]*script.Script
	versions map[string][]script.Version
	// schedules is the schedule half of the store, implemented in
	// schedules_test.go; scheduleErr makes it fail on demand.
	schedules   map[string]*script.Schedule
	scheduleErr error
	// scheduleReadErr fails only the read-back, so a write that landed can be
	// distinguished from one that did not.
	scheduleReadErr error
	// versionErr fails the current-version lookup, enabledErr the
	// enable/disable write.
	versionErr error
	enabledErr error
	nextID     int
}

func newMemStore() *memStore {
	return &memStore{
		scripts:   map[string]*script.Script{},
		versions:  map[string][]script.Version{},
		schedules: map[string]*script.Schedule{},
	}
}

func (m *memStore) Create(_ context.Context, sc *script.Script, author script.Author) error {
	m.nextID++
	sc.ID = fmt.Sprintf("script_%d", m.nextID)
	sc.Version = 1
	if sc.Status == "" {
		// The real store's Create defaults the status: every script starts in
		// service.
		sc.Status = script.StatusActive
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

func (m *memStore) GetByName(_ context.Context, owner, name string) (*script.Script, error) {
	if owner == "" {
		return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
	}
	for _, sc := range m.scripts {
		if sc.Name == name && sc.OwnerEmail == owner {
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
		// The facet axes model the real store's operators: the category is an
		// equality and the tags are an OVERLAP, so naming two tags asks for the
		// scripts carrying either (#1369).
		if filter.Category != "" && sc.Category != filter.Category {
			continue
		}
		if len(filter.Tags) > 0 && !slices.ContainsFunc(filter.Tags, func(t string) bool {
			return slices.Contains(sc.Tags, t)
		}) {
			continue
		}
		out = append(out, *sc)
	}
	return out, nil
}

// Transfer models the real store: the owner moves and the move is snapshotted
// unconditionally, because the roles captured on that version are what the
// script now runs with.
func (m *memStore) Transfer(_ context.Context, id, newOwner string, author script.Author) error {
	stored, ok := m.scripts[id]
	if !ok {
		return fmt.Errorf("script %s not found", id)
	}
	moved := *stored
	if err := moved.Transfer(newOwner); err != nil {
		return err //nolint:wrapcheck // the fake mirrors the store: the domain refusal is the caller's message
	}
	for _, other := range m.scripts {
		if other.ID != id && other.Name == moved.Name && other.OwnerEmail == moved.OwnerEmail {
			return fmt.Errorf("a script named %q already belongs to %s: %w",
				moved.Name, moved.OwnerEmail, script.ErrNameTaken)
		}
	}
	moved.Version++
	m.scripts[id] = &moved
	m.snapshot(&moved, author, script.VersionStatusApplied)
	return nil
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
	if m.versionErr != nil {
		return nil, m.versionErr
	}
	for _, v := range m.versions[scriptID] {
		if v.Version == version {
			out := v
			return &out, nil
		}
	}
	return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
}

// callerCtx returns a context carrying an authenticated caller. The roles are
// part of the identity rather than decoration: every version an author writes
// records the roles they held, and a run of that version presents exactly
// those roles.
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

// createAdmins creates a script an administrator wrote and owns.
func createAdmins(t *testing.T, h *Handle) *mcp.CallToolResult {
	t.Helper()
	return call(t, h, adminCtx(), manageScriptInput{
		Command: cmdCreate, Name: "shared", DisplayName: "Shared",
		Source: "print(\"hello\")\n",
	})
}

// TestCreate_StartsActiveAndRuns pins that a saved script is a runnable script:
// there is no draft state and nothing to approve, so create lands version 1 as
// the version run_script executes.
func TestCreate_StartsActiveAndRuns(t *testing.T) {
	h, store := newHandle()
	res := createDaily(t, h)
	require.False(t, res.IsError, resultText(res))

	fields := resultFields(t, res)
	assert.Equal(t, "created", fields["status"])
	assert.EqualValues(t, 1, fields["version"])
	assert.Contains(t, fields["next"], "run_script")
	require.Len(t, store.scripts, 1)
	for _, sc := range store.scripts {
		assert.Equal(t, script.StatusActive, sc.Status)
		assert.Equal(t, "jane@example.com", sc.OwnerEmail)

		v, err := store.GetVersion(context.Background(), sc.ID, sc.Version)
		require.NoError(t, err)
		require.NotNil(t, v, "the version run_script loads exists from the first save")
		assert.Equal(t, script.VersionStatusApplied, v.Status)
		assert.Equal(t, []string{"analyst"}, v.AuthorRoles,
			"the version records the authority its runs present")
	}
}

// TestCreate_SharedScriptStartsActiveToo pins that scope does not change the
// lifecycle: a global script an administrator saves is in service on save.
func TestCreate_SharedScriptStartsActiveToo(t *testing.T) {
	h, store := newHandle()
	res := createAdmins(t, h)
	require.False(t, res.IsError, resultText(res))

	require.Len(t, store.scripts, 1)
	for _, sc := range store.scripts {
		assert.Equal(t, script.StatusActive, sc.Status)
		assert.NoError(t, script.RefuseRun(sc), "nothing gates a freshly saved script")
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

// TestCreate_NamesAreUniquePerOwner proves two people may each keep a script
// under the same name, which is what makes a name their own to choose.
func TestCreate_NamesAreUniquePerOwner(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, callerCtx("bob@example.com", "analyst"), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: "x = 1",
	})

	require.False(t, res.IsError, resultText(res))
	assert.Len(t, store.scripts, 2)
}

// TestUpdate_AppliesToTheLiveRowAndAdvancesTheVersion pins the one-edit-path
// rule: an edit lands on the live record, the version advances to it, and the
// saved source is the source a run executes.
func TestUpdate_AppliesToTheLiveRowAndAdvancesTheVersion(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Source: "print(\"changed\")\n",
	})
	require.False(t, res.IsError, resultText(res))
	fields := resultFields(t, res)
	assert.Equal(t, "updated", fields["status"])
	assert.EqualValues(t, 2, fields["version"])
	assert.Contains(t, fields["message"], "this version is what runs now")
	for _, sc := range store.scripts {
		assert.Equal(t, "print(\"changed\")\n", sc.Source)
		assert.Equal(t, 2, sc.Version)

		v, err := store.GetVersion(context.Background(), sc.ID, sc.Version)
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.Equal(t, "print(\"changed\")\n", v.Source,
			"the version the run gate points at carries the edited source")
	}
}

// TestUpdate_AppliesDirectlyToASharedScript pins that an admin's edit of a
// shared script lands live rather than waiting on anything: there is no review
// state between a save and the version that runs.
func TestUpdate_AppliesDirectlyToASharedScript(t *testing.T) {
	h, store := newHandle()
	createAdmins(t, h)

	res := call(t, h, adminCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "shared", Source: "print(\"changed\")\n",
	})
	require.False(t, res.IsError, resultText(res))
	fields := resultFields(t, res)
	assert.Equal(t, "updated", fields["status"])
	assert.EqualValues(t, 2, fields["version"])
	for _, sc := range store.scripts {
		assert.Equal(t, "print(\"changed\")\n", sc.Source, "the live row serves the edited source")
	}
}

// TestUpdate_DoesNotClaimADisabledScriptRuns keeps the tool's answer honest:
// the run gate refuses a disabled script whatever was just saved, so a save
// that said it runs would be a false statement an author acts on.
func TestUpdate_DoesNotClaimADisabledScriptRuns(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)

	no := false
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Description: "quieter", Enabled: &no,
	})
	require.False(t, res.IsError, resultText(res))

	message, _ := resultFields(t, res)["message"].(string)
	assert.Contains(t, message, "until it is enabled again")
	assert.NotContains(t, message, "executes now")
}

// TestUpdate_DoesNotClaimADeprecatedScriptRuns is the other state the run gate
// refuses whatever was just saved.
func TestUpdate_DoesNotClaimADeprecatedScriptRuns(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)
	res := call(t, h, adminCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", OwnerEmail: "jane@example.com",
		Status: script.StatusDeprecated,
	})
	require.False(t, res.IsError, resultText(res))

	res = call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Source: "print(\"changed\")\n",
	})
	require.False(t, res.IsError, resultText(res))
	message, _ := resultFields(t, res)["message"].(string)
	assert.Contains(t, message, "is deprecated")
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

	// The lifecycle status is admin-only even for the owner.
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
	createAdmins(t, h)

	res := call(t, h, adminCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "shared", Status: script.StatusDeprecated,
	})
	require.False(t, res.IsError, resultText(res))
	for _, sc := range store.scripts {
		assert.Equal(t, script.StatusDeprecated, sc.Status)
	}

	// Deprecation is an operational judgement an operator can reverse.
	res = call(t, h, adminCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "shared", Status: script.StatusActive,
	})
	require.False(t, res.IsError, resultText(res))
	for _, sc := range store.scripts {
		assert.Equal(t, script.StatusActive, sc.Status)
	}
}

// TestDelete_AnOwnerDeletesTheirOwnPersonalScript: nobody else could see it,
// run it, or notice it go, so it takes its schedule and history with it.
func TestDelete_AnOwnerDeletesTheirOwnPersonalScript(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdDelete, Name: "daily"})
	require.False(t, res.IsError, resultText(res))
	assert.Empty(t, store.scripts)
}

// TestDelete_TakesTheScriptAndItsHistory: a script is one person's, so its
// owner deleting it costs nobody else a schedule or a history.
func TestDelete_TakesTheScriptAndItsHistory(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdDelete, Name: "daily"})

	require.False(t, res.IsError, resultText(res))
	assert.Empty(t, store.scripts)
}

// TestDelete_RefusedForAnotherPersonsScript: somebody who does not own a script
// is not told it is there, while an administrator is unrestricted.
func TestDelete_RefusedForAnotherPersonsScript(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, callerCtx("bob@example.com", "analyst"),
		manageScriptInput{Command: cmdDelete, Name: "daily"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not found")
	assert.Len(t, store.scripts, 1)

	res = call(t, h, adminCtx(), manageScriptInput{
		Command: cmdDelete, Name: "daily", OwnerEmail: "jane@example.com",
	})
	require.False(t, res.IsError, resultText(res))
	assert.Empty(t, store.scripts)
}

// TestGet_ReportsTheExecutionGate pins the executable_note: it answers with
// the run gate's own reading, so a reader learns whether run_script would
// execute the script and why not when it would refuse.
func TestGet_ReportsTheExecutionGate(t *testing.T) {
	h, store := newHandle()
	createAdmins(t, h)

	fields := resultFields(t, call(t, h, adminCtx(), manageScriptInput{Command: cmdGet, Name: "shared"}))
	assert.Contains(t, fields["executable_note"], "run_script",
		"a saved script runs, and the note says with what")

	for _, sc := range store.scripts {
		sc.Enabled = false
	}
	fields = resultFields(t, call(t, h, adminCtx(), manageScriptInput{Command: cmdGet, Name: "shared"}))
	assert.Contains(t, fields["executable_note"], "disabled",
		"the note carries the run gate's refusal in its own words")
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

// TestList_ShowsTheCallersOwnScripts proves the listing answers the same
// question the read path answers: a caller lists what they own, and an
// administrator lists every script on the platform.
func TestList_ShowsTheCallersOwnScripts(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h) // jane's
	call(t, h, adminCtx(), manageScriptInput{Command: cmdCreate, Name: "admin-private", Source: "x = 1"})
	call(t, h, callerCtx("bob@example.com", "analyst"), manageScriptInput{
		Command: cmdCreate, Name: "bobs-report", Source: "x = 1",
	})

	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdList}))
	assert.Equal(t, []string{"daily"}, listedNames(t, fields),
		"a caller lists their own scripts and nobody else's")

	fields = resultFields(t, call(t, h, adminCtx(), manageScriptInput{Command: cmdList}))
	assert.EqualValues(t, 3, fields["count"], "an admin sees every script")
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

// TestRead_HidesAnotherPersonsScript keeps the read path and the list path
// answering the same question, with a message that does not confirm the script
// exists, and proves an administrator is not held to it.
func TestRead_HidesAnotherPersonsScript(t *testing.T) {
	h, _ := newHandle()
	call(t, h, callerCtx("bob@example.com", "data-engineer"), manageScriptInput{
		Command: cmdCreate, Name: "bobs-report", Source: "x = 1",
	})

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "bobs-report"})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not found")

	res = call(t, h, adminCtx(), manageScriptInput{
		Command: cmdGet, Name: "bobs-report", OwnerEmail: "bob@example.com",
	})
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

// Help carries the built-in authoring pages, so the tool an agent is told to
// call before writing its first script is the tool that names the reading
// (#1476). The references are slugs: a built-in page's row id is generated per
// deployment, so an id here would resolve nowhere but the deployment it was
// read from.
func TestHelp_PointsAtTheBuiltInPages(t *testing.T) {
	h, _ := newHandle()
	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdHelp}))

	seeAlso, ok := fields["see_also"].([]any)
	require.True(t, ok, "help returned no see_also list: %+v", fields["see_also"])
	require.Len(t, seeAlso, len(KnowledgePages))

	for i, raw := range seeAlso {
		entry, ok := raw.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, KnowledgePages[i].Slug, entry["slug"])
		assert.Equal(t, "mcp:knowledge_page:"+KnowledgePages[i].Slug, entry["reference"])
		assert.NotEmpty(t, entry["summary"])
	}
	assert.Contains(t, fields["read_a_page"], "fetch")
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

// TestCreate_CarriesTheCategory proves the axis reaches the record from the
// tool, alongside the tags it has always carried (#1369).
func TestCreate_CarriesTheCategory(t *testing.T) {
	h, store := newHandle()

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: "print(1)\n",
		Category: new("reporting"), Tags: []string{"sales"},
	})

	require.False(t, res.IsError, resultText(res))
	for _, sc := range store.scripts {
		assert.Equal(t, "reporting", sc.Category)
		assert.Equal(t, []string{"sales"}, sc.Tags)
	}
}

// TestCreate_RefusesACategoryThatIsNotASlug keeps one category from being
// filed three ways. The check is the domain's, applied to the whole record, so
// the tool and the portal refuse the same input.
func TestCreate_RefusesACategoryThatIsNotASlug(t *testing.T) {
	h, _ := newHandle()

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: "print(1)\n", Category: new("Sales Reports"),
	})

	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "category must be at most 31 characters")
}

// TestUpdate_CarriesTheCategory proves the filing axis reaches the record from
// the tool: filing a script applies directly, as every edit does (#1369).
func TestUpdate_CarriesTheCategory(t *testing.T) {
	h, store := newHandle()
	createAdmins(t, h)

	res := call(t, h, adminCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "shared", Category: new("reporting"),
	})

	require.False(t, res.IsError, resultText(res))
	fields := resultFields(t, res)
	assert.Equal(t, "updated", fields["status"])
	for _, sc := range store.scripts {
		assert.Equal(t, "reporting", sc.Category)
	}
}

// TestUpdate_ClearsACategoryWhenAskedTo proves the axis can be UNSET through
// the tool, not only set. An agent told to unfile a script must not be answered
// "updated" over a record that still carries the category: the field is a
// pointer for exactly this, as the tag list beside it has always been a nil-able
// slice.
func TestUpdate_ClearsACategoryWhenAskedTo(t *testing.T) {
	h, store := newHandle()
	call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: "print(1)\n",
		Category: new("reporting"), Tags: []string{"sales"},
	})

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Category: new(""), Tags: []string{},
	})

	require.False(t, res.IsError, resultText(res))
	for _, sc := range store.scripts {
		assert.Empty(t, sc.Category)
		assert.Empty(t, sc.Tags)
	}

	// And an update that does not mention the category leaves it alone.
	call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Category: new("finance"),
	})
	res = call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", DisplayName: "Daily",
	})
	require.False(t, res.IsError, resultText(res))
	for _, sc := range store.scripts {
		assert.Equal(t, "finance", sc.Category)
	}
}

// TestUpdate_CarriesTheLongDescriptionAdvisory proves the signal travels with a
// SUCCESSFUL write: the description is stored and the response suggests where
// the prose might live better.
func TestUpdate_CarriesTheLongDescriptionAdvisory(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)
	long := strings.Repeat("x", 20_000)

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Description: long,
	})

	require.False(t, res.IsError, resultText(res))
	notice, _ := resultFields(t, res)["description_notice"].(string)
	assert.Contains(t, notice, "knowledge page")
	for _, sc := range store.scripts {
		assert.Equal(t, long, sc.Description, "the advisory must not have blocked the write")
	}

	// An ordinary description carries no advisory at all.
	res = call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Description: "By region, every weekday.",
	})
	require.False(t, res.IsError, resultText(res))
	assert.NotContains(t, resultFields(t, res), "description_notice")
}

// TestList_NarrowsByCategoryAndTag proves the two axes reach the store's filter
// from the tool, so an agent and the portal narrow a listing the same way.
func TestList_NarrowsByCategoryAndTag(t *testing.T) {
	h, _ := newHandle()
	call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily-sales", Source: "x = 1",
		Category: new("reporting"), Tags: []string{"sales"},
	})
	call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "margin-check", Source: "x = 1",
		Category: new("finance"), Tags: []string{"margins"},
	})

	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdList, Category: new("reporting"),
	}))
	assert.Equal(t, []string{"daily-sales"}, listedNames(t, fields))

	fields = resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdList, Tags: []string{"margins"},
	}))
	assert.Equal(t, []string{"margin-check"}, listedNames(t, fields))

	// The listing reports the axes it filters on, so a reader can see how a row
	// is filed without opening it.
	items, ok := fields["scripts"].([]any)
	require.True(t, ok)
	row, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "finance", row["category"])
}
