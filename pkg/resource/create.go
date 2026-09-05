package resource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// NewResource is a resource about to exist: where it is filed, what it is
// called, and the bytes it starts life with.
//
// Every field is already validated by the time it reaches CreateResource. The
// caller owns validation because the two surfaces read their input from
// different places -- a multipart form, a tool call -- and reporting a bad
// folder path as a form error or as a tool result is theirs to decide.
type NewResource struct {
	Scope       Scope
	ScopeID     string
	Path        string
	Filename    string
	DisplayName string
	Description string
	Tags        []string
	// Content is the bytes to store, and MIMEType the type they are stored
	// under. It is a reader rather than a slice because the upload route hands
	// over the multipart part itself, so a file crosses the platform without
	// ever existing in it whole (#1631); a caller that is already holding the
	// object passes a bytes.Reader over it. Nil stores an empty object.
	Content  io.Reader
	MIMEType string
	// DeclaredMIMEType is what the caller said the bytes were, kept so a
	// detection that replaced it is recorded. Empty when nothing was declared.
	DeclaredMIMEType string
}

// CreateResource stores the blob, inserts the metadata row, and records the
// content as version 1 so the trail starts at the upload rather than at the
// first revision. A metadata failure removes the blob, so a failed create
// leaves nothing behind.
//
// It is exported for the same reason ReviseContent is: creating a managed
// resource is not the browser's alone. An agent, and therefore a scheduled
// script, creates one through here (#1487), so a resource written on the
// platform's own initiative is the same record as one a person uploaded --
// same URI, same version trail, same retention.
func CreateResource(ctx context.Context, deps Deps, claims *Claims, in NewResource) (*Resource, error) {
	id, err := GenerateID()
	if err != nil {
		return nil, fmt.Errorf("generating ID: %w", err)
	}

	scheme := deps.URIScheme
	if scheme == "" {
		scheme = DefaultURIScheme
	}
	uri := BuildURI(scheme, in.Scope, in.ScopeID, in.Path, in.Filename)
	s3Key := BuildS3Key(in.Scope, in.ScopeID, id, in.Filename)

	// A stored type that disagrees with what the client sent is the one thing
	// an operator cannot reconstruct after the fact, so record the swap. Both
	// types trace back to a client-supplied value, so both are sanitized
	// before they reach the log.
	if in.MIMEType != in.DeclaredMIMEType {
		slog.Info("resource upload: content type detected from content",
			logKeyResourceID, id,
			"declared_mime_type", logsan.SanitizeForLog(in.DeclaredMIMEType),
			"stored_mime_type", logsan.SanitizeForLog(in.MIMEType),
		)
	}

	// The size is what the write reported, not what the caller declared: a
	// streamed body carries no length, so the bytes that reached storage are
	// the only account of how big the file is.
	size, err := storeContent(ctx, deps, s3Key, in.Content, in.MIMEType)
	if err != nil {
		return nil, contentWriteError("resource upload", err)
	}

	// The subject is the principal that made the call and the address is the
	// person whose authority it ran under. They are the same for a person, and
	// for a managed-script run they are script:<name> and its version author --
	// the pairing every other surface records for a run, and the one the
	// version trail has to carry or a scheduled refresh reads as having been
	// made by whoever happens to own the script.
	res := Resource{
		ID: id, Scope: in.Scope, ScopeID: in.ScopeID,
		Path: in.Path, Filename: in.Filename,
		DisplayName: in.DisplayName, Description: in.Description,
		MIMEType: in.MIMEType, SizeBytes: size,
		S3Key: s3Key, URI: uri, Tags: in.Tags,
		UploaderSub: claims.Sub, UploaderEmail: PersonAddress(*claims),
	}

	if err := deps.Store.Insert(ctx, res); err != nil {
		// Clean up orphaned S3 blob.
		if deps.S3Client != nil {
			_ = deps.S3Client.DeleteObject(ctx, deps.S3Bucket, s3Key)
		}
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return nil, &conflictError{msg: "a resource with this library, folder, and filename already exists"}
		}
		slog.Error("resource upload: db insert failed", msgError, err)
		return nil, fmt.Errorf("saving resource metadata: %w", err)
	}

	saved := readBackCreated(ctx, deps, id, res)
	recordInitialVersion(ctx, deps, saved, claims)
	noteProducer(ctx, deps, claims, producedby.Write{TargetID: saved.ID, Created: true, Version: 1})
	return saved, nil
}

// readBackCreated re-reads the inserted row so the caller sees the stored
// timestamps. A read that fails falls back to the record as written, stamped
// now: the resource exists either way, and reporting the create as a failure
// because the read-back failed would be a lie about what happened.
func readBackCreated(ctx context.Context, deps Deps, id string, written Resource) *Resource {
	saved, err := deps.Store.Get(ctx, id)
	if err != nil || saved == nil {
		now := time.Now().UTC()
		written.CreatedAt = now
		written.UpdatedAt = now
		return &written
	}
	return saved
}

// recordInitialVersion records the created resource as version 1 so the trail
// starts at upload rather than at the first revision. A failure is logged, not
// surfaced: the upload succeeded and the resource is usable; the migration's
// backfill shape (a v1 row derived from the resource row) is exactly what a
// later repair would write.
func recordInitialVersion(ctx context.Context, deps Deps, res *Resource, claims *Claims) {
	if deps.Versions == nil {
		return
	}
	if _, err := deps.Versions.AddRevision(ctx, Revision{
		ResourceID:    res.ID,
		MIMEType:      res.MIMEType,
		SizeBytes:     res.SizeBytes,
		S3Key:         res.S3Key,
		UploaderSub:   claims.Sub,
		UploaderEmail: PersonAddress(*claims),
	}); err != nil {
		slog.Warn("resource upload: recording initial version failed", msgError, err,
			logKeyResourceID, res.ID) // #nosec G706 -- server-generated ID
	}
}

// storeContent streams content to blob storage under key and reports the
// number of bytes it wrote.
//
// A deployment with no blob client stores nothing, and still draws the body to
// its end and counts it: the caller's reader is a request body either way, and
// a record whose size disagreed with the content it was created from would be
// wrong in the one place nothing can recompute.
func storeContent(ctx context.Context, deps Deps, key string, body io.Reader, mimeType string) (int64, error) {
	if body == nil {
		body = bytes.NewReader(nil)
	}
	if deps.S3Client == nil {
		n, err := io.Copy(io.Discard, body)
		if err != nil {
			return 0, fmt.Errorf("reading the content: %w", err)
		}
		return n, nil
	}
	written, err := deps.S3Client.PutObjectStream(ctx, deps.S3Bucket, key, body, mimeType)
	if err != nil {
		return 0, err //nolint:wrapcheck // classified by contentWriteError, which needs the cause intact
	}
	return written, nil
}

// contentWriteError separates the two ways a streamed write ends badly, which
// are opposite answers to whoever is uploading.
//
// A request the caller can fix -- the file passed the ceiling, or the form put
// a part behind the file -- travels intact, so the route can render it as the
// 400 it is. Anything else is the platform's: storage refused the object, or
// the body stopped arriving. Those read the same to the uploader (nothing was
// saved, try again) and the mechanism belongs in the log beside it rather than
// in the response.
func contentWriteError(what string, err error) error {
	if errors.Is(err, errUploadTooLarge) || errors.Is(err, errFilePartLast) {
		return err
	}
	slog.Error(what+": storing the content failed", msgError, err)
	return &storageError{msg: msgStorageRefused, cause: err}
}
