package resource

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/grantsource"
)

// grantSource answers fixed folders per grantee address or persona, and
// counts the reads.
type fakeGrants struct {
	folders map[string][]string
	reads   int
	err     error
	asked   []grantsource.Grantee
}

func (g *fakeGrants) WritableFolders(_ context.Context, who grantsource.Grantee) ([]string, error) {
	g.reads++
	g.asked = append(g.asked, who)
	if g.err != nil {
		return nil, g.err
	}
	out := append([]string{}, g.folders[who.Email]...)
	for _, p := range who.Personas {
		out = append(out, g.folders["persona:"+p]...)
	}
	return out, nil
}

func installGrants(t *testing.T, src grantsource.Source) {
	t.Helper()
	grantsource.Install(src)
	t.Cleanup(func() { grantsource.Install(nil) })
}

func TestUnderSQLAndParent(t *testing.T) {
	for _, tc := range []struct {
		path, folder string
		want         bool
	}{
		{"reports", "reports", true},
		{"reports/q3", "reports", true},
		{"reports/q3/drafts", "reports", true},
		{"reports2", "reports", false},
		{"reports-old", "reports", false},
		{"report", "reports", false},
		{"finance/reports", "reports", false},
		{"anything", "", true},
	} {
		assert.Equal(t, tc.want, PathUnder(tc.path, tc.folder), "%q under %q", tc.path, tc.folder)
	}
	assert.Equal(t, "", parentFolder("reports"))
	assert.Equal(t, "reports", parentFolder("reports/q3"))
	assert.Equal(t, "(p = f OR left(p, length(f) + 1) = f || '/')", grantsource.UnderSQL("p", "f"))
}

// TestCanWriteFolder_AGrantCoversItsSubtree: a person granted write on Global
// "reports" writes it and everything beneath it, and nothing else in Global;
// another library is untouched; a person kept from Global writes nothing.
func TestCanWriteFolder_AGrantCoversItsSubtree(t *testing.T) {
	src := &fakeGrants{folders: map[string][]string{"jane@example.com": {"reports"}}}
	installGrants(t, src)
	jane := Claims{Sub: "sub-jane", Email: "jane@example.com"}.WithFolderGrants(context.Background())
	for path, want := range map[string]bool{
		"reports": true, "reports/q3": true, "reports/q3/drafts": true,
		"reports2": false, "reports-old": false, "finance": false, "": false,
	} {
		assert.Equal(t, want, jane.CanWriteFolder(ScopeGlobal, "", path), path)
	}
	assert.False(t, jane.CanWriteFolder(ScopePersona, "finance", "reports"), "a grant is on Global only")
	assert.False(t, CanWriteScope(jane, ScopeGlobal, ""), "a grant does not open the whole library")
	assert.Equal(t, 1, src.reads, "the grants are read once per claims")
	assert.Equal(t, []string{"reports"}, jane.GrantedFolders())

	kept := jane
	kept.NoGlobal = true
	assert.False(t, kept.CanWriteFolder(ScopeGlobal, "", "reports"), "an external person writes no Global folder")
	assert.Equal(t, []string{}, kept.GrantedFolders())
	assert.False(t, Claims{Email: "bob@example.com"}.WithFolderGrants(context.Background()).CanWriteFolder(ScopeGlobal, "", "reports"))
}

// TestCanWriteFolder_APersonaGrant reaches whoever resolves to the persona.
func TestCanWriteFolder_APersonaGrant(t *testing.T) {
	installGrants(t, &fakeGrants{folders: map[string][]string{"persona:finance": {"finance/close"}}})
	member := Claims{Sub: "s", Email: "a@example.com", Personas: []string{"finance"}}.WithFolderGrants(context.Background())
	other := Claims{Sub: "t", Email: "b@example.com", Personas: []string{"sales"}}.WithFolderGrants(context.Background())
	assert.True(t, member.CanWriteFolder(ScopeGlobal, "", "finance/close/2026"))
	assert.False(t, other.CanWriteFolder(ScopeGlobal, "", "finance/close/2026"))
	// Every persona the caller's roles reach is asked, in any order, so a
	// grant to one of them is honored on every request.
	for _, order := range [][]string{{"sales", "finance"}, {"finance", "sales"}} {
		both := Claims{Sub: "u", Email: "c@example.com", Personas: order}.WithFolderGrants(context.Background())
		assert.True(t, both.CanWriteFolder(ScopeGlobal, "", "finance/close"), "personas %v", order)
	}
}

// TestWithFolderGrants_ARunHoldsItsAuthorsGrants: a caller acting for a person
// is looked up as that person, never as the principal.
func TestWithFolderGrants_ARunHoldsItsAuthorsGrants(t *testing.T) {
	src := &fakeGrants{folders: map[string][]string{"jane@example.com": {"reports"}}}
	installGrants(t, src)
	run := Claims{Sub: "script:nightly", Email: "owner@example.com"}.ActingFor("jane@example.com", "sub-jane").WithFolderGrants(context.Background())
	assert.True(t, run.CanWriteFolder(ScopeGlobal, "", "reports/x"))
	require.Len(t, src.asked, 1)
	assert.Equal(t, grantsource.Grantee{Subject: "sub-jane", Email: "jane@example.com"}, src.asked[0])
}

// TestWithFolderGrants_AFailedReadGrantsNothing, and no source grants nothing.
func TestWithFolderGrants_AFailedReadGrantsNothing(t *testing.T) {
	installGrants(t, &fakeGrants{err: errors.New("down")})
	c := Claims{Email: "jane@example.com"}.WithFolderGrants(context.Background())
	assert.False(t, c.CanWriteFolder(ScopeGlobal, "", "reports"))
	grantsource.Install(nil)
	none := Claims{Email: "jane@example.com"}.WithFolderGrants(context.Background())
	assert.Nil(t, none.folderWrite)
	assert.Equal(t, []string{}, none.GrantedFolders())
}

