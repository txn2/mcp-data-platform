package resource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- fakes ---

// fakeVersions is an in-memory VersionStore that models the Postgres store's
// contract: a missing revision is a WRAPPED sql.ErrNoRows (not a nil result),
// version numbers are assigned by the store, and AddRevision moves the head.
// Modeling the not-found shape matters: the handler distinguishes "no such
// version" (404) from "the read failed" (500) with IsNotFound, and a fake that
// returned a bare error would make both paths look identical in tests while
// production returned 500 for a version a user simply mistyped.
type fakeVersions struct {
	// store is the head the real AddRevision moves in the same transaction it
	// records the version in. The fake moves it too: a fake that recorded the
	// trail without promoting the head would make "the revision is live" untrue
	// in exactly the tests that assert it.
	store      *mockStore
	byResource map[string][]Version
	addErr     error
	listErr    error
	getErr     error
	pruneErr   error
	// pruneKeep records the retention cap the handler asked for.
	pruneKeep int
}

func newFakeVersions(store *mockStore) *fakeVersions {
	return &fakeVersions{store: store, byResource: map[string][]Version{}}
}

func (f *fakeVersions) AddRevision(_ context.Context, rev Revision) (*Version, error) {
	if f.addErr != nil {
		return nil, f.addErr
	}
	next := 1
	for _, v := range f.byResource[rev.ResourceID] {
		if v.Version >= next {
			next = v.Version + 1
		}
	}
	v := Version{
		ResourceID: rev.ResourceID, Version: next, MIMEType: rev.MIMEType,
		SizeBytes: rev.SizeBytes, S3Key: rev.S3Key, UploaderSub: rev.UploaderSub,
		UploaderEmail: rev.UploaderEmail, RestoredFrom: rev.RestoredFrom,
		CreatedAt: time.Now().UTC(),
	}
	f.byResource[rev.ResourceID] = append(f.byResource[rev.ResourceID], v)
	if head, ok := f.store.resources[rev.ResourceID]; ok {
		head.MIMEType, head.SizeBytes, head.S3Key = rev.MIMEType, rev.SizeBytes, rev.S3Key
		head.UpdatedAt = v.CreatedAt
	}
	return &v, nil
}

