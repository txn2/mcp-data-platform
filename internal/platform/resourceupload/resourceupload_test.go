package resourceupload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/grantsource"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// memStore keeps links by hash, as the table does: a place taken per claim,
// given back by a finish that filed nothing.
type memStore struct {
	mu       sync.Mutex
	links    map[string]*link
	used     map[string]int
	revoked  map[string]bool
	finished map[string][2]string
	err      error
}

func newMemStore() *memStore {
	return &memStore{links: map[string]*link{}, used: map[string]int{}, revoked: map[string]bool{}, finished: map[string][2]string{}}
}

func (m *memStore) insert(_ context.Context, l link, sum string) error {
	if m.err != nil {
		return m.err
	}
	m.links[sum] = &l
	return nil
}

func (m *memStore) claim(_ context.Context, sum string, now time.Time) (*link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.links[sum]
	if l == nil || m.revoked[l.ID] || m.used[sum] >= max(l.MaxFiles, 1) || !l.ExpiresAt.After(now) {
		return nil, ErrNoLink
	}
	m.used[sum]++
	c := *l
	c.FilesUsed = m.used[sum]
	return &c, nil
}

func (m *memStore) finish(_ context.Context, id, resourceID, failure string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finished[id] = [2]string{resourceID, failure}
	if resourceID == "" {
		for sum, l := range m.links {
			if l.ID == id {
				m.used[sum]--
			}
		}
	}
	return nil
}

func (m *memStore) open(_ context.Context, userID, email string, now time.Time) ([]Open, error) {
	out := []Open{}
	for sum, l := range m.links {
		if (l.UserID == userID || (email != "" && l.UserEmail == email)) && !m.revoked[l.ID] &&
			m.used[sum] < max(l.MaxFiles, 1) && l.ExpiresAt.After(now) {
			out = append(out, Open{ID: l.ID, Path: l.Path, MaxFiles: l.MaxFiles, FilesUsed: m.used[sum], ExpiresAt: l.ExpiresAt})
		}
	}
	return out, m.err
}

func (m *memStore) revoke(_ context.Context, id string, by resource.Claims, _ time.Time) error {
	if m.err != nil {
		return m.err
	}
	for _, l := range m.links {
		if l.ID == id && !m.revoked[id] && (by.IsAdmin || l.UserID == by.Sub || (by.Email != "" && l.UserEmail == by.Email)) {
			m.revoked[id] = true
			return nil
		}
	}
	return ErrNoLink
}

func (*memStore) sweep(context.Context, time.Time) error { return nil }

// memWriter files what it is given, refusing a type the deny list refuses.
type memWriter struct {
	created  []resource.NewResource
	replaced []string
	existing *resource.Resource
	body     []byte
	claims   resource.Claims
}

func (w *memWriter) Create(_ context.Context, in resource.NewResource, claims resource.Claims) (*resource.Resource, error) {
	w.claims = claims
	b, err := io.ReadAll(in.Content)
	if err != nil {
		return nil, fmt.Errorf("reading: %w", err)
	}
	w.body, w.created = b, append(w.created, in)
	return &resource.Resource{ID: "r-new", Filename: in.Filename, MIMEType: in.MIMEType, SizeBytes: int64(len(b))}, nil
}

func (w *memWriter) Replace(_ context.Context, id string, up resource.RevisionUpload, _ resource.Claims) (*resource.Resource, int, error) {
	b, err := io.ReadAll(up.Content)
	if err != nil {
		return nil, 0, fmt.Errorf("reading: %w", err)
	}
	w.body, w.replaced = b, append(w.replaced, id)
	return &resource.Resource{ID: id, SizeBytes: int64(len(b))}, 2, nil
}

func (w *memWriter) Locate(context.Context, toolkit.ResourceAddress, resource.Claims) (*resource.Resource, string, error) {
	return w.existing, "", nil
}

