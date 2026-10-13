package scriptres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// library holds one page script with two versions.
type library struct {
	resource.Store
	file *resource.Resource
}

func (l *library) Get(_ context.Context, id string) (*resource.Resource, error) {
	if id == l.file.ID {
		return l.file, nil
	}
	return nil, fmt.Errorf("scanning: %w", sql.ErrNoRows)
}

func (l *library) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	if uri == l.file.URI {
		return l.file, nil
	}
	return nil, fmt.Errorf("scanning: %w", sql.ErrNoRows)
}

func (*library) AddRevision(context.Context, resource.Revision) (*resource.Version, error) {
	return nil, errors.New("not used")
}

func (*library) ListVersions(context.Context, string) ([]resource.Version, error) {
	return []resource.Version{{Version: 2, S3Key: "v2", SizeBytes: 9}, {Version: 1, S3Key: "v1", SizeBytes: 9}}, nil
}

func (*library) GetVersion(_ context.Context, _ string, v int) (*resource.Version, error) {
	return &resource.Version{Version: v, S3Key: fmt.Sprintf("v%d", v), SizeBytes: 9}, nil
}

func (*library) PruneVersions(context.Context, string, int) ([]resource.Version, error) {
	return nil, nil
}

type blobs struct{ resource.S3Client }

func (blobs) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	return []byte(map[string]string{"v1": "oldPage()", "v2": "newPage()"}[key]), "text/javascript", nil
}

const module = "res:global/flows/sync/page.js"

func reader() *scriptsrc.Reader {
	return scriptsrc.New(&library{file: &resource.Resource{
		ID: "r1", Scope: resource.ScopeGlobal, Path: "flows/sync", Filename: "page.js",
		URI: "mcp://global/flows/sync/page.js", S3Key: "v2", SizeBytes: 9,
	}}, blobs{}, "b", "")
}

var anyone = resource.BuildClaims("u", "u@example.com", "analyst", []string{"analyst"}, false)

// textOf reads module through src as a run does.
func textOf(t *testing.T, src scriptlib.Source, module string) (string, error) {
	t.Helper()
	rs, ok := src.(scriptlib.ResourceSource)
	require.True(t, ok, "the source reads no res: modules")
	text, err := rs.ResourceText(context.Background(), module)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", module, err)
	}
	return text, nil
}

func TestURI(t *testing.T) {
	assert.Equal(t, "mcp://global/flows/sync/page.js", URI(module, "mcp"))
}

func TestPinResolvesEveryLoadAtItsHead(t *testing.T) {
	source := "load(\"" + module + "\", PAGE_JS = \"text\")\n\ndef main():\n    print(PAGE_JS)\n"
	pins, problems := Pin(context.Background(), reader(), source, anyone)
	require.Empty(t, problems)
	assert.Equal(t, map[string]script.ResourcePin{module: {ResourceID: "r1", Version: 2}}, pins)

	pins, problems = Pin(context.Background(), reader(), "def main():\n    print(1)\n", anyone)
	assert.Empty(t, problems)
	assert.Empty(t, pins)

	_, problems = Pin(context.Background(), nil, source, anyone)
	assert.Equal(t, []string{"this deployment has no managed-resource library to load a file from"}, problems)

	missing := "load(\"res:global/flows/sync/gone.js\", P = \"text\")\n\ndef main():\n    print(P)\n"
	_, problems = Pin(context.Background(), reader(), missing, anyone)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "gone.js")
}

// TestSourceReadsThePinnedVersion is #2113's replay criterion: a run of a
// version reads the file version its save pinned, though the file has moved
// on since.
func TestSourceReadsThePinnedVersion(t *testing.T) {
	src := With(nil, reader(), map[string]script.ResourcePin{module: {ResourceID: "r1", Version: 1}}, nil)
	rs, ok := src.(scriptlib.ResourceSource)
	require.True(t, ok)
	text, err := rs.ResourceText(context.Background(), module)
	require.NoError(t, err)
	assert.Equal(t, "oldPage()", text)

	_, err = rs.ResourceText(context.Background(), "res:global/flows/other.js")
	assert.ErrorContains(t, err, "save the script again to pin it", "a run reads only what its version pinned")

	_, err = src.LibrarySource(context.Background(), "x", 1)
	assert.ErrorIs(t, err, scriptlib.ErrNotFound, "no library store, no library")
}