func (f *fakeVersions) ListVersions(_ context.Context, resourceID string) ([]Version, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := append([]Version(nil), f.byResource[resourceID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (f *fakeVersions) GetVersion(_ context.Context, resourceID string, version int) (*Version, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, v := range f.byResource[resourceID] {
		if v.Version == version {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("scanning resource version: %w", sql.ErrNoRows)
}

func (f *fakeVersions) PruneVersions(_ context.Context, resourceID string, keep int) ([]Version, error) {
	f.pruneKeep = keep
	if f.pruneErr != nil {
		return nil, f.pruneErr
	}
	all := f.byResource[resourceID]
	if len(all) <= keep {
		return nil, nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Version < all[j].Version })
	cut := len(all) - keep
	pruned := append([]Version(nil), all[:cut]...)
	f.byResource[resourceID] = append([]Version(nil), all[cut:]...)
	return pruned, nil
}

// recordingRecorder captures the read events a surface reports.
type recordingRecorder struct {
	events []ReadEvent
}

func (r *recordingRecorder) RecordRead(_ context.Context, ev ReadEvent) {
	r.events = append(r.events, ev)
}

// fakeUsage returns canned usage for the detail read.
type fakeUsage struct {
	usage map[string]Usage
	err   error
}

func (f fakeUsage) ResourceUsage(_ context.Context, ids []string) (map[string]Usage, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]Usage{}
	for _, id := range ids {
		if u, ok := f.usage[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

// seedResourceID is the id every fixture in this file seeds under.
const seedResourceID = "res-1"

// versionedFixture is a handler with the version store, recorder, and usage
// reader wired, plus the backing fakes a test asserts on.
type versionedFixture struct {
	handler  *Handler
	store    *mockStore
	s3       *mockS3
	versions *fakeVersions
	reads    *recordingRecorder
}

func newVersionedHandler(t *testing.T, extractFn ClaimsExtractor) versionedFixture {
	t.Helper()
	store, s3 := newMockStore(), newMockS3()
	versions := newFakeVersions(store)
	rec := &recordingRecorder{}
	h := NewHandler(Deps{
		Store:        store,
		S3Client:     s3,
		S3Bucket:     "test-bucket",
		URIScheme:    "mcp",
		Versions:     versions,
		ReadRecorder: rec,
	}, extractFn, nil)
	return versionedFixture{handler: h, store: store, s3: s3, versions: versions, reads: rec}
}

// seedVersionedResource seeds a resource AND records it as version 1, which is
// the state every resource is in in production: the create path records version
// 1 from the uploaded blob, and migration 000092 backfilled the same row for
// resources uploaded before versioning existed. Tests that assert over the full
// blob set need it, because a head blob no version row names is a state the
// running system does not produce.
func seedVersionedResource(t *testing.T, store *mockStore, s3 *mockS3, versions *fakeVersions) {
	t.Helper()
	res := seedResource(store, s3, seedResourceID, ScopeGlobal, "", "user-123")
	if _, err := versions.AddRevision(context.Background(), Revision{
		ResourceID: res.ID, MIMEType: res.MIMEType, SizeBytes: res.SizeBytes,
		S3Key: res.S3Key, UploaderSub: res.UploaderSub, UploaderEmail: res.UploaderEmail,
	}); err != nil {
		t.Fatalf("seed version 1: %v", err)
	}
}

// --- replace content ---

func TestReplaceContent_KeepsIdentityAndRecordsVersion(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
	res := seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
	originalURI, originalKey, originalFilename := res.URI, res.S3Key, res.Filename

	req := buildMultipartRequest(t, nil, []byte("revised,content\n1,2\n"), "renamed-by-user.csv")
	req.URL.Path = "/api/v1/resources/res-1/content"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	body := decodeJSON(t, w.Body)
	if body["id"] != "res-1" {
		t.Errorf("id = %v, want res-1 (revision must not mint a new ID)", body["id"])
	}
	if body["uri"] != originalURI {
		t.Errorf("uri = %v, want %s (revision must not move the URI)", body["uri"], originalURI)
	}
	if body["filename"] != originalFilename {
		t.Errorf("filename = %v, want %s (the upload's own name must not rename the resource)", body["filename"], originalFilename)
	}

	stored := store.resources["res-1"]
	if stored.S3Key == originalKey {
		t.Error("head still points at the original blob; the revision was not promoted")
	}
	if got := string(s3.objects[stored.S3Key]); got != "revised,content\n1,2\n" {
		t.Errorf("head blob = %q, want the revised content", got)
	}
	if _, stillThere := s3.objects[originalKey]; !stillThere {
		t.Error("prior version's blob was deleted; history must remain readable")
	}
	if got := len(versions.byResource["res-1"]); got != 1 {
		t.Fatalf("recorded versions = %d, want 1", got)
	}
	if versions.byResource["res-1"][0].SizeBytes != int64(len("revised,content\n1,2\n")) {
		t.Errorf("version size = %d, want the revised byte count", versions.byResource["res-1"][0].SizeBytes)
	}
}

func TestReplaceContent_PrunesAtRetentionCap(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
	seedVersionedResource(t, store, s3, versions)

	// Revisions past a cap of 2 must leave the oldest blobs deleted.
	h.deps.MaxVersions = 2
	for i := range 3 {
		req := buildMultipartRequest(t, nil, fmt.Appendf(nil, "rev-%d", i), "f.csv")
		req.URL.Path = "/api/v1/resources/res-1/content"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("revision %d: status = %d: %s", i, w.Code, w.Body.String())
		}
	}

	if versions.pruneKeep != 2 {
		t.Errorf("prune keep = %d, want the configured cap of 2", versions.pruneKeep)
	}
	kept := versions.byResource["res-1"]
	if len(kept) != 2 {
		t.Fatalf("kept versions = %d, want 2", len(kept))
	}
	for _, v := range kept {
		if _, ok := s3.objects[v.S3Key]; !ok {
			t.Errorf("version %d blob missing; only pruned versions may lose their blob", v.Version)
		}
	}
	// Four versions existed (the seeded v1 plus three revisions); only the two
	// kept ones may still have bytes.
	if len(s3.objects) != 2 {
		t.Errorf("objects in storage = %d, want 2 (the kept versions); pruned blobs were not deleted", len(s3.objects))
	}
}

func TestReplaceContent_Rejections(t *testing.T) {
	tests := []struct {
		name      string
		extractor ClaimsExtractor
		setup     func(*Handler, *mockStore, *mockS3, *fakeVersions)
		id        string
		content   []byte
		want      int
	}{
		{
			name: "unknown resource is not found", extractor: okExtractor,
			id: "missing", content: []byte("x"), want: http.StatusNotFound,
		},
		{
			name: "unauthenticated", extractor: failExtractor,
			id: "res-1", content: []byte("x"), want: http.StatusUnauthorized,
		},
		{
			name: "resource outside the caller's scopes is not found", extractor: memberExtractor,
			setup: func(_ *Handler, s *mockStore, s3 *mockS3, _ *fakeVersions) {
				seedResource(s, s3, "res-other", ScopeUser, "someone-else", "someone-else")
			},
			id: "res-other", content: []byte("x"), want: http.StatusNotFound,
		},
		{
			name: "no version store means no revision", extractor: okExtractor,
			setup: func(h *Handler, _ *mockStore, _ *mockS3, _ *fakeVersions) { h.deps.Versions = nil },
			id:    "res-1", content: []byte("x"), want: http.StatusServiceUnavailable,
		},
		{
			name: "empty upload is rejected", extractor: okExtractor,
			id: "res-1", content: nil, want: http.StatusBadRequest,
		},
		{
			name: "a failed revision record is a server error", extractor: okExtractor,
			setup: func(_ *Handler, _ *mockStore, _ *mockS3, v *fakeVersions) {
				v.addErr = errors.New("insert failed")
			},
			id: "res-1", content: []byte("x"), want: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newVersionedHandler(t, tt.extractor)
			h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
			seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
			if tt.setup != nil {
				tt.setup(h, store, s3, versions)
			}
			req := buildMultipartRequest(t, nil, tt.content, "f.csv")
			req.URL.Path = "/api/v1/resources/" + tt.id + "/content"
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestReplaceContent_FailedRecordRemovesTheOrphanedBlob(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
	versions.addErr = errors.New("insert failed")
	before := len(s3.objects)

	req := buildMultipartRequest(t, nil, []byte("data"), "f.csv")
	req.URL.Path = "/api/v1/resources/res-1/content"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got := len(s3.objects); got != before {
		t.Errorf("objects in storage = %d, want %d: the blob of a revision that was never recorded must not survive", got, before)
	}
}

func TestReplaceContent_RequiresModifyPermission(t *testing.T) {
	fx := newVersionedHandler(t, memberExtractor)
	h, store, s3 := fx.handler, fx.store, fx.s3
	// A persona resource the member can READ but has no write authority over.
	seedResource(store, s3, "res-p", ScopePersona, "analyst", "another-user")

	req := buildMultipartRequest(t, nil, []byte("data"), "f.csv")
	req.URL.Path = "/api/v1/resources/res-p/content"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: a reader must not be able to revise another user's material", w.Code)
	}
}

// --- version history ---

func TestListVersions_ReportsTrailAndCurrent(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3 := fx.handler, fx.store, fx.s3
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
	for i := range 2 {
		req := buildMultipartRequest(t, nil, fmt.Appendf(nil, "rev-%d", i), "f.csv")
		req.URL.Path = "/api/v1/resources/res-1/content"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/versions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	body := decodeJSON(t, w.Body)
	list, _ := body["versions"].([]any)
	if len(list) != 2 {
		t.Fatalf("versions = %d, want 2", len(list))
	}
	if got, _ := body["current"].(float64); got != 2 {
		t.Errorf("current = %v, want 2 (the version the head points at)", got)
	}
	if got, _ := body["max_versions"].(float64); got != DefaultMaxVersions {
		t.Errorf("max_versions = %v, want the default %d", got, DefaultMaxVersions)
	}
	first, _ := list[0].(map[string]any)
	if first["uploader_email"] != "user@example.com" {
		t.Errorf("uploader_email = %v, want the revising caller's", first["uploader_email"])
	}
}

func TestListVersions_Failures(t *testing.T) {
	t.Run("store read failure is a server error", func(t *testing.T) {
		fx := newVersionedHandler(t, okExtractor)
		h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
		seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
		versions.listErr = errors.New("db down")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/versions", nil))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.Code)
		}
	})

	t.Run("no version store is service unavailable", func(t *testing.T) {
		fx := newVersionedHandler(t, okExtractor)
		h, store, s3 := fx.handler, fx.store, fx.s3
		seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
		h.deps.Versions = nil
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/versions", nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
	})
}

func TestGetVersionContent(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, rec := fx.handler, fx.store, fx.s3, fx.reads
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
	req := buildMultipartRequest(t, nil, []byte("first revision"), "f.csv")
	req.URL.Path = "/api/v1/resources/res-1/content"
	h.ServeHTTP(httptest.NewRecorder(), req)

	t.Run("serves the recorded bytes and records the read", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/v1/resources/res-1/versions/1/content", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if got := w.Body.String(); got != "first revision" {
			t.Errorf("body = %q, want the recorded revision's bytes", got)
		}
		if len(rec.events) == 0 {
			t.Fatal("no read event recorded for a version download")
		}
		last := rec.events[len(rec.events)-1]
		if last.Surface != SurfaceDownload || last.Version != 1 || last.ResourceID != "res-1" {
			t.Errorf("event = %+v, want a rest_download of res-1 version 1", last)
		}
	})

	t.Run("unknown version is not found", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/v1/resources/res-1/versions/99/content", nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})

	t.Run("malformed version is a bad request", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/v1/resources/res-1/versions/zero/content", nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})

	t.Run("a failed version read is a server error, not a 404", func(t *testing.T) {
		fx2 := newVersionedHandler(t, okExtractor)
		h2, store2, s32, versions2 := fx2.handler, fx2.store, fx2.s3, fx2.versions
		seedResource(store2, s32, "res-1", ScopeGlobal, "", "user-123")
		versions2.getErr = errors.New("db down")
		w := httptest.NewRecorder()
		h2.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/v1/resources/res-1/versions/1/content", nil))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.Code)
		}
	})
}

// --- restore ---

func TestRestoreVersion_RoundTripsBytesAsANewHead(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	for _, content := range []string{"version one", "version two"} {
		req := buildMultipartRequest(t, nil, []byte(content), "f.csv")
		req.URL.Path = "/api/v1/resources/res-1/content"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/resources/res-1/versions/1/restore", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	head := store.resources["res-1"]
	if got := string(s3.objects[head.S3Key]); got != "version one" {
		t.Errorf("restored head content = %q, want the restored version's bytes exactly", got)
	}
	all := versions.byResource["res-1"]
	if len(all) != 3 {
		t.Fatalf("versions = %d, want 3 (restore appends a head revision, it does not rewind)", len(all))
	}
	newest := all[len(all)-1]
	if newest.RestoredFrom == nil || *newest.RestoredFrom != 1 {
		t.Errorf("restored_from = %v, want 1", newest.RestoredFrom)
	}
	if head.SizeBytes != int64(len("version one")) {
		t.Errorf("head size = %d, want the restored byte count", head.SizeBytes)
	}
}

func TestRestoreVersion_Rejections(t *testing.T) {
	t.Run("reader cannot restore", func(t *testing.T) {
		fx := newVersionedHandler(t, memberExtractor)
		h, store, s3 := fx.handler, fx.store, fx.s3
		seedResource(store, s3, "res-p", ScopePersona, "analyst", "another-user")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost,
			"/api/v1/resources/res-p/versions/1/restore", nil))
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
	})

	t.Run("missing blob is a server error", func(t *testing.T) {
		fx := newVersionedHandler(t, okExtractor)
		h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
		res := seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
		if _, err := versions.AddRevision(context.Background(), Revision{
			ResourceID: res.ID, MIMEType: res.MIMEType, S3Key: "resources/global/res-1/v/gone/test.csv",
		}); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost,
			"/api/v1/resources/res-1/versions/1/restore", nil))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.Code)
		}
	})
}

// --- read recording and usage on the existing routes ---

// The portal's library draws an image section from the resources' own bytes,
// there being no stored thumbnail for a resource. A page view is therefore a
// read of every image in view, which must not read as somebody using the file
// (#1471). The read is still audited under the caller's identity; only the door
// it is recorded as changes.
func TestGetContent_PreviewIsRecordedAsItsOwnSurface(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, rec := fx.handler, fx.store, fx.s3, fx.reads
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/resources/res-1/content?preview=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(rec.events) != 1 {
		t.Fatalf("recorded events = %d, want 1: a preview is still a read", len(rec.events))
	}
	if got := rec.events[0].Surface; got != SurfacePreview {
		t.Errorf("surface = %q, want %q", got, SurfacePreview)
	}
	if rec.events[0].UserID != "user-123" {
		t.Errorf("caller = %q, want the authenticated caller: a preview names the reason, not the reader",
			rec.events[0].UserID)
	}
}

// Only the exact declaration counts. Anything else is the download it has
// always been, so a mistyped or absent parameter cannot quietly stop a real
// download from moving the curation signal.
func TestGetContent_OnlyPreviewOneIsAPreview(t *testing.T) {
	for _, query := range []string{"", "?preview=0", "?preview=true", "?preview="} {
		t.Run("query="+query, func(t *testing.T) {
			fx := newVersionedHandler(t, okExtractor)
			h, store, s3, rec := fx.handler, fx.store, fx.s3, fx.reads
			seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
				"/api/v1/resources/res-1/content"+query, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if got := rec.events[0].Surface; got != SurfaceDownload {
				t.Errorf("surface = %q, want %q", got, SurfaceDownload)
			}
		})
	}
}

func TestGetContent_RecordsARead(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, rec := fx.handler, fx.store, fx.s3, fx.reads
	res := seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/content", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(rec.events) != 1 {
		t.Fatalf("recorded events = %d, want 1", len(rec.events))
	}
	ev := rec.events[0]
	if ev.ResourceID != "res-1" || ev.URI != res.URI || ev.Surface != SurfaceDownload {
		t.Errorf("event = %+v, want a rest_download naming res-1 and its URI", ev)
	}
	if ev.UserID != "user-123" || ev.UserEmail != "user@example.com" {
		t.Errorf("event caller = %q/%q, want the authenticated caller", ev.UserID, ev.UserEmail)
	}
	if ev.Version != 0 {
		t.Errorf("version = %d, want 0 (the head was served, no version was named)", ev.Version)
	}
}

