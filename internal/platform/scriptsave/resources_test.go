package scriptsave

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptres"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// pageLibrary is a managed-resource library holding Jane's page script at its
// second version.
type pageLibrary struct{ resource.Store }

var page = &resource.Resource{
	ID: "res_page", Scope: resource.ScopeUser, ScopeID: "jane@example.com", Path: "flows/sync",
	Filename: "page.js", URI: "mcp://user/jane@example.com/flows/sync/page.js", S3Key: "v2", SizeBytes: 22,
	UploaderEmail: "jane@example.com",
}

func (pageLibrary) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	if uri == page.URI {
		return page, nil
	}
	return nil, fmt.Errorf("scanning: %w", sql.ErrNoRows)
}

func (pageLibrary) AddRevision(context.Context, resource.Revision) (*resource.Version, error) {
	return nil, errors.New("not used")
}

func (pageLibrary) ListVersions(context.Context, string) ([]resource.Version, error) {
	return []resource.Version{{Version: 2, S3Key: "v2", SizeBytes: 22}}, nil
}

func (pageLibrary) GetVersion(_ context.Context, _ string, v int) (*resource.Version, error) {
	return &resource.Version{Version: v, S3Key: "v2", SizeBytes: 22}, nil
}

func (pageLibrary) PruneVersions(context.Context, string, int) ([]resource.Version, error) {
	return nil, nil
}

type pageBlobs struct{ resource.S3Client }

func (pageBlobs) GetObject(context.Context, string, string) (body []byte, contentType string, err error) {
	return []byte("return document.title;"), "text/javascript", nil
}

const pageModule = "res:user/jane@example.com/flows/sync/page.js"

// loadsThePage is a script whose page script is its own file, with a test
// over the text it loads.
const loadsThePage = `load("` + pageModule + `", PAGE_JS = "text")

def main():
    """Reports how long the page script is."""
    platform.result({"length": len(PAGE_JS)})

def test_main():
    """Reads the page script the file holds."""
    main()
    assert.eq(testing.outputs().result, {"length": 22})
`

// TestASavePinsTheFilesItLoads is #2113 at the gate: the save reads each file
// a load names under the saver's access, its tests read the same text, and
// the version is given the file version they read.
func TestASavePinsTheFilesItLoads(t *testing.T) {
	g := &Gate{Recordings: &store{}, Resources: scriptsrc.New(pageLibrary{}, pageBlobs{}, "b", "")}
	res := g.Check(context.Background(), Request{Name: "flow", Source: loadsThePage, Caller: jane})
	require.Empty(t, res.Refusal)
	assert.Equal(t, map[string]script.ResourcePin{pageModule: {ResourceID: "res_page", Version: 2}}, res.ResourcePins)

	var sc script.Script
	res.Apply(&sc)
	assert.Equal(t, res.ResourcePins, sc.ResourcePins, "the version keeps what the save pinned")
}

func TestASaveIsRefusedAFileItCannotRead(t *testing.T) {
	g := &Gate{Recordings: &store{}, Resources: scriptsrc.New(pageLibrary{}, pageBlobs{}, "b", "")}
	bob := Caller{Email: "bob@example.com"}
	res := g.Check(context.Background(), Request{Name: "flow", Source: loadsThePage, Caller: bob})
	assert.Contains(t, res.Refusal, "a load names a file that cannot be read")

	none := &Gate{Recordings: &store{}}
	res = none.Check(context.Background(), Request{Name: "flow", Source: loadsThePage, Caller: jane})
	assert.Contains(t, res.Refusal, "no managed-resource library")
}

// editedPage is the page library after the page script was edited: version 1
// is what an earlier run read, version 2 what it holds now.
type editedPage struct{ pageLibrary }

func (editedPage) Get(_ context.Context, id string) (*resource.Resource, error) {
	if id == page.ID {
		return page, nil
	}
	return nil, fmt.Errorf("scanning: %w", sql.ErrNoRows)
}

func (editedPage) ListVersions(context.Context, string) ([]resource.Version, error) {
	return []resource.Version{{Version: 2, S3Key: "v2", SizeBytes: 22}, {Version: 1, S3Key: "v1", SizeBytes: 9}}, nil
}

func (editedPage) GetVersion(_ context.Context, _ string, v int) (*resource.Version, error) {
	return &resource.Version{Version: v, S3Key: fmt.Sprintf("v%d", v), SizeBytes: 22}, nil
}

type editedBlobs struct{ resource.S3Client }

func (editedBlobs) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	return []byte(map[string]string{"v1": "return 1;", "v2": "return document.title;"}[key]), "text/javascript", nil
}

// TestSavingAgainAfterTheFileChangedIsABehaviorChange is #2113's replay at
// the save: the saved version replays its recorded run reading the file
// version that run read, so saving the same source again after the file was
// edited reports what the edit changed, and needs the person's agreement.
func TestSavingAgainAfterTheFileChangedIsABehaviorChange(t *testing.T) {
	reader := scriptsrc.New(editedPage{}, editedBlobs{}, "b", "")
	st := &store{}
	ran := map[string]script.ResourcePin{pageModule: {ResourceID: page.ID, Version: 1}}
	rec := scriptrec.NewRecorder(scriptrec.Header{RunID: "run_7", MaxRows: scriptrun.DraftMaxRows, Preview: true, Files: ran})
	_, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: loadsThePage, Name: "flow", RunID: "run_7", Caller: upstream{}, OnCall: rec.OnCall,
		Libraries: scriptres.With(nil, reader, ran, nil),
	})
	require.NoError(t, err)
	require.True(t, rec.SaveTo(context.Background(), st, scriptrec.Meta{
		RunID: "run_7", ScriptID: "s2", Kind: scriptrec.KindRun, RecordedBy: "jane@example.com", Succeeded: true,
	}))

	g := &Gate{Recordings: st, Resources: reader}
	existing := &script.Script{ID: "s2", Name: "flow", OwnerEmail: "jane@example.com", Source: loadsThePage}
	res := g.Check(context.Background(), Request{Existing: existing, Name: "flow", Source: loadsThePage, Caller: jane})
	require.True(t, res.ChangeNeeded, "the edited file changes what the script hands back: %+v", res)
	assert.Equal(t, []string{"run_7"}, res.Replayed)
	assert.Equal(t, map[string]script.ResourcePin{pageModule: {ResourceID: page.ID, Version: 2}}, res.ResourcePins)

	// A source that loads no file and has not changed is not compared.
	plain := &script.Script{ID: "s2", Name: "flow", OwnerEmail: "jane@example.com", Source: weekly + replays}
	record(t, st, "run_1", "", weekly)
	res = g.Check(context.Background(), Request{Existing: plain, Name: "flow", Source: weekly + replays, Caller: jane})
	assert.Empty(t, res.Replayed)
}