// TestCanModifyResource_AGrantedFolderFile: a file in a covered folder is the
// grantee's to change and delete; one outside is not.
func TestCanModifyResource_AGrantedFolderFile(t *testing.T) {
	installGrants(t, &fakeGrants{folders: map[string][]string{"jane@example.com": {"reports"}}})
	jane := Claims{Sub: "sub-jane", Email: "jane@example.com"}.WithFolderGrants(context.Background())
	in := &Resource{ID: "r1", Scope: ScopeGlobal, Path: "reports/q3"}
	out := &Resource{ID: "r2", Scope: ScopeGlobal, Path: "finance"}
	assert.True(t, CanModifyResource(jane, in))
	assert.True(t, CanEditResource(jane, in))
	assert.False(t, CanModifyResource(jane, out))
}

// TestResolveMove_AGranteeStaysInsideTheGrant: a grantee moves a file within
// what they can write, and is refused moving it to another Global folder or
// into Global's root, even a file they uploaded.
func TestResolveMove_AGranteeStaysInsideTheGrant(t *testing.T) {
	installGrants(t, &fakeGrants{folders: map[string][]string{"jane@example.com": {"reports"}}})
	jane := Claims{Sub: "sub-jane", Email: "jane@example.com"}.WithFolderGrants(context.Background())
	res := &Resource{ID: "r1", Scope: ScopeGlobal, Path: "reports", Filename: "a.csv", URI: "mcp://global/reports/a.csv", UploaderSub: "sub-jane"}
	_, _, err := checkRelocation(&jane, res, Destination{Scope: ScopeGlobal, Path: "reports/2026"}, "")
	require.NoError(t, err)
	_, _, err = checkRelocation(&jane, res, Destination{Scope: ScopeGlobal, Path: "finance"}, "")
	require.ErrorIs(t, err, ErrMoveForbidden, "out of the grant, even for the file's uploader")
}

// TestFolderOperations_TheGrantedFolderIsTheAdministrators: a grantee makes,
// deletes and moves folders beneath the granted one, and never the granted
// folder itself.
func TestFolderOperations_TheGrantedFolderIsTheAdministrators(t *testing.T) {
	installGrants(t, &fakeGrants{folders: map[string][]string{"jane@example.com": {"reports"}}})
	jane := Claims{Sub: "sub-jane", Email: "jane@example.com"}.WithFolderGrants(context.Background())
	global := ScopeFilter{Scope: ScopeGlobal}
	folders := &fakeFolders{exists: true}
	deps := Deps{Folders: folders, Store: newFolderStore()}

	require.NoError(t, CreateFolder(context.Background(), deps, &jane, NewFolder{Library: global, Path: "reports/q3"}))
	require.ErrorIs(t, CreateFolder(context.Background(), deps, &jane, NewFolder{Library: global, Path: "finance2"}), ErrMoveForbidden)
	require.ErrorIs(t, DeleteFolder(context.Background(), deps, &jane, NewFolder{Library: global, Path: "reports"}), ErrMoveForbidden)
	_, err := MoveFolder(context.Background(), deps, &jane, FolderRename{Library: global, From: "reports", To: "reporting"})
	require.ErrorIs(t, err, ErrMoveForbidden, "the granted folder is not renamed by its grantee")
	_, err = MoveFolder(context.Background(), deps, &jane, FolderRename{Library: global, From: "reports/q3", To: "finance/q3"})
	require.ErrorIs(t, err, ErrMoveForbidden, "out of the grant")
	_, err = MoveFolder(context.Background(), deps, &jane, FolderRename{Library: global, From: "reports/q3", To: "reports/2026/q3"})
	require.NoError(t, err, "inside the grant")
	assert.Len(t, folders.moved, 1)
}

// TestMoveFolder_AnEmptyFolderIsNotRenamedByAReader: before #2684 an empty
// stored folder moved for anyone who could see its library.
func TestMoveFolder_AnEmptyFolderIsNotRenamedByAReader(t *testing.T) {
	reader := Claims{Sub: "sub-r", Email: "r@example.com"}
	folders := &fakeFolders{exists: true}
	_, err := MoveFolder(context.Background(), Deps{Folders: folders, Store: newFolderStore()}, &reader,
		FolderRename{Library: ScopeFilter{Scope: ScopeGlobal}, From: "brand", To: "brand-old"})
	require.ErrorIs(t, err, ErrMoveForbidden)
	assert.Empty(t, folders.moved)
}

// TestMyGrantsRoute: the caller reads the Global folders their grants cover,
// [] for none.
func TestMyGrantsRoute(t *testing.T) {
	installGrants(t, &fakeGrants{folders: map[string][]string{"jane@example.com": {"reports"}}})
	jane := Claims{Sub: "sub-jane", Email: "jane@example.com"}.WithFolderGrants(context.Background())
	h := NewHandler(Deps{Store: newFolderStore()}, func(*http.Request) (*Claims, error) { return &jane, nil }, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/folder-grants/mine", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"folders":["reports"]}`, rec.Body.String())

	none := Claims{Email: "bob@example.com"}
	h = NewHandler(Deps{Store: newFolderStore()}, func(*http.Request) (*Claims, error) { return &none, nil }, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/folder-grants/mine", nil))
	assert.JSONEq(t, `{"folders":[]}`, rec.Body.String(), "[] never null")
}