func TestGetContent_ServesWithoutARecorder(t *testing.T) {
	store, s3 := newMockStore(), newMockS3()
	h := NewHandler(Deps{Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp"}, okExtractor, nil)
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/content", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: with audit disabled a read must still serve", w.Code)
	}
}

func TestGet_IncludesUsageWhenAvailable(t *testing.T) {
	store, s3 := newMockStore(), newMockS3()
	last := time.Now().UTC()
	h := NewHandler(Deps{
		Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp",
		Usage: fakeUsage{usage: map[string]Usage{"res-1": {
			Reads30d: 4, Reads90d: 9, LastReadAt: &last,
			BySurface30d: map[string]int64{SurfaceMCPRead: 3, SurfaceDownload: 1},
		}}},
	}, okExtractor, nil)
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := decodeJSON(t, w.Body)
	usage, ok := body["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage absent from the detail read: %s", w.Body.String())
	}
	reads30, _ := usage["reads_30d"].(float64)
	reads90, _ := usage["reads_90d"].(float64)
	if reads30 != 4 || reads90 != 9 {
		t.Errorf("usage counts = %v/%v, want 4/9", reads30, reads90)
	}
	surfaces, _ := usage["by_surface_30d"].(map[string]any)
	if mcpReads, _ := surfaces[SurfaceMCPRead].(float64); mcpReads != 3 {
		t.Errorf("mcp_read count = %v, want 3", mcpReads)
	}
}

