package resource

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ifExistsField names the create form field that says what an upload does when
// its library, folder and filename already hold a resource. It precedes the
// file part like every other field.
const ifExistsField = "if_exists"

// The if_exists values. Fail is what a create has always done. Skip-unchanged
// is what a bulk upload sends (#1862): a folder re-uploaded after a few of its
// files changed records those as new versions and leaves the rest alone, rather
// than refusing every file or filing a second copy of each.
const (
	ifExistsFail          = "fail"
	ifExistsSkipUnchanged = "skip_unchanged"
)

// The outcomes a create reports, one per file, so a caller uploading many can
// say what happened to each.
const (
	outcomeCreated   = "created"
	outcomeRevised   = "revised"
	outcomeUnchanged = "unchanged"
)

// createdResource is a create's answer: the resource, and what the upload did
// to it. The record is embedded so the response stays the resource every
// client of this route already reads.
type createdResource struct {
	*Resource
	// Outcome is created, revised (the address held a resource and these
	// bytes are its next version) or unchanged (the address held these exact
	// bytes, and nothing was written).
	Outcome string `json:"outcome" example:"created"`
	// TableChanges is what a revision did to the tables registered over the
	// file, as the replace-content route reports it. Absent otherwise.
	TableChanges []string `json:"table_changes,omitempty"`
}

// parseIfExists reads the field, refusing a value that is neither: the two
// differ in whether an existing file changes, and a misspelling that silently
// meant "fail" would read as a platform that lost the write.
func parseIfExists(v string) (skipUnchanged bool, err error) {
	switch strings.TrimSpace(v) {
	case "", ifExistsFail:
		return false, nil
	case ifExistsSkipUnchanged:
		return true, nil
	default:
		return false, fmt.Errorf("if_exists %q is not a value this route takes: %q refuses an address that "+
			"already holds a file, which is the default, and %q skips a file whose bytes are the ones "+
			"already there and records any other as that file's next version", v, ifExistsFail, ifExistsSkipUnchanged)
	}
}

// occupant returns the resource filed at exactly uri, and false when the
// address is free. A hit through the alias table is a file that moved away, so
// its old address is free to create at, as a move treats it.
func occupant(ctx context.Context, deps Deps, uri string) (*Resource, bool, error) {
	existing, err := deps.Store.GetByURI(ctx, uri)
	if err != nil {
		if IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("checking the address: %w", err)
	}
	if existing == nil || existing.URI != uri {
		return nil, false, nil
	}
	return existing, true, nil
}

// occupantUpload is a skip_unchanged upload aimed at one address: the address,
// the file part, and the ceiling a refusal names.
type occupantUpload struct {
	uri   string
	file  *uploadStream
	limit int64
}

// headSHA256 is the hash of the content res holds now: the recorded version
// whose blob the head points at. Empty when that version was written before
// hashes were recorded, which makes the next upload a new version.
func headSHA256(ctx context.Context, deps Deps, res *Resource) (string, error) {
	versions, err := deps.Versions.ListVersions(ctx, res.ID)
	if err != nil {
		return "", fmt.Errorf("reading the versions: %w", err)
	}
	for _, v := range versions {
		if v.S3Key == res.S3Key {
			return v.ContentSHA256, nil
		}
	}
	return "", nil
}

// reviseOccupant handles a skip_unchanged upload to an address that already
// holds a resource: the bytes become its next version, or nothing is written
// when they are the ones it holds. It reports whether it answered the request;
// false means the address is free and the caller creates as usual.
//
// It asks no permission question of its own. The route has already required
// write authority over the library the address is in, and that authority is
// what lets a caller see and change any resource filed there (CanAccessResource,
// CanModifyResource), so the occupant is one the caller may revise.
func (h *Handler) reviseOccupant(w http.ResponseWriter, r *http.Request, claims *Claims, up occupantUpload) bool {
	existing, found, err := occupant(r.Context(), h.deps, up.uri)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return true
	}
	if !found {
		return false
	}
	if h.deps.Versions == nil || h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, msgVersioningUnavailable)
		return true
	}
	head, err := headSHA256(r.Context(), h.deps, existing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return true
	}
	revised, err := h.storeRevision(r.Context(), existing, claims, RevisionUpload{
		Content: up.file.body, MIMEType: up.file.mimeType, SkipIfSHA256: head,
	})
	switch {
	case errors.Is(err, errContentUnchanged):
		writeJSON(w, http.StatusOK, createdResource{Resource: existing, Outcome: outcomeUnchanged})
	case err != nil:
		writeUploadError(w, err, up.limit)
	default:
		writeJSON(w, http.StatusOK, createdResource{
			Resource: revised.Resource, Outcome: outcomeRevised, TableChanges: revised.TableChanges,
		})
		h.notifyCreate(revised.Resource)
	}
	return true
}

// writeUploadError answers a write that failed: a refusal the uploader can fix
// as the 400 it is, an address already taken as 409, storage refusing the
// object as 503, anything else as 500.
func writeUploadError(w http.ResponseWriter, err error, limit int64) {
	if refusal, caller := uploadRefusal(err, limit); caller {
		writeError(w, http.StatusBadRequest, refusal)
		return
	}
	var ce *conflictError
	if errors.As(err, &ce) {
		writeError(w, http.StatusConflict, ce.Error())
		return
	}
	var se *storageError
	if errors.As(err, &se) {
		writeError(w, http.StatusServiceUnavailable, se.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
