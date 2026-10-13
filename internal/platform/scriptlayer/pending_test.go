package scriptlayer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptbgdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptpending"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// memPending keeps refused creates in a map.
type memPending struct {
	creates map[string]scriptpending.Create
}

func (m *memPending) Put(_ context.Context, owner string, c scriptpending.Create) error {
	m.creates[owner+"/"+c.Name] = c
	return nil
}

func (m *memPending) Get(_ context.Context, owner, name string) (*scriptpending.Create, error) {
	c, ok := m.creates[owner+"/"+name]
	if !ok {
		return nil, nil //nolint:nilnil // none kept
	}
	return &c, nil
}

func (m *memPending) Delete(_ context.Context, owner, name string) error {
	delete(m.creates, owner+"/"+name)
	return nil
}

// sourceLibrary is a managed-resource library holding one file.
type sourceLibrary struct {
	resource.Store
	file *resource.Resource
}

func (s sourceLibrary) Get(_ context.Context, id string) (*resource.Resource, error) {
	if id == s.file.ID {
		return s.file, nil
	}
	return nil, fmt.Errorf("scanning: %w", sql.ErrNoRows)
}

type sourceBlobs struct {
	resource.S3Client
	body string
}

func (b sourceBlobs) GetObject(context.Context, string, string) (body []byte, contentType string, err error) {
	return []byte(b.body), "text/plain", nil
}

// pendingHandle is a layer that keeps refused creates and reads sources
// from a library holding source.
func pendingHandle(source string) (handle *Handle, store *memStore, pending *memPending) {
	store = newMemStore()
	pending = &memPending{creates: map[string]scriptpending.Create{}}
	file := &resource.Resource{
		ID: "src1", Scope: resource.ScopeUser, ScopeID: "jane@example.com", Path: "drafts",
		Filename: "flow.star", URI: "mcp://user/jane@example.com/drafts/flow.star",
		S3Key: "k", SizeBytes: int64(len(source)), UploaderEmail: "jane@example.com",
	}
	reader := scriptsrc.New(sourceLibrary{file: file}, sourceBlobs{body: source}, "b", "")
	return New(Config{Store: store, AdminPersona: "admin", Sources: reader, Pending: pending}), store, pending
}

// bigFlow is a 45 KB script whose test fails as written.
func bigFlow() string {
	var b strings.Builder
	for i := range 900 {
		_, _ = fmt.Fprintf(&b, "STEP_%03d = \"%s\"\n", i, strings.Repeat("x", 36))
	}
	_, _ = b.WriteString(inMain("print(STEP_000)\n"))
	_, _ = b.WriteString("\ndef test_main():\n    \"\"\"Runs main.\"\"\"\n    main()\n    assert.eq(testing.outputs().log, \"wrong\\n\")\n")
	return b.String()
}

// TestARefusedCreateIsFixedWithASmallPatch is #2114's criterion: a 45 KB
// script is created from a stored file, the failing test in it is fixed with
// a small patch to the kept create, and the save goes through, with no
// full-source resend.
func TestARefusedCreateIsFixedWithASmallPatch(t *testing.T) {
	source := bigFlow()
	require.Greater(t, len(source), 45000)
	h, store, pending := pendingHandle(source)

	refused := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "flow", DisplayName: "Flow", SourceRef: "mcp:resource:src1",
	}))
	require.Equal(t, "invalid", refused[fieldStatus])
	assert.NotContains(t, refused, "formatted_source", "a refusal does not echo the source")
	assert.Contains(t, refused["pending"], "command=patch name=flow")
	assert.Empty(t, store.scripts)
	require.Len(t, pending.creates, 1)

	got := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "flow"}))
	assert.Equal(t, true, got["pending"])
	assert.Contains(t, got[fieldSource], "STEP_899")

	dry := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdPatch, Name: "flow", DryRun: true,
		Edits: []textpatch.Edit{{Op: "replace", Find: `"wrong\n"`, Replace: `"` + strings.Repeat("x", 36) + `\n"`}},
	}))
	assert.Equal(t, true, dry["pending"])
	assert.Empty(t, store.scripts, "a dry run saves nothing")

	saved := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdPatch, Name: "flow",
		Edits: []textpatch.Edit{{Op: "replace", Find: `"wrong\n"`, Replace: `"` + strings.Repeat("x", 36) + `\n"`}},
	})
	fields := resultFields(t, saved)
	require.Equal(t, "created", fields[fieldStatus], resultText(saved))
	require.Len(t, store.scripts, 1)
	for _, sc := range store.scripts {
		assert.Equal(t, "Flow", sc.DisplayName, "the create keeps what it was sent with")
	}
	assert.Empty(t, pending.creates, "a saved name keeps no pending create")
}