func TestGet_UsageFailureDoesNotFailTheRead(t *testing.T) {
	store, s3 := newMockStore(), newMockS3()
	h := NewHandler(Deps{
		Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp",
		Usage: fakeUsage{err: errors.New("audit unavailable")},
	}, okExtractor, nil)
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: a usage rollup failure must not fail the metadata read", w.Code)
	}
	if _, present := decodeJSON(t, w.Body)["usage"]; present {
		t.Error("usage present despite the rollup failing")
	}
}

// --- create and delete integration with the trail ---

func TestCreate_RecordsVersionOne(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, versions := fx.handler, fx.versions
	req := buildMultipartRequest(t, map[string]string{
		"scope": "global", "path": "samples", "display_name": "Seed", "description": "d",
	}, []byte("a,b\n1,2\n"), "seed.csv")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	id, _ := decodeJSON(t, w.Body)["id"].(string)
	trail := versions.byResource[id]
	if len(trail) != 1 || trail[0].Version != 1 {
		t.Fatalf("trail = %+v, want a single version 1: the history must start at upload", trail)
	}
	if trail[0].S3Key == "" || trail[0].SizeBytes == 0 {
		t.Errorf("version 1 = %+v, want the uploaded blob's key and size", trail[0])
	}
}

func TestDelete_RemovesEveryVersionBlob(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
	seedVersionedResource(t, store, s3, versions)
	for i := range 2 {
		req := buildMultipartRequest(t, nil, fmt.Appendf(nil, "rev-%d", i), "f.csv")
		req.URL.Path = "/api/v1/resources/res-1/content"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-1", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if len(s3.objects) != 0 {
		t.Errorf("objects left in storage = %d, want 0: deleting a resource must not orphan its version blobs", len(s3.objects))
	}
}