func TestSourceReadsTheHeadForADraft(t *testing.T) {
	src := With(nil, reader(), nil, &anyone)
	text, err := textOf(t, src, module)
	require.NoError(t, err)
	assert.Equal(t, "newPage()", text)
	_, err = textOf(t, src, "res:global/x/gone.js")
	assert.ErrorIs(t, err, scriptsrc.ErrNotFound)
}

func TestWithoutALibrary(t *testing.T) {
	var libs scriptlib.Source
	assert.Nil(t, With(libs, nil, nil, nil), "no reader leaves the library source as it is")
	_, err := Source{}.ResourceText(context.Background(), module)
	assert.ErrorContains(t, err, "no managed-resource library")
}

// TestAReplayReadsTheVersionItsRunRead is #2113's replay criterion for a
// test: a recording keeps the version each load read, and a test replaying it
// reads that version though the file has moved on and the draft source reads
// the file as it is now.
func TestAReplayReadsTheVersionItsRunRead(t *testing.T) {
	draft := With(nil, reader(), Current(context.Background(), reader(), "load(\""+module+"\", P = \"text\")\n", anyone), &anyone)
	assert.Equal(t, map[string]script.ResourcePin{module: {ResourceID: "r1", Version: 2}}, PinsOf(draft),
		"a draft records the version it read")

	replay := Replaying(draft, map[string]script.ResourcePin{module: {ResourceID: "r1", Version: 1}})
	text, err := textOf(t, replay, module)
	require.NoError(t, err)
	assert.Equal(t, "oldPage()", text)
	text, err = textOf(t, draft, module)
	require.NoError(t, err)
	assert.Equal(t, "newPage()", text, "replaying leaves the source it was made from alone")

	other, err := textOf(t, replay, "res:global/x/gone.js")
	assert.Empty(t, other)
	assert.ErrorIs(t, err, scriptsrc.ErrNotFound, "a file the recording did not read is read as the source reads it")

	assert.Nil(t, PinsOf(nil))
	assert.Nil(t, Current(context.Background(), nil, "", anyone))
	var none scriptlib.Source
	assert.Nil(t, Replaying(none, map[string]script.ResourcePin{module: {}}), "nothing to replay through")
	assert.Equal(t, draft, Replaying(draft, nil))
}

// oneLibrary is a library store holding one version of one library.
type oneLibrary struct{}

func (oneLibrary) LibrarySource(_ context.Context, name string, version int) (string, error) {
	if name == "helpers" && version == 1 {
		return "def twice(x):\n    return 2 * x\n", nil
	}
	return "", scriptlib.ErrNotFound
}

func TestSourceReadsLibrariesThroughTheLibraryStore(t *testing.T) {
	src := With(oneLibrary{}, reader(), nil, &anyone)
	text, err := src.LibrarySource(context.Background(), "helpers", 1)
	require.NoError(t, err)
	assert.Contains(t, text, "def twice")
	_, err = src.LibrarySource(context.Background(), "helpers", 2)
	assert.ErrorIs(t, err, scriptlib.ErrNotFound)
}

// TestAReplayReadsAPinnedFileOnlyUnderTheCallersAccess: a test replaying a
// run reads the pinned file under the access of whoever runs the test.
func TestAReplayReadsAPinnedFileOnlyUnderTheCallersAccess(t *testing.T) {
	lib := &library{file: &resource.Resource{
		ID: "r1", Scope: resource.ScopeUser, ScopeID: "jane@example.com", Path: "flows/sync", Filename: "page.js",
		URI: "mcp://user/jane@example.com/flows/sync/page.js", S3Key: "v2", SizeBytes: 9, UploaderSub: "jane",
	}}
	r := scriptsrc.New(lib, blobs{}, "b", "")
	pins := map[string]script.ResourcePin{"res:user/jane@example.com/flows/sync/page.js": {ResourceID: "r1", Version: 1}}
	bob := resource.BuildClaims("bob", "bob@example.com", "analyst", []string{"analyst"}, false)
	_, err := textOf(t, With(nil, r, pins, &bob), "res:user/jane@example.com/flows/sync/page.js")
	require.ErrorIs(t, err, scriptsrc.ErrNotFound)

	text, err := textOf(t, With(nil, r, pins, nil), "res:user/jane@example.com/flows/sync/page.js")
	require.NoError(t, err)
	assert.Equal(t, "oldPage()", text, "a run reads what its version pinned")
}