func TestARefusedPatchOfAPendingCreateKeepsThePatchedSource(t *testing.T) {
	h, store, pending := pendingHandle(bigFlow())
	call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "flow", SourceRef: "mcp:resource:src1"})
	res := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdPatch, Name: "flow",
		Edits: []textpatch.Edit{{Op: "replace", Find: `"wrong\n"`, Replace: `"still wrong\n"`}},
	}))
	assert.Equal(t, "invalid", res[fieldStatus])
	assert.Empty(t, store.scripts)
	assert.Contains(t, pending.creates["jane@example.com/flow"].Source, "still wrong")

	bad := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdPatch, Name: "flow", Edits: []textpatch.Edit{{Op: "replace", Find: "no such text", Replace: "x"}},
	})
	assert.True(t, bad.IsError, "an anchor that is not there is refused as a saved script's would be")
}

func TestSourceRefRefusals(t *testing.T) {
	h, _, _ := pendingHandle("print(1)\n")
	both := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "x", Source: "print(1)\n", SourceRef: "mcp:resource:src1",
	})
	assert.Contains(t, resultText(both), "not both")
	missing := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "x", SourceRef: "mcp:resource:gone"})
	assert.Contains(t, resultText(missing), scriptsrc.ErrNotFound.Error())
	updateMissing := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "x", SourceRef: "mcp:resource:gone"})
	assert.True(t, updateMissing.IsError)

	noLibrary, _ := newHandle()
	res := call(t, noLibrary, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "x", SourceRef: "mcp:resource:src1"})
	assert.Contains(t, resultText(res), "no managed-resource library")
}

// TestUpdateFromASourceRef: an update saves the file's text as the next
// version, as it would the source sent inline.
func TestUpdateFromASourceRef(t *testing.T) {
	h, store, _ := pendingHandle(tested(inMain("print(\"from the file\")\n")))
	createDaily(t, h)
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", SourceRef: "mcp:resource:src1", ChangeSummary: "prints from the file",
		UserAgreed: true,
	})
	require.False(t, res.IsError, resultText(res))
	for _, sc := range store.scripts {
		assert.Contains(t, sc.Source, "from the file")
	}
}

// failingPending is a pending store that cannot keep anything.
type failingPending struct{}

func (failingPending) Put(context.Context, string, scriptpending.Create) error {
	return errors.New("db down")
}

func (failingPending) Get(context.Context, string, string) (*scriptpending.Create, error) {
	return nil, errors.New("db down")
}

func (failingPending) Delete(context.Context, string, string) error { return errors.New("db down") }

func TestAPendingStoreFailureLeavesTheRefusalStanding(t *testing.T) {
	h := New(Config{Store: newMemStore(), AdminPersona: "admin", Pending: failingPending{}})
	res := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "flow", Source: tested(inMain("print(1)\n")) + "\nX = undefined_name\n",
	}))
	assert.NotContains(t, res, "pending")
	got := call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "flow"})
	assert.True(t, got.IsError, "a pending store that cannot answer leaves get to say the script is not there")
	created := resultFields(t, createDaily(t, h))
	assert.Equal(t, "created", created[fieldStatus], "a failed cleanup does not fail a save")
}

// TestRunDraftTimeoutIsAnAdministratorsToRaise is #2117's rule at the tool:
// a draft past 60 seconds is refused to anyone but an administrator, and to
// an administrator past the deployment's cap.
func TestRunDraftTimeoutIsAnAdministratorsToRaise(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	h := New(Config{Store: newMemStore(), DB: db, AdminPersona: "admin", DraftLimit: scriptbgdraft.Config{MaxSeconds: 600}})
	createDaily(t, h)
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdRunDraft, Name: "daily", TimeoutSeconds: 120})
	assert.Contains(t, resultText(res), "administrator's to run")

	createAdmins(t, h)
	res = call(t, h, adminCtx(), manageScriptInput{Command: cmdRunDraft, Name: "shared", TimeoutSeconds: 900})
	assert.Contains(t, resultText(res), "at most 600")

	mock.ExpectExec("DELETE FROM script_draft_runs").WillReturnError(errors.New("down"))
	res = call(t, h, adminCtx(), manageScriptInput{Command: cmdRunDraft, Name: "shared", TimeoutSeconds: 300})
	assert.True(t, res.IsError, "a draft that could not be recorded is not started: %s", resultText(res))

	help := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdHelp}))
	limits, _ := help["limits"].(map[string]any)
	assert.EqualValues(t, 600, limits["draft_max_seconds"])

	noDB := New(Config{Store: newMemStore(), AdminPersona: "admin"})
	createAdmins(t, noDB)
	res = call(t, noDB, adminCtx(), manageScriptInput{Command: cmdRunDraft, Name: "shared", TimeoutSeconds: 300})
	assert.Contains(t, resultText(res), "at most 60", "with nowhere to keep a long draft, none is offered")
}