func TestDelete_SurvivesAVersionListFailure(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
	versions.listErr = errors.New("db down")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-1", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: an unreadable trail must not make a resource undeletable", w.Code)
	}
}

// --- list ordering ---

func TestList_ForwardsTheSortParameter(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3 := fx.handler, fx.store, fx.s3
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/resources?sort=last_read", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if store.lastListFilter.Sort != SortLastRead {
		t.Errorf("filter sort = %q, want %q", store.lastListFilter.Sort, SortLastRead)
	}
}

func TestSortOrderByClause(t *testing.T) {
	tests := map[Sort]string{
		SortLastRead: "last_read_at DESC NULLS LAST, updated_at DESC",
		SortUpdated:  "updated_at DESC",
		"":           "updated_at DESC",
		"; DROP":     "updated_at DESC",
	}
	for order, want := range tests {
		if got := order.orderByClause(); got != want {
			t.Errorf("Sort(%q).orderByClause() = %q, want %q", order, got, want)
		}
	}
	if strings.Contains(Sort("; DROP TABLE resources").orderByClause(), "DROP") {
		t.Error("an unrecognized sort value reached the SQL")
	}
}

// --- storage failure paths ---

// failingS3 wraps mockS3, failing the chosen operations so the handler's
// storage-failure paths are exercised against a client that behaves like a
// blob store having a bad day.
type failingS3 struct {
	*mockS3
	putErr    error
	getErr    error
	deleteErr error
}