var (
	at     = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	person = resource.Claims{Sub: "u-1", Email: "ann@example.com", Personas: []string{"analyst"}}
)

// fixture is a service over memory and the clock it reads.
type fixture struct {
	svc   *Service
	store *memStore
	w     *memWriter
	now   *time.Time
}

func newService() fixture {
	now := at
	st, w := newMemStore(), &memWriter{}
	return fixture{svc: &Service{
		Store: st, Writer: w, BaseURL: func() string { return "https://platform.example.com/" },
		MaxUploadBytes: 1 << 20, Now: func() time.Time { return now },
	}, store: st, w: w, now: &now}
}

func request(maxBytes int64) IssueRequest {
	return IssueRequest{Scope: resource.ScopeUser, ScopeID: "u-1", Path: "sources", Filename: "site.zip", ContentType: "application/zip", MaxBytes: maxBytes}
}

func withTTL(in IssueRequest, ttl time.Duration) IssueRequest {
	in.TTL = ttl
	return in
}

func withFiles(in IssueRequest, n int) IssueRequest {
	in.MaxFiles = n
	return in
}

func tokenOf(t *testing.T, url string) string {
	t.Helper()
	tok, ok := strings.CutPrefix(url, "https://platform.example.com"+RoutePrefix)
	require.True(t, ok, url)
	return tok
}

func zipBytes(n int) []byte {
	return append([]byte("PK\x03\x04"), bytes.Repeat([]byte{7}, n-4)...)
}

// TestIssue: a link names the platform's origin, lives DefaultTTL, and is kept
// by its token's hash alone; the refusals name what to change.
func TestIssue(t *testing.T) {
	f := newService()
	svc, st := f.svc, f.store
	got, err := svc.Issue(context.Background(), request(1000), person, "u-1")
	require.NoError(t, err)
	assert.Equal(t, "PUT", got.Method)
	assert.Equal(t, at.Add(DefaultTTL), got.ExpiresAt)
	assert.Equal(t, int64(1000), got.MaxBytes)
	tok := tokenOf(t, got.URL)
	require.Len(t, st.links, 1)
	for sum := range st.links {
		assert.Equal(t, Hash(tok), sum)
		assert.NotContains(t, sum, tok, "the token is never kept")
	}

	for label, c := range map[string]struct {
		in     IssueRequest
		claims resource.Claims
		want   string
	}{
		"no size":           {request(0), person, "size_bytes is required"},
		"over the limit":    {request(2 << 20), person, "over this deployment's upload limit"},
		"a long life":       {withTTL(request(1), 25*time.Hour), person, "at most 86400 seconds"},
		"another's library": {IssueRequest{Scope: resource.ScopeUser, ScopeID: "u-2", Path: "p", Filename: "a", ContentType: "text/plain", MaxBytes: 1}, person, "cannot write to that library"},
		"too many files":    {withFiles(request(1), 101), person, "1 to 100"},
		"a name and files":  {withFiles(request(1), 2), person, "takes no filename"},
		"no content type":   {IssueRequest{Scope: resource.ScopeUser, ScopeID: "u-1", Path: "p", Filename: "a", MaxBytes: 1}, person, "content_type is required"},
		"a bad path":        {IssueRequest{Scope: resource.ScopeUser, ScopeID: "u-1", Path: "../x", ContentType: "text/plain", MaxBytes: 1}, person, "path"},
		"a bad if_exists":   {IssueRequest{Scope: resource.ScopeUser, ScopeID: "u-1", Path: "p", ContentType: "text/plain", MaxBytes: 1, IfExists: "merge"}, person, "if_exists"},
		"no filename left":  {IssueRequest{Scope: resource.ScopeUser, ScopeID: "u-1", Path: "p", Filename: "///", ContentType: "text/plain", MaxBytes: 1}, person, "plain file name"},
		"a bad scope":       {IssueRequest{Scope: "nowhere", ContentType: "text/plain", MaxBytes: 1}, person, "scope"},
	} {
		_, err := svc.Issue(context.Background(), c.in, c.claims, "u-1")
		require.ErrorIs(t, err, ErrRefused, label)
		assert.Contains(t, err.Error(), c.want, label)
	}
	svc.BaseURL = func() string { return " " }
	_, err = svc.Issue(context.Background(), request(10), person, "u-1")
	assert.ErrorContains(t, err, "portal.public_base_url")
	svc.BaseURL = func() string { return "https://platform.example.com" }
	st.err = errors.New("db down")
	_, err = svc.Issue(context.Background(), request(10), person, "u-1")
	assert.ErrorContains(t, err, "db down")
}

