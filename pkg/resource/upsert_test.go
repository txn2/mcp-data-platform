package resource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// uploadAt posts one file to the create route as a bulk upload does.
func uploadAt(t *testing.T, h *Handler, ifExists, displayName string, content []byte) (code int, body map[string]any) {
	t.Helper()
	fields := map[string]string{
		"scope": "global", "path": "brand", "display_name": displayName, "description": "logo",
	}
	if ifExists != "" {
		fields[ifExistsField] = ifExists
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, buildMultipartRequest(t, fields, content, "logo.png"))
	if w.Code >= 300 {
		return w.Code, map[string]any{"error": w.Body.String()}
	}
	return w.Code, decodeJSON(t, w.Body)
}

// A created resource records the hash of its bytes on version 1 and says it
// was created.
func TestCreate_RecordsTheContentHash(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	content := []byte("png-bytes-v1")
	code, body := uploadAt(t, fx.handler, "", "Logo", content)
	if code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %v", code, body)
	}
	if body["outcome"] != outcomeCreated {
		t.Errorf("outcome = %v, want created", body["outcome"])
	}
	id, _ := body["id"].(string)
	trail := fx.versions.byResource[id]
	if len(trail) != 1 || trail[0].ContentSHA256 != sha256Hex(content) {
		t.Fatalf("trail = %+v, want version 1 carrying the sha256 of the upload", trail)
	}
}

// Re-uploading a file whose bytes are the ones stored writes nothing: no
// version, no leftover blob, and the answer names the resource that is there.
func TestCreate_SkipUnchanged_SameBytesWritesNothing(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	content := []byte("png-bytes-v1")
	_, first := uploadAt(t, fx.handler, "", "Logo", content)
	blobs := len(fx.s3.objects)

	code, body := uploadAt(t, fx.handler, ifExistsSkipUnchanged, "Logo renamed", content)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", code, body)
	}
	if body["outcome"] != outcomeUnchanged || body["id"] != first["id"] {
		t.Errorf("answer = %v, want outcome unchanged for %v", body, first["id"])
	}
	id, _ := first["id"].(string)
	if n := len(fx.versions.byResource[id]); n != 1 {
		t.Errorf("versions = %d, want 1: an unchanged file records nothing", n)
	}
	if len(fx.s3.objects) != blobs {
		t.Errorf("blobs = %d, want %d: the copy written to compare must be removed", len(fx.s3.objects), blobs)
	}
	if fx.store.resources[id].DisplayName != "Logo" {
		t.Errorf("display name = %q, want the stored one untouched", fx.store.resources[id].DisplayName)
	}
}

// Different bytes at a taken address become that resource's next version, with
// its id and metadata kept.
func TestCreate_SkipUnchanged_ChangedBytesAreTheNextVersion(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	_, first := uploadAt(t, fx.handler, "", "Logo", []byte("png-bytes-v1"))
	changed := []byte("png-bytes-v2")

	code, body := uploadAt(t, fx.handler, ifExistsSkipUnchanged, "Logo renamed", changed)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", code, body)
	}
	if body["outcome"] != outcomeRevised || body["id"] != first["id"] {
		t.Errorf("answer = %v, want outcome revised for %v", body, first["id"])
	}
	id, _ := first["id"].(string)
	trail, _ := fx.versions.ListVersions(context.Background(), id)
	if len(trail) != 2 || trail[0].Version != 2 || trail[0].ContentSHA256 != sha256Hex(changed) {
		t.Fatalf("trail = %+v, want version 2 carrying the new hash", trail)
	}
	head := fx.store.resources[id]
	if !bytes.Equal(fx.s3.objects[head.S3Key], changed) {
		t.Errorf("head blob = %q, want the new bytes", fx.s3.objects[head.S3Key])
	}
	if head.DisplayName != "Logo" {
		t.Errorf("display name = %q, want the stored one untouched", head.DisplayName)
	}

	// The same bytes again are now the unchanged ones.
	code, body = uploadAt(t, fx.handler, ifExistsSkipUnchanged, "Logo", changed)
	if code != http.StatusOK || body["outcome"] != outcomeUnchanged {
		t.Errorf("repeat = %d %v, want 200 unchanged", code, body)
	}
}