func (f *failingS3) PutObject(ctx context.Context, bucket, key string, data []byte, ct string) error {
	if f.putErr != nil {
		return f.putErr
	}
	return f.mockS3.PutObject(ctx, bucket, key, data, ct)
}

func (f *failingS3) GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error) {
	if f.getErr != nil {
		return nil, "", f.getErr
	}
	return f.mockS3.GetObject(ctx, bucket, key)
}

func (f *failingS3) DeleteObject(ctx context.Context, bucket, key string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return f.mockS3.DeleteObject(ctx, bucket, key)
}

func TestReplaceContent_BlobWriteFailureIsAServerError(t *testing.T) {
	store, s3 := newMockStore(), newMockS3()
	versions := newFakeVersions(store)
	failing := &failingS3{mockS3: s3, putErr: errors.New("bucket unavailable")}
	h := NewHandler(Deps{
		Store: store, S3Client: failing, S3Bucket: "test-bucket", URIScheme: "mcp", Versions: versions,
	}, okExtractor, nil)
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	req := buildMultipartRequest(t, nil, []byte("data"), "f.csv")
	req.URL.Path = "/api/v1/resources/res-1/content"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if len(versions.byResource["res-1"]) != 0 {
		t.Error("a revision was recorded for bytes that were never stored")
	}
}

