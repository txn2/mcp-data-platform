package resourceupload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// manyFiles is a link an external service delivers several files to (#2844).
func manyFiles(n int) IssueRequest {
	return IssueRequest{Scope: resource.ScopeUser, ScopeID: "u-1", Path: "renders", ContentType: "application/zip", MaxBytes: 100, MaxFiles: n}
}

// TestUpload_SeveralFilesNamedInTheURL: a link issued with no filename and
// max_files takes that many files, each named by the URL, outlives an hour,
// and refuses one more; a PUT that files nothing gives its place back, so the
// service sends it again.
func TestUpload_SeveralFilesNamedInTheURL(t *testing.T) {
	f := newService()
	in := manyFiles(2)
	in.TTL = 20 * time.Hour
	issued, err := f.svc.Issue(context.Background(), in, person, "u-1")
	require.NoError(t, err)
	assert.True(t, issued.NameInURL)
	assert.Equal(t, 2, issued.MaxFiles)
	assert.Equal(t, at.Add(20*time.Hour), issued.ExpiresAt)
	tok := tokenOf(t, issued.URL)

	*f.now = at.Add(19 * time.Hour)
	_, _, err = f.svc.Upload(context.Background(), tok, "", bytes.NewReader(zipBytes(20)), 20)
	require.ErrorIs(t, err, ErrBadName, "a link with no filename needs one in the URL")
	_, _, err = f.svc.Upload(context.Background(), tok, "../a.zip", bytes.NewReader(zipBytes(20)), 20)
	require.ErrorIs(t, err, ErrBadName, "a name that is not a plain file name")
	_, _, err = f.svc.Upload(context.Background(), tok, "big.zip", bytes.NewReader(zipBytes(200)), 200)
	require.ErrorIs(t, err, ErrTooLarge)

	res, remaining, err := f.svc.Upload(context.Background(), tok, "img-1.zip", bytes.NewReader(zipBytes(20)), 20)
	require.NoError(t, err, "three refused PUTs took no place")
	assert.Equal(t, "img-1.zip", res.Filename)
	assert.Equal(t, 1, remaining)
	_, remaining, err = f.svc.Upload(context.Background(), tok, "img-2.zip", bytes.NewReader(zipBytes(20)), 20)
	require.NoError(t, err)
	assert.Zero(t, remaining)
	_, _, err = f.svc.Upload(context.Background(), tok, "img-3.zip", bytes.NewReader(zipBytes(20)), 20)
	require.ErrorIs(t, err, ErrNoLink, "a full link")
	require.Len(t, f.w.created, 2)
	assert.Equal(t, []string{"img-1.zip", "img-2.zip"}, []string{f.w.created[0].Filename, f.w.created[1].Filename})
	assert.Equal(t, "img-2.zip", f.w.created[1].DisplayName, "each file of a link for several is named for itself")
	assert.Equal(t, "renders", f.w.created[1].Path)
}

// TestUpload_ALinkWithAFilenameTakesThatName: a URL naming another file is
// refused, and one naming the link's own file is filed.
func TestUpload_ALinkWithAFilenameTakesThatName(t *testing.T) {
	f := newService()
	issued, err := f.svc.Issue(context.Background(), request(100), person, "u-1")
	require.NoError(t, err)
	assert.False(t, issued.NameInURL)
	tok := tokenOf(t, issued.URL)
	_, _, err = f.svc.Upload(context.Background(), tok, "other.zip", bytes.NewReader(zipBytes(20)), 20)
	require.ErrorIs(t, err, ErrBadName)
	assert.Contains(t, err.Error(), `issued for "site.zip"`)
	_, _, err = f.svc.Upload(context.Background(), tok, "site.zip", bytes.NewReader(zipBytes(20)), 20)
	require.NoError(t, err)
	require.Len(t, f.w.created, 1)
	assert.Equal(t, "site.zip", f.w.created[0].DisplayName, "a link issued with no display name names the file for itself")
	assert.Equal(t, linkDescription, f.w.created[0].Description, "and describes it as filed through the link")
}

// TestOpenedAndRevoke: a person sees the links they made that can still take
// a file, never another's, and a revoked link takes nothing more.
func TestOpenedAndRevoke(t *testing.T) {
	f := newService()
	mine, err := f.svc.Issue(context.Background(), manyFiles(3), person, "u-1")
	require.NoError(t, err)
	other := resource.Claims{Sub: "u-2", Email: "bo@example.com"}
	_, err = f.svc.Issue(context.Background(), IssueRequest{
		Scope: resource.ScopeUser, ScopeID: "u-2", Path: "x", ContentType: "text/plain", MaxBytes: 1,
	}, other, "u-2")
	require.NoError(t, err)

	open, err := f.svc.Opened(context.Background(), person)
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, mine.ID, open[0].ID)

	require.ErrorIs(t, f.svc.Revoke(context.Background(), mine.ID, other), ErrNoLink, "another person's link")
	require.NoError(t, f.svc.Revoke(context.Background(), mine.ID, person))
	_, _, err = f.svc.Upload(context.Background(), tokenOf(t, mine.URL), "a.zip", bytes.NewReader(zipBytes(20)), 20)
	require.ErrorIs(t, err, ErrNoLink, "a revoked link")
	open, err = f.svc.Opened(context.Background(), person)
	require.NoError(t, err)
	assert.Empty(t, open)
}