// TestUpload: one PUT files the bytes at the link's address with the type
// detected from them; a second PUT, an expired link and an unknown token are
// one answer; a file past the size is refused whether it said so or not.
func TestUpload(t *testing.T) {
	f := newService()
	svc, st, w, now := f.svc, f.store, f.w, f.now
	issued, err := svc.Issue(context.Background(), request(100), person, "u-1")
	require.NoError(t, err)
	tok := tokenOf(t, issued.URL)
	body := zipBytes(80)
	res, _, err := svc.Upload(context.Background(), tok, "", bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	assert.Equal(t, "r-new", res.ID)
	assert.Equal(t, body, w.body)
	require.Len(t, w.created, 1)
	assert.Equal(t, "site.zip", w.created[0].Filename)
	assert.Equal(t, "sources", w.created[0].Path)
	assert.Equal(t, "application/zip", w.created[0].MIMEType)

	_, _, err = svc.Upload(context.Background(), tok, "", bytes.NewReader(body), int64(len(body)))
	require.ErrorIs(t, err, ErrNoLink, "a link is good for one upload")
	_, _, err = svc.Upload(context.Background(), "unknown", "", bytes.NewReader(body), -1)
	require.ErrorIs(t, err, ErrNoLink)
	_, _, err = svc.Upload(context.Background(), strings.Repeat("x", 200), "", bytes.NewReader(body), -1)
	require.ErrorIs(t, err, ErrNoLink, "a token no link could have is refused before a read")

	stale, err := svc.Issue(context.Background(), request(100), person, "u-1")
	require.NoError(t, err)
	*now = now.Add(DefaultTTL)
	_, _, err = svc.Upload(context.Background(), tokenOf(t, stale.URL), "", bytes.NewReader(body), -1)
	require.ErrorIs(t, err, ErrNoLink, "an expired link")

	*now = at
	said, _ := svc.Issue(context.Background(), request(10), person, "u-1")
	_, _, err = svc.Upload(context.Background(), tokenOf(t, said.URL), "", bytes.NewReader(body), int64(len(body)))
	require.ErrorIs(t, err, ErrTooLarge, "a declared length past the size")
	quiet, _ := svc.Issue(context.Background(), request(10), person, "u-1")
	_, _, err = svc.Upload(context.Background(), tokenOf(t, quiet.URL), "", bytes.NewReader(body), -1)
	require.ErrorIs(t, err, ErrTooLarge, "a stream past the size")
	failures := 0
	for _, f := range st.finished {
		if f[1] != "" {
			failures++
		}
	}
	assert.Equal(t, 2, failures, "a refused upload is recorded on its link")
}

// TestUpload_ReplacesWhenTold: a link issued with if_exists=replace records the
// next version of the file at its address.
func TestUpload_ReplacesWhenTold(t *testing.T) {
	f := newService()
	svc, w := f.svc, f.w
	w.existing = &resource.Resource{ID: "r-old"}
	in := request(100)
	in.IfExists = "replace"
	issued, err := svc.Issue(context.Background(), in, person, "u-1")
	require.NoError(t, err)
	res, _, err := svc.Upload(context.Background(), tokenOf(t, issued.URL), "", bytes.NewReader(zipBytes(50)), 50)
	require.NoError(t, err)
	assert.Equal(t, "r-old", res.ID)
	assert.Equal(t, []string{"r-old"}, w.replaced)
}

// TestPut: the route answers 201 with the filed resource, 404 for a spent
// link, 413 past the size.
func TestPut(t *testing.T) {
	svc := newService().svc
	mux := http.NewServeMux()
	svc.Mount(mux)
	(*Service)(nil).Mount(mux)
	put := func(url string, body []byte) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, strings.TrimPrefix(url, "https://platform.example.com"), bytes.NewReader(body))
		mux.ServeHTTP(rec, req)
		return rec
	}
	issued, err := svc.Issue(context.Background(), request(100), person, "u-1")
	require.NoError(t, err)
	rec := put(issued.URL, zipBytes(60))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"reference":"mcp:resource:r-new"`)
	rec = put(issued.URL, zipBytes(60))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), tokenOf(t, issued.URL), "a refusal never repeats the token")

	small, _ := svc.Issue(context.Background(), request(10), person, "u-1")
	assert.Equal(t, http.StatusRequestEntityTooLarge, put(small.URL, zipBytes(60)).Code)
}

// mounter is a toolkit that issues links.
type mounter struct{ s *Service }

func (m mounter) MountUploadLinks(mux *http.ServeMux) { m.s.Mount(mux) }

// TestMountFrom: the route is mounted from the toolkit that issues links, and
// a toolkit that does not is passed over.
func TestMountFrom(t *testing.T) {
	svc := newService().svc
	mux := http.NewServeMux()
	MountFrom(mux, []any{struct{}{}, mounter{svc}})
	issued, err := svc.Issue(context.Background(), request(100), person, "u-1")
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		strings.TrimPrefix(issued.URL, "https://platform.example.com"), bytes.NewReader(zipBytes(20))))
	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// grants answers a fixed set of granted folders.
type grants struct{ folders []string }

func (g *grants) WritableFolders(context.Context, grantsource.Grantee) ([]string, error) {
	return g.folders, nil
}

// TestUpload_ReadsTheGrantAtTheUpload pins #2684 on an upload link: a link a
// grantee issued for a Global folder they can write is written under the
// grants read when the file arrives, so the upload lands, and a grant revoked
// in between refuses it.
func TestUpload_ReadsTheGrantAtTheUpload(t *testing.T) {
	src := &grants{folders: []string{"reports"}}
	grantsource.Install(src)
	t.Cleanup(func() { grantsource.Install(nil) })
	f := newService()
	in := IssueRequest{Scope: resource.ScopeGlobal, Path: "reports/q3", Filename: "site.zip", ContentType: "application/zip", MaxBytes: 100}
	issued, err := f.svc.Issue(context.Background(), in, person.WithFolderGrants(context.Background()), "u-1")
	require.NoError(t, err, "a grantee may issue a link for a folder they can write")
	stored := f.store.links
	for _, l := range stored {
		l.Claims = resource.Claims{Sub: l.Claims.Sub, Email: l.Claims.Email, Personas: l.Claims.Personas}
	}
	body := zipBytes(40)
	_, _, err = f.svc.Upload(context.Background(), tokenOf(t, issued.URL), "", bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	assert.True(t, f.w.claims.CanWriteFolder(resource.ScopeGlobal, "", "reports/q3"), "the upload is written under the grant read at the upload")

	again, err := f.svc.Issue(context.Background(), in, person.WithFolderGrants(context.Background()), "u-1")
	require.NoError(t, err)
	src.folders = nil
	_, _, err = f.svc.Upload(context.Background(), tokenOf(t, again.URL), "", bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	assert.False(t, f.w.claims.CanWriteFolder(resource.ScopeGlobal, "", "reports/q3"), "a grant revoked before the upload no longer covers it")
}