func TestPruneFailuresDoNotFailTheRevision(t *testing.T) {
	t.Run("a failed prune query", func(t *testing.T) {
		fx := newVersionedHandler(t, okExtractor)
		h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
		seedVersionedResource(t, store, s3, versions)
		versions.pruneErr = errors.New("db down")

		req := buildMultipartRequest(t, nil, []byte("data"), "f.csv")
		req.URL.Path = "/api/v1/resources/res-1/content"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: a storage-cleanup problem must not fail a committed revision", w.Code)
		}
	})

	t.Run("a pruned blob that resists deletion", func(t *testing.T) {
		store, s3 := newMockStore(), newMockS3()
		versions := newFakeVersions(store)
		failing := &failingS3{mockS3: s3, deleteErr: errors.New("access denied")}
		h := NewHandler(Deps{
			Store: store, S3Client: failing, S3Bucket: "test-bucket", URIScheme: "mcp",
			Versions: versions, MaxVersions: 2,
		}, okExtractor, nil)
		seedVersionedResource(t, store, s3, versions)

		for i := range 3 {
			req := buildMultipartRequest(t, nil, fmt.Appendf(nil, "rev-%d", i), "f.csv")
			req.URL.Path = "/api/v1/resources/res-1/content"
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("revision %d: status = %d, want 200", i, w.Code)
			}
		}
	})
}

func TestGetVersionContent_BlobReadFailureIsAServerError(t *testing.T) {
	store, s3 := newMockStore(), newMockS3()
	versions := newFakeVersions(store)
	failing := &failingS3{mockS3: s3}
	h := NewHandler(Deps{
		Store: store, S3Client: failing, S3Bucket: "test-bucket", URIScheme: "mcp", Versions: versions,
	}, okExtractor, nil)
	seedVersionedResource(t, store, s3, versions)
	failing.getErr = errors.New("bucket unavailable")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/resources/res-1/versions/1/content", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestCreate_SucceedsWhenTheInitialVersionCannotBeRecorded(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, versions := fx.handler, fx.versions
	versions.addErr = errors.New("db down")

	req := buildMultipartRequest(t, map[string]string{
		"scope": "global", "path": "samples", "display_name": "Seed", "description": "d",
	}, []byte("a,b\n"), "seed.csv")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: an unrecordable trail must not fail the upload itself", w.Code)
	}
}