// TestPut_NamedRoute: the route a link with no filename is PUT to files the
// named file and says how many more the link takes; a missing name is 400.
func TestPut_NamedRoute(t *testing.T) {
	svc := newService().svc
	mux := http.NewServeMux()
	svc.Mount(mux)
	issued, err := svc.Issue(context.Background(), manyFiles(2), person, "u-1")
	require.NoError(t, err)
	put := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			strings.TrimPrefix(path, "https://platform.example.com"), bytes.NewReader(zipBytes(20))))
		return rec
	}
	rec := put(issued.URL)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "followed by")
	rec = put(issued.URL + "/out.zip")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"files_remaining":1`)
	assert.Contains(t, rec.Body.String(), `"filename":"out.zip"`)
}

// failingExtract refuses as a ClaimsExtractor does.
type refusal struct{}

func (refusal) Error() string   { return "refused" }
func (refusal) Refusal() string { return "your persona reads Resources and uploads nothing" }

// TestPortal: the Resources page makes a link as the caller, lists the
// caller's open links without their tokens, and revokes one; a caller who is
// not signed in, a persona that cannot upload, a bad body and a refused link
// are answered.
func TestPortal(t *testing.T) {
	f := newService()
	var who *resource.Claims
	var whoErr error
	mux := http.NewServeMux()
	f.svc.MountPortal(mux, func(*http.Request) (*resource.Claims, error) { return who, whoErr })
	(*Service)(nil).MountPortal(mux, nil)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
		return rec
	}
	p := person
	who = &p

	rec := call(http.MethodPost, PortalRoute, `{"scope":"user","path":"renders","content_type":"image/png","size_bytes":1000,"max_files":4,"ttl_seconds":7200}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var issued Issued
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issued))
	assert.True(t, issued.NameInURL)
	assert.Equal(t, 4, issued.MaxFiles)
	assert.Equal(t, at.Add(2*time.Hour), issued.ExpiresAt)
	token := tokenOf(t, issued.URL)

	rec = call(http.MethodGet, PortalRoute, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), issued.ID)
	assert.NotContains(t, rec.Body.String(), token, "a listed link never carries its token")
	assert.NotContains(t, rec.Body.String(), Hash(token))

	assert.Equal(t, http.StatusBadRequest, call(http.MethodPost, PortalRoute, `not json`).Code)
	rec = call(http.MethodPost, PortalRoute, `{"path":"renders","content_type":"image/png","size_bytes":1000,"ttl_seconds":90000}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "at most 86400 seconds")
	assert.NotContains(t, rec.Body.String(), ": refused")

	assert.Equal(t, http.StatusNoContent, call(http.MethodPost, PortalRoute+"/"+issued.ID+"/revoke", "").Code)
	assert.Equal(t, http.StatusNotFound, call(http.MethodPost, PortalRoute+"/"+issued.ID+"/revoke", "").Code)

	who, whoErr = nil, errors.New("no session")
	assert.Equal(t, http.StatusUnauthorized, call(http.MethodGet, PortalRoute, "").Code)
	whoErr = refusal{}
	rec = call(http.MethodPost, PortalRoute, `{}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "uploads nothing")
	whoErr = resource.ErrForbidden
	assert.Equal(t, http.StatusForbidden, call(http.MethodPost, PortalRoute, `{}`).Code)

	who, whoErr = &p, nil
	f.store.err = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, call(http.MethodGet, PortalRoute, "").Code)
	assert.Equal(t, http.StatusInternalServerError,
		call(http.MethodPost, PortalRoute, `{"path":"renders","content_type":"image/png","size_bytes":1000}`).Code)
	assert.Equal(t, http.StatusInternalServerError, call(http.MethodPost, PortalRoute+"/any/revoke", "").Code)
}

// portalMounter is a toolkit that mounts the portal's link routes.
type portalMounter struct{ s *Service }

func (m portalMounter) MountUploadLinkPortal(mux *http.ServeMux, extract resource.ClaimsExtractor) {
	m.s.MountPortal(mux, extract)
}

// TestMountPortalFrom: the routes are mounted from the toolkit that issues
// links, and a toolkit that does not is passed over.
func TestMountPortalFrom(t *testing.T) {
	svc := newService().svc
	mux := http.NewServeMux()
	p := person
	MountPortalFrom(mux, []any{struct{}{}, portalMounter{svc}}, func(*http.Request) (*resource.Claims, error) { return &p, nil })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, PortalRoute, http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code)
}