// A resource whose version predates hashes has nothing to compare against, so
// its first re-upload is recorded as a version that carries one.
func TestCreate_SkipUnchanged_AnUnhashedVersionIsRevised(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	res := seedResource(fx.store, fx.s3, "legacy", ScopeGlobal, "", "user-123")
	res.Path, res.Filename = "brand", "logo.png"
	res.URI = BuildURI("mcp", ScopeGlobal, "", "brand", "logo.png")
	if _, err := fx.versions.AddRevision(context.Background(), Revision{
		ResourceID: res.ID, MIMEType: res.MIMEType, SizeBytes: res.SizeBytes, S3Key: res.S3Key,
	}); err != nil {
		t.Fatal(err)
	}
	code, body := uploadAt(t, fx.handler, ifExistsSkipUnchanged, "Logo", []byte("hello,world\n"))
	if code != http.StatusOK || body["outcome"] != outcomeRevised {
		t.Fatalf("answer = %d %v, want 200 revised", code, body)
	}
}

// With nothing at the address, skip_unchanged creates as usual.
func TestCreate_SkipUnchanged_FreeAddressCreates(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	code, body := uploadAt(t, fx.handler, ifExistsSkipUnchanged, "Logo", []byte("png"))
	if code != http.StatusCreated || body["outcome"] != outcomeCreated {
		t.Fatalf("answer = %d %v, want 201 created", code, body)
	}
}

// An address a file moved away from is free: the alias trail does not make
// the moved file its occupant.
func TestCreate_SkipUnchanged_AMovedFileLeavesItsAddressFree(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	moved := seedResource(fx.store, fx.s3, "moved", ScopeGlobal, "", "user-123")
	fx.store.aliases[BuildURI("mcp", ScopeGlobal, "", "brand", "logo.png")] = moved.ID
	code, body := uploadAt(t, fx.handler, ifExistsSkipUnchanged, "Logo", []byte("png"))
	if code != http.StatusCreated || body["id"] == moved.ID {
		t.Fatalf("answer = %d %v, want a new resource created", code, body)
	}
}

// Without if_exists a taken address is still the conflict it always was.
func TestCreate_DefaultStillRefusesATakenAddress(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	fx.store.insertErr = errors.New("duplicate key value violates unique constraint")
	for _, v := range []string{"", ifExistsFail} {
		code, _ := uploadAt(t, fx.handler, v, "Logo", []byte("png"))
		if code != http.StatusConflict {
			t.Errorf("if_exists %q: status = %d, want 409", v, code)
		}
	}
}

func TestCreate_RefusesAnUnknownIfExists(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	code, body := uploadAt(t, fx.handler, "replace", "Logo", []byte("png"))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %v", code, body)
	}
}

// Each way the revise path cannot proceed is answered, not swallowed.
func TestCreate_SkipUnchanged_Failures(t *testing.T) {
	seeded := func(t *testing.T) versionedFixture {
		t.Helper()
		fx := newVersionedHandler(t, okExtractor)
		uploadAt(t, fx.handler, "", "Logo", []byte("v1"))
		return fx
	}
	t.Run("the address cannot be read", func(t *testing.T) {
		fx := seeded(t)
		fx.store.getByURIErr = errors.New("connection reset")
		if code, _ := uploadAt(t, fx.handler, ifExistsSkipUnchanged, "Logo", []byte("v2")); code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", code)
		}
	})
	t.Run("the versions cannot be read", func(t *testing.T) {
		fx := seeded(t)
		fx.versions.listErr = errors.New("connection reset")
		if code, _ := uploadAt(t, fx.handler, ifExistsSkipUnchanged, "Logo", []byte("v2")); code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", code)
		}
	})
	t.Run("no version store", func(t *testing.T) {
		fx := seeded(t)
		fx.handler.deps.Versions = nil
		if code, _ := uploadAt(t, fx.handler, ifExistsSkipUnchanged, "Logo", []byte("v2")); code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", code)
		}
	})
	t.Run("the revision cannot be recorded", func(t *testing.T) {
		fx := seeded(t)
		fx.versions.addErr = errors.New("connection reset")
		if code, _ := uploadAt(t, fx.handler, ifExistsSkipUnchanged, "Logo", []byte("v2")); code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", code)
		}
	})
}

func TestWriteUploadError(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{fmt.Errorf("wrapped: %w", errUploadTooLarge), http.StatusBadRequest},
		{&conflictError{msg: "taken"}, http.StatusConflict},
		{&storageError{msg: msgStorageRefused, cause: errors.New("s3")}, http.StatusServiceUnavailable},
		{errors.New("other"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		writeUploadError(w, c.err, 10)
		if w.Code != c.want {
			t.Errorf("%v: status = %d, want %d", c.err, w.Code, c.want)
		}
	}
}

// A head no recorded version names has no hash to compare against.
func TestHeadSHA256_NoVersionNamesTheHead(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	res := seedResource(fx.store, fx.s3, "bare", ScopeGlobal, "", "user-123")
	got, err := headSHA256(context.Background(), fx.handler.deps, res)
	if err != nil || got != "" {
		t.Fatalf("headSHA256 = %q, %v; want empty and no error", got, err)
	}
}
